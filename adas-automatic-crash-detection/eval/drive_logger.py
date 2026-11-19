#!/usr/bin/env python3
"""Car test: record everything the ESP32 sees and decides, plus the
passenger's own marks, to a folder on the laptop.

The ESP32 stays plugged into the laptop by USB for the whole drive (that
also powers it). The script switches the board into car-test mode ($RAW,1),
so it streams every raw sample and every engine event. The passenger presses
a key whenever they FEEL something, which is the ground truth the report
compares the detections against:

    p  pothole            b  speed bump        r  rough road / cobblestone
    h  hard braking       x  anything else      c  cancel a crash alarm
    q  stop and save

    .venv\\Scripts\\python eval\\drive_logger.py                 (finds the board)
    .venv\\Scripts\\python eval\\drive_logger.py --port COM5 --name route1
    .venv\\Scripts\\python eval\\drive_logger.py --list          (show serial ports)

Output: data/drives/<date>_<time>[_name]/ with raw.csv (every sample),
events.csv (every detection), marks.csv (the key presses), console.log and
session.json. Then run eval/drive_report.py for the counts and the map.
Nothing here changes the detection; it only records it.
"""
from __future__ import annotations

import argparse
import csv
import json
import sys
import threading
import time
from collections import Counter
from datetime import datetime
from pathlib import Path

try:
    import serial
    import serial.tools.list_ports
except ImportError:  # pragma: no cover
    raise SystemExit("pyserial missing: .venv\\Scripts\\pip install pyserial")

ROOT = Path(__file__).resolve().parent.parent
DRIVES = ROOT / "data" / "drives"

EVENT_NAMES = {1: "CRASH", 2: "ROLLOVER", 3: "POTHOLE", 4: "SPEED_BUMP",
               5: "ALARM_CANCELLED", 6: "ALARM_CONFIRMED", 7: "SENSOR_FAULT"}
KEYS = {"p": "pothole", "b": "speed_bump", "r": "rough_road", "h": "hard_brake", "x": "other"}


def frame(body: str) -> bytes:
    """$BODY*CS with the NMEA-style XOR checksum (protocol.md)."""
    cs = 0
    for ch in body.encode("ascii"):
        cs ^= ch
    return f"${body}*{cs:02X}\r\n".encode("ascii")


def pick_port(explicit: str | None) -> str:
    if explicit:
        return explicit
    ports = list(serial.tools.list_ports.comports())
    usb = [p for p in ports if any(k in (p.description or "") + (p.hwid or "")
                                   for k in ("CP210", "CH340", "CH910", "USB", "VID:PID"))]
    cands = usb or ports
    if len(cands) == 1:
        print(f"using {cands[0].device} ({cands[0].description})")
        return cands[0].device
    if not cands:
        raise SystemExit("no serial port found: is the ESP32 plugged in (and its USB driver installed)?")
    print("several serial ports found, pick one with --port:")
    for p in cands:
        print(f"  {p.device:8s} {p.description}")
    raise SystemExit(1)


class Logger:
    def __init__(self, port: str, baud: int, out: Path):
        self.out = out
        self.ser = serial.serial_for_url(port, baudrate=baud, timeout=0.1)
        self.wlock = threading.Lock()
        self.plock = threading.Lock()
        self.stop = threading.Event()
        self.t0 = time.time()
        self.last_raw = 0.0
        self.last_any = 0.0
        self.last_raw_req = 0.0
        self.n_raw = 0
        self.n_gaps = 0
        self.last_n = None
        self.events = Counter()
        self.marks = Counter()
        self.board_line = ""
        self.seen_seq: set[tuple[str, int]] = set()
        self.alarm_active = False
        self.thread: threading.Thread | None = None

        self.f_raw = open(out / "raw.csv", "w", newline="")
        self.f_ev = open(out / "events.csv", "w", newline="")
        self.f_mk = open(out / "marks.csv", "w", newline="")
        self.f_fr = open(out / "frames.csv", "w", newline="")
        self.f_log = open(out / "console.log", "w", encoding="utf-8")
        self.w_raw = csv.writer(self.f_raw)
        self.w_ev = csv.writer(self.f_ev)
        self.w_mk = csv.writer(self.f_mk)
        self.w_fr = csv.writer(self.f_fr)
        self.w_raw.writerow(["host_t", "n", "esp_ms", "ax", "ay", "az", "gx", "gy", "gz"])
        self.w_ev.writerow(["host_t", "n", "esp_ms", "code", "event"])
        self.w_mk.writerow(["host_t", "key", "label"])
        self.w_fr.writerow(["host_t", "type", "seq", "body"])

    # -- output helpers ---------------------------------------------------
    def say(self, msg: str) -> None:
        with self.plock:
            print(f"{datetime.now():%H:%M:%S}  {msg}", flush=True)

    def send(self, data: bytes) -> None:
        with self.wlock:
            try:
                self.ser.write(data)
            except serial.SerialException as exc:
                self.say(f"!! write failed: {exc}")

    # -- reader thread ----------------------------------------------------
    def reader(self) -> None:
        buf = b""
        while not self.stop.is_set():
            try:
                chunk = self.ser.read(4096)
            except Exception as exc:
                if self.stop.is_set():        # we are shutting down
                    return
                self.say(f"!! serial port lost ({exc}). Check the USB cable; saving what we have.")
                self.stop.set()
                return
            if not chunk:
                continue
            buf += chunk
            *lines, buf = buf.split(b"\n")
            now = time.time()
            for raw in lines:
                line = raw.decode("ascii", errors="replace").strip()
                if line:
                    self.last_any = now
                    try:
                        self.handle(line, now)
                    except Exception as exc:  # a garbled line must never stop the log
                        self.f_log.write(f"{now:.3f}\t!! {exc!r} on {line!r}\n")

    def handle(self, line: str, now: float) -> None:
        if line.startswith("R,"):
            p = line.split(",")
            if len(p) != 9:
                return
            n = int(p[1])
            if self.last_n is not None and n != self.last_n + 1 and n > self.last_n:
                self.n_gaps += n - self.last_n - 1
            self.last_n = n
            self.w_raw.writerow([f"{now:.3f}"] + p[1:])
            self.n_raw += 1
            self.last_raw = now
            return

        self.f_log.write(f"{now:.3f}\t{line}\n")
        if line.startswith("E,"):
            _, n, ms, code = line.split(",")
            code = int(code)
            name = EVENT_NAMES.get(code, f"code{code}")
            self.w_ev.writerow([f"{now:.3f}", n, ms, code, name])
            self.f_ev.flush()
            self.events[name] += 1
            self.announce(name)
        elif line.startswith(">"):
            body = line.lstrip("> ").replace("(retry) ", "")
            if body.startswith("$") and "*" in body:
                body = body[1:body.rindex("*")]
                parts = body.split(",")
                kind = parts[0]
                if kind in ("EVT", "ALM") and len(parts) > 1:
                    seq = int(parts[1])
                    self.send(frame(f"ACK,{seq}"))   # no Jetson in the car: we ack
                    if (kind, seq) in self.seen_seq:
                        return
                    self.seen_seq.add((kind, seq))
                    self.w_fr.writerow([f"{now:.3f}", kind, seq, body])
                    self.f_fr.flush()
                    if kind == "EVT" and parts[2] == "POTHOLE" and len(parts) > 4:
                        self.say(f"         pothole severity {parts[4]} g")
                    elif kind == "EVT" and parts[2] == "CRASH" and len(parts) > 8:
                        self.say(f"         peak {parts[4]} g, score {parts[7]}, tier {parts[8]}")
        elif line.startswith("#"):
            if "RetroADAS crash unit" in line:
                self.board_line = line[1:].strip()
                self.say(f"board: {self.board_line}")
                if "NOT FOUND" in line:
                    self.say("!! the board cannot see the MPU-6050: check the 4 sensor wires")
            elif "raw stream on" in line:
                self.say("board is streaming. Recording.")

    def announce(self, name: str) -> None:
        if name == "CRASH" or name == "ROLLOVER":
            self.alarm_active = True
            self.say(f"*** {name} ALARM: buzzer is on. Press c (or the button) to cancel if nothing happened.")
        elif name == "ALARM_CANCELLED":
            self.alarm_active = False
            self.say("alarm cancelled")
        elif name == "ALARM_CONFIRMED":
            self.alarm_active = False
            self.say("alarm was NOT cancelled (would have called for help)")
        elif name == "POTHOLE":
            self.say(">>> POTHOLE detected")
        elif name == "SPEED_BUMP":
            self.say("    speed bump recognised (not reported)")
        else:
            self.say(f"!! {name}")

    # -- main thread ------------------------------------------------------
    def mark(self, key: str) -> None:
        now = time.time()
        if key == "c":
            self.send(frame("CANCEL"))
            self.w_mk.writerow([f"{now:.3f}", "c", "cancel_alarm"])
            self.say("sent cancel")
        else:
            label = KEYS[key]
            self.w_mk.writerow([f"{now:.3f}", key, label])
            self.marks[label] += 1
            self.say(f"[mark] {label}")
        self.f_mk.flush()

    def housekeeping(self) -> None:
        now = time.time()
        if now - self.last_raw > 2.0 and now - self.last_raw_req > 2.0:
            self.send(frame("RAW,1"))
            self.last_raw_req = now
            if self.n_raw and now - self.last_raw > 3.0:
                self.say("!! no data from the board for 3 s (loose cable? board restarted?)")
            elif not self.n_raw and now - self.t0 > 8.0 and int(now - self.t0) % 10 < 2:
                if self.last_any:
                    self.say("!! board talks but does not stream: is the car-test firmware flashed?")
                else:
                    self.say("!! nothing from the board yet: right port? board powered?")
        for f in (self.f_raw, self.f_log):
            f.flush()

    def status(self) -> None:
        mins = (time.time() - self.t0) / 60
        ev = ", ".join(f"{k.lower()} {v}" for k, v in sorted(self.events.items())) or "none"
        mk = ", ".join(f"{k} {v}" for k, v in sorted(self.marks.items())) or "none"
        drop = f", {self.n_gaps} samples lost" if self.n_gaps else ""
        self.say(f"[{mins:5.1f} min] {self.n_raw / 100:7.0f} s of data{drop} | detected: {ev} | marked: {mk}")

    def close(self) -> dict:
        self.send(frame("RAW,0"))
        time.sleep(0.2)
        self.stop.set()
        if self.thread is not None:
            self.thread.join(timeout=2.0)
        try:
            self.ser.close()
        except Exception:
            pass
        for f in (self.f_raw, self.f_ev, self.f_mk, self.f_fr, self.f_log):
            f.close()
        return {"samples": self.n_raw, "samples_lost": self.n_gaps,
                "events": dict(self.events), "marks": dict(self.marks)}


def key_source(stop: threading.Event):
    """Yield single keys: msvcrt on Windows, line input elsewhere."""
    try:
        import msvcrt  # type: ignore

        def gen():
            while not stop.is_set():
                if msvcrt.kbhit():
                    yield msvcrt.getwch().lower()
                else:
                    time.sleep(0.05)
                    yield None
        return gen()
    except ImportError:
        import queue
        q: "queue.Queue[str]" = queue.Queue()

        def feed():
            for ln in sys.stdin:
                for ch in ln.strip().lower()[:1]:
                    q.put(ch)

        threading.Thread(target=feed, daemon=True).start()

        def gen():
            while not stop.is_set():
                try:
                    yield q.get(timeout=0.05)
                except queue.Empty:
                    yield None
        return gen()


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--port", help="COM port (e.g. COM5) or a pyserial URL; default: auto-detect")
    ap.add_argument("--baud", type=int, default=115200)
    ap.add_argument("--name", default="", help="short label added to the folder name, e.g. route1")
    ap.add_argument("--minutes", type=float, default=0, help="stop by itself after this long (0 = until q)")
    ap.add_argument("--list", action="store_true", help="list serial ports and exit")
    a = ap.parse_args()

    if a.list:
        for p in serial.tools.list_ports.comports():
            print(f"{p.device:8s} {p.description}")
        return

    port = pick_port(a.port)
    stamp = datetime.now().strftime("%Y-%m-%d_%H%M%S")
    out = DRIVES / (stamp + (f"_{a.name}" if a.name else ""))
    out.mkdir(parents=True, exist_ok=True)

    lg = Logger(port, a.baud, out)
    meta = {"started": datetime.now().isoformat(timespec="seconds"), "started_unix": lg.t0,
            "port": port, "baud": a.baud, "name": a.name}
    lg.thread = threading.Thread(target=lg.reader, daemon=True)
    lg.thread.start()

    print(f"\nRecording to {out}")
    print("Passenger keys:  p pothole   b speed bump   r rough road   h hard brake   x other")
    print("                 c cancel a crash alarm      q stop and save\n")
    lg.send(frame("RAW,1"))
    lg.last_raw_req = time.time()

    last_status = time.time()
    deadline = lg.t0 + a.minutes * 60 if a.minutes > 0 else None
    try:
        for k in key_source(lg.stop):
            if k == "q":
                break
            if k and (k in KEYS or k == "c"):
                lg.mark(k)
            lg.housekeeping()
            if time.time() - last_status >= 30:
                last_status = time.time()
                lg.status()
            if deadline and time.time() >= deadline:
                break
    except KeyboardInterrupt:
        pass

    lg.status()
    summary = lg.close()
    meta.update({"stopped": datetime.now().isoformat(timespec="seconds"), "board": lg.board_line, **summary})
    (out / "session.json").write_text(json.dumps(meta, indent=2))
    print(f"\nSaved in {out}")
    print("Next: copy the phone's Sensor Logger export (.zip) into that folder, then run")
    print(f"  .venv\\Scripts\\python eval\\drive_report.py \"{out}\"")


if __name__ == "__main__":
    main()
