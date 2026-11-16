#!/usr/bin/env python3
"""Jetson side of the Feature 3 link (protocol.md).

    python3 jetson/crash_link.py --port /dev/ttyTHS1                 # on the Jetson
    python3 jetson/crash_link.py --port COM5                         # laptop + real ESP32
    python3 jetson/crash_link.py --tcp 9000                          # laptop demo: eval/replay.py connects here

What it does:
  * verifies checksums, ACKs every $EVT/$ALM, logs every line to crash_link.log
  * sends $SPD once a second from the GPS source (stub: --gps-fake 40)
  * on $ALM,CONFIRMED -> accident report: POST /v1/hazards event_tag=accident + emergency stub
  * on $EVT,POTHOLE   -> POST /v1/hazards event_tag=pothole
Without --cloud-url the posts are printed instead of sent.
"""
from __future__ import annotations

import argparse
import json
import socket
import sys
import time
from datetime import datetime, timezone

LOG = "crash_link.log"


def nmea_sum(body: str) -> int:
    x = 0
    for ch in body.encode():
        x ^= ch
    return x


def frame(body: str) -> bytes:
    return f"${body}*{nmea_sum(body):02X}\n".encode()


def parse(line: str):
    line = line.strip()
    if not line.startswith("$") or "*" not in line:
        return None
    body, _, cs = line[1:].rpartition("*")
    try:
        if int(cs, 16) != nmea_sum(body):
            return None
    except ValueError:
        return None
    return body.split(",")


class Link:
    """Serial or TCP transport with the same read/write interface."""

    def __init__(self, port: str | None, tcp: int | None, baud: int):
        self.ser = None
        self.sock = None
        self.buf = b""
        if port:
            import serial  # pyserial
            self.ser = serial.Serial(port, baud, timeout=0.05)
        elif tcp:
            srv = socket.socket()
            srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            srv.bind(("127.0.0.1", tcp))
            srv.listen(1)
            print(f"waiting for a connection on 127.0.0.1:{tcp} (eval/replay.py --tcp {tcp}) ...")
            self.sock, _ = srv.accept()
            self.sock.settimeout(0.05)
            print("connected")
        else:
            raise SystemExit("need --port or --tcp")

    def readlines(self):
        try:
            data = self.ser.read(256) if self.ser else self.sock.recv(256)
        except (socket.timeout, OSError):
            data = b""
        if data == b"" and self.sock:
            return []
        self.buf += data
        out = []
        while b"\n" in self.buf:
            line, _, self.buf = self.buf.partition(b"\n")
            out.append(line.decode(errors="replace"))
        return out

    def write(self, data: bytes):
        if self.ser:
            self.ser.write(data)
        else:
            try:
                self.sock.sendall(data)
            except OSError:
                raise ConnectionError("link closed by the other side") from None


class Cloud:
    def __init__(self, url: str | None, device_id: str, api_key: str):
        self.url, self.device_id, self.api_key = url, device_id, api_key

    def post_hazard(self, event_tag: str, lat: float, lon: float, heading: float | None,
                    confidence: float, extra: dict):
        body = {"event_tag": event_tag, "lat": lat, "lng": lon, "heading_deg": heading,
                "confidence": confidence, "reported_by": self.device_id, **extra}
        if not self.url:
            print(f"[cloud stub] POST /v1/hazards {json.dumps(body)}")
            return
        import urllib.request
        req = urllib.request.Request(f"{self.url}/v1/hazards", data=json.dumps(body).encode(),
                                     headers={"Content-Type": "application/json",
                                              "X-Device-Id": self.device_id,
                                              "X-Api-Key": self.api_key})
        try:
            with urllib.request.urlopen(req, timeout=5) as r:
                print(f"[cloud] {r.status} {event_tag}")
        except Exception as e:  # noqa: BLE001
            print(f"[cloud] FAILED {e}")

    def emergency_contact(self, report: dict):
        # stub — per handover.md a dummy function / simple API call is fine for now
        print(f"[emergency stub] would call/SMS the emergency contact with {json.dumps(report)}")


class Gps:
    """Replace with the real GPS feed on the Jetson (gpsd / NMEA serial)."""

    def __init__(self, fake_kmh: float | None):
        self.fake_kmh = fake_kmh

    def fix(self) -> dict:
        return {"lat": 33.6420, "lon": 72.9830, "heading": 90.0,
                "speed_kmh": self.fake_kmh if self.fake_kmh is not None else -1.0,
                "valid": self.fake_kmh is not None}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port")
    ap.add_argument("--tcp", type=int)
    ap.add_argument("--baud", type=int, default=115200)
    ap.add_argument("--cloud-url")
    ap.add_argument("--device-id", default="retroadas-dev-0")
    ap.add_argument("--api-key", default="")
    ap.add_argument("--gps-fake", type=float, default=None, help="constant fake speed km/h")
    args = ap.parse_args()

    link = Link(args.port, args.tcp, args.baud)
    cloud = Cloud(args.cloud_url, args.device_id, args.api_key)
    gps = Gps(args.gps_fake)
    log = open(LOG, "a", encoding="utf-8")
    last_spd = 0.0
    last_event: dict = {}

    def logline(direction: str, line: str):
        ts = datetime.now(timezone.utc).isoformat(timespec="milliseconds")
        log.write(f"{ts} {direction} {line}\n")
        log.flush()

    print("crash_link running; Ctrl-C to stop")
    while True:
        now = time.time()
        if now - last_spd >= 1.0:
            last_spd = now
            f = gps.fix()
            link.write(frame(f"SPD,{f['speed_kmh']:.1f},{1 if f['valid'] else 0}"))
        for raw in link.readlines():
            logline("<", raw.strip())
            fields = parse(raw)
            if not fields:
                continue
            typ = fields[0]
            if typ in ("EVT", "ALM"):
                link.write(frame(f"ACK,{fields[1]}"))
            if typ == "HB":
                state = {"0": "idle", "1": "ALARM", "2": "lockout"}.get(fields[3], fields[3])
                print(f"hb seq={fields[1]} up={fields[2]}s state={state} fault={fields[4]}")
            elif typ == "EVT":
                kind = fields[2]
                fix = gps.fix()
                print(f"EVENT {kind} {fields[3:]}")
                if kind == "POTHOLE":
                    sev = float(fields[4])
                    cloud.post_hazard("pothole", fix["lat"], fix["lon"], fix["heading"],
                                      confidence=min(1.0, sev / 2.0), extra={"severity_g": sev})
                else:
                    last_event = {"kind": kind, "fields": fields[3:], "fix": fix,
                                  "t": datetime.now(timezone.utc).isoformat()}
                    print("  alarm started on the unit; driver has the cancel window")
            elif typ == "ALM":
                if fields[2] == "CANCELLED":
                    print(f"ALARM CANCELLED by driver ({fields[4]})")
                elif fields[2] == "CONFIRMED":
                    fix = gps.fix()
                    report = {"device_id": args.device_id, "cause": fields[4],
                              "peak_g": float(fields[5]), "speed_kmh": float(fields[6]),
                              "lat": fix["lat"], "lon": fix["lon"],
                              "t": datetime.now(timezone.utc).isoformat(), "trigger": last_event}
                    print(f"ACCIDENT CONFIRMED: {json.dumps(report)}")
                    cloud.post_hazard("accident", fix["lat"], fix["lon"], fix["heading"],
                                      confidence=1.0, extra={"peak_g": report["peak_g"]})
                    cloud.emergency_contact(report)
            elif typ == "LOG":
                pass   # already in crash_link.log; keep the terminal quiet
        time.sleep(0.01)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(0)
    except ConnectionError as e:
        print(f"{e}; exiting")
        sys.exit(0)
