#!/usr/bin/env python3
"""Replay a recorded clip through the engine in real time, the way the ESP32
would see it, and print/emit events.

    # laptop-only: print events as they happen
    .venv\\Scripts\\python eval\\replay.py vzcrash --label crash --n 1
    .venv\\Scripts\\python eval\\replay.py pvs --drive 3 --start 300 --seconds 120
    .venv\\Scripts\\python eval\\replay.py roadsens --file 103

    # emulate the ESP32 over TCP so jetson/crash_link.py --tcp 9000 shows the
    # whole chain (event -> ack -> countdown -> cloud stub):
    .venv\\Scripts\\python eval\\replay.py vzcrash --label crash --n 1 --tcp 9000 --countdown 5

    # stream $SIM lines into a real ESP32 on COM5 (its buzzer/LED react):
    .venv\\Scripts\\python eval\\replay.py vzcrash --label crash --n 1 --port COM5

--speed 0 runs as fast as possible (no real-time pacing).
"""
from __future__ import annotations

import argparse
import socket
import sys
import time
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parent))
import datasets as ds  # noqa: E402
from engine import EV_NAMES, Engine  # noqa: E402
from engine import EV_CRASH, EV_POTHOLE, EV_ROLLOVER  # noqa: E402

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "jetson"))
from crash_link import frame  # noqa: E402


def pick_clip(args):
    if args.source == "vzcrash":
        want = {"crash": 0, "near_miss": 1, "normal": 2}[args.label]
        k = 0
        for shard, eid, lab, acc, gyr, spd, vs in ds.iter_vzcrash("test"):
            if lab != want:
                continue
            if k == args.n:
                return f"VZCrash {eid} ({args.label})", acc, gyr, spd
            k += 1
        raise SystemExit("no such event")
    if args.source == "pvs":
        d = ds.load_pvs(args.drive)
        a, b = int(args.start * 100), int((args.start + args.seconds) * 100)
        return d.name, d.acc[a:b], d.gyr[a:b], None
    if args.source == "roadsens":
        for p in ds.roadsens_files():
            if p.stem.split(" ")[0] == str(args.file):
                d = ds.load_roadsens(p)
                return d.name, d.acc, d.gyr, None
        raise SystemExit("no such file")
    raise SystemExit(args.source)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("source", choices=["vzcrash", "pvs", "roadsens"])
    ap.add_argument("--label", default="crash", choices=["crash", "near_miss", "normal"])
    ap.add_argument("--n", type=int, default=0, help="which event of that label")
    ap.add_argument("--drive", type=int, default=1)
    ap.add_argument("--file", type=int, default=1)
    ap.add_argument("--start", type=float, default=0.0)
    ap.add_argument("--seconds", type=float, default=60.0)
    ap.add_argument("--speed", type=float, default=1.0, help="1 = real time, 0 = max")
    ap.add_argument("--tcp", type=int, help="emulate the ESP32 to crash_link.py on this port")
    ap.add_argument("--port", help="stream $SIM lines to a real ESP32 on this serial port")
    ap.add_argument("--countdown", type=float, default=15.0, help="alarm countdown (s) in TCP mode")
    args = ap.parse_args()

    name, acc, gyr, spd = pick_clip(args)
    n = len(acc)
    print(f"replaying {name}: {n / 100:.1f} s, peak |a| {np.linalg.norm(acc, axis=1).max():.2f} g")

    sock = ser = None
    if args.tcp:
        sock = socket.create_connection(("127.0.0.1", args.tcp))
        sock.setblocking(False)
        print(f"connected to crash_link on {args.tcp}")

    def drain():
        """Print what the Jetson side sends back ($SPD, $ACK) without blocking."""
        if not sock:
            return
        try:
            data = sock.recv(4096)
        except BlockingIOError:
            return
        for line in data.decode(errors="replace").split("\n"):
            if line.startswith("$ACK"):
                print(f"  jetson: {line.strip()}")
    if args.port:
        import serial
        ser = serial.Serial(args.port, 115200, timeout=0)
        time.sleep(2.0)   # ESP32 resets on port open
        print(f"streaming $SIM to {args.port}; the board's own engine decides")

    e = Engine(alarm_ms=int(args.countdown * 1000))
    seq = 0
    t0 = time.perf_counter()
    last_hb = -1
    last_feat = np.zeros(8, np.float32)   # strongest recent capture, for the report lines
    for i in range(n):
        if args.speed > 0:
            target = t0 + i * 0.01 / args.speed
            while time.perf_counter() < target:
                time.sleep(0.001)
        t_ms = i * 10
        if spd is not None and i % 100 == 0 and i // 100 < len(spd):
            e.feed_speed(float(spd[i // 100]), t_ms)
        if ser:
            a, g = acc[i], gyr[i]
            ser.write(frame(f"SIM,{a[0]:.3f},{a[1]:.3f},{a[2]:.3f},{g[0]:.1f},{g[1]:.1f},{g[2]:.1f}"))
            while ser.in_waiting:
                line = ser.readline().decode(errors="replace").rstrip()
                if line:
                    print(f"  ESP32: {line}")
            continue
        ev, feats = e.run(acc[i:i + 1], gyr[i:i + 1])
        ev = int(ev[0])
        if len(feats):
            last_feat = feats[-1]
        if sock and i // 100 != last_hb:
            last_hb = i // 100
            drain()
            seq += 1
            sock.sendall(frame(f"HB,{seq},{i // 100},0,0,{i}"))
        if ev:
            print(f"t={i / 100:6.2f}s  {EV_NAMES[ev]}")
            if sock:
                seq += 1
                if ev == EV_CRASH:
                    f = last_feat
                    sock.sendall(frame(f"EVT,{seq},CRASH,{t_ms},{f[0]:.2f},{f[1]:.2f},{f[2]:.2f},0.00,3,-1"))
                elif ev == EV_ROLLOVER:
                    sock.sendall(frame(f"EVT,{seq},ROLLOVER,{t_ms},65.0,-1"))
                elif ev == EV_POTHOLE:
                    sock.sendall(frame(f"EVT,{seq},POTHOLE,{t_ms},1.00,-1"))
                elif ev == 5:
                    sock.sendall(frame(f"ALM,{seq},CANCELLED,{t_ms},CRASH"))
                elif ev == 6:
                    sock.sendall(frame(f"ALM,{seq},CONFIRMED,{t_ms},CRASH,{last_feat[0]:.2f},-1"))
    # in TCP mode keep running an idle tail so the countdown can finish
    if sock:
        tail = int(args.countdown * 100) + 200
        quiet_a = np.tile(acc[-1], (tail, 1))
        quiet_g = np.zeros((tail, 3), np.float32)
        for i in range(tail):
            if args.speed > 0:
                time.sleep(0.01 / args.speed)
            ev, feats = e.run(quiet_a[i:i + 1], quiet_g[i:i + 1])
            ev = int(ev[0])
            if ev == 6:
                seq += 1
                sock.sendall(frame(f"ALM,{seq},CONFIRMED,{(n + i) * 10},CRASH,{last_feat[0]:.2f},-1"))
                print(f"t={(n + i) / 100:6.2f}s  ALARM_CONFIRMED")
            elif ev == 5:
                print(f"t={(n + i) / 100:6.2f}s  ALARM_CANCELLED")
        sock.close()
    print("done")


if __name__ == "__main__":
    main()
