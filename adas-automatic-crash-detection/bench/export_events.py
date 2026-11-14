#!/usr/bin/env python3
"""Export VZCrash parquet shards to a flat binary the C benchmark can stream.

Binary layout (little-endian), one file per shard:

    uint32  magic  = 0x56435231  ("VCR1")
    uint32  n_events
    per event:
        int64   event_id
        int32   label          (0=crash, 1=near_miss, 2=normal_driving)
        int32   n_imu          (100 Hz samples)
        int32   n_speed        (1 Hz samples)
        int32   reserved       (0)
        float32 gsensor[n_imu][3]   (g)
        float32 gyro[n_imu][3]      (deg/s)
        float32 gps_speed[n_speed]  (km/h)

Usage:
    .venv/bin/python3 bench/export_events.py            # both shards
    .venv/bin/python3 bench/export_events.py 0          # shard 0 only
"""
import struct
import sys
from pathlib import Path

import numpy as np
import pyarrow.parquet as pq

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "data" / "vzcrash"
DST = ROOT / "data" / "bench"
MAGIC = 0x56435231


def export_shard(idx: int) -> None:
    src = SRC / f"test-0000{idx}-of-00002.parquet"
    dst = DST / f"test-0000{idx}.bin"
    DST.mkdir(parents=True, exist_ok=True)

    pf = pq.ParquetFile(src)
    n_events = pf.metadata.num_rows
    print(f"{src.name}: {n_events} events -> {dst}")

    with open(dst, "wb") as f:
        f.write(struct.pack("<II", MAGIC, n_events))
        written = 0
        for rg in range(pf.metadata.num_row_groups):
            tbl = pf.read_row_group(
                rg, columns=["event_id", "label", "gsensor", "gyro", "gps_speed"]
            )
            ids = tbl["event_id"].to_pylist()
            labels = tbl["label"].to_pylist()
            gs = tbl["gsensor"].to_pylist()
            gy = tbl["gyro"].to_pylist()
            sp = tbl["gps_speed"].to_pylist()
            for eid, lab, a, g, s in zip(ids, labels, gs, gy, sp):
                a = np.asarray(a, dtype="<f4")
                g = np.asarray(g, dtype="<f4")
                s = np.asarray(s, dtype="<f4")
                if a.shape[0] != g.shape[0]:
                    # one event in shard 1 has accel/gyro of different lengths;
                    # keep the overlapping prefix so both streams stay aligned
                    n = min(a.shape[0], g.shape[0])
                    print(f"  event {eid}: accel {a.shape[0]} vs gyro {g.shape[0]} "
                          f"samples, truncating to {n}")
                    a, g = a[:n], g[:n]
                assert a.shape[1] == 3 and g.shape[1] == 3
                f.write(struct.pack("<qiiii", eid, lab, a.shape[0], s.shape[0], 0))
                f.write(a.tobytes())
                f.write(g.tobytes())
                f.write(s.tobytes())
                written += 1
            print(f"  row group {rg}: {written}/{n_events}")
    assert written == n_events


if __name__ == "__main__":
    shards = [int(x) for x in sys.argv[1:]] or [0, 1]
    for i in shards:
        export_shard(i)
