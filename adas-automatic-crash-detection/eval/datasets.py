"""Loaders for the three datasets the evaluation uses. Every loader yields
`Drive` objects in the engine's units: accel in g (gravity included),
gyro in deg/s, 100 Hz, plus optional GPS speed (km/h) and labels.

    PVS        data/pvs/pvs{1..9}_gps_mpu_left.csv + pvs{i}_labels.csv
    RoadSens   data/roadsens/x/*.csv
    VZCrash    data/vzcrash/*.parquet
"""
from __future__ import annotations

import glob
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterator

import numpy as np
import pandas as pd

ROOT = Path(__file__).resolve().parent.parent
DATA = ROOT / "data"
G = 9.80665
RAD2DEG = 57.29577951308232
VZ_LABELS = {0: "crash", 1: "near_miss", 2: "normal_driving"}


@dataclass
class Drive:
    name: str
    acc: np.ndarray                 # (n, 3) g
    gyr: np.ndarray                 # (n, 3) deg/s
    t: np.ndarray                   # (n,) seconds
    speed_kmh: np.ndarray | None = None   # (n,) or None
    labels: pd.DataFrame | None = None    # per-sample, aligned with acc
    meta: dict = field(default_factory=dict)

    @property
    def hours(self) -> float:
        return float(self.t[-1] - self.t[0]) / 3600.0 if len(self.t) > 1 else 0.0


# ---------------------------------------------------------------------------
# PVS — Passive Vehicular Sensors (Menegazzo & von Wangenheim), dashboard MPU-9250
# ---------------------------------------------------------------------------

PVS_COLS = ["timestamp", "acc_x_dashboard", "acc_y_dashboard", "acc_z_dashboard",
            "gyro_x_dashboard", "gyro_y_dashboard", "gyro_z_dashboard", "speed"]


def pvs_available() -> list[int]:
    out = []
    for i in range(1, 10):
        f = DATA / "pvs" / f"pvs{i}_gps_mpu_left.csv"
        lab = DATA / "pvs" / f"pvs{i}_labels.csv"
        if f.exists() and f.stat().st_size > 50_000_000 and lab.exists() and lab.stat().st_size > 1_000_000:
            out.append(i)
    return out


def load_pvs(i: int) -> Drive:
    f = DATA / "pvs" / f"pvs{i}_gps_mpu_left.csv"
    df = pd.read_csv(f, usecols=PVS_COLS)
    lab = pd.read_csv(DATA / "pvs" / f"pvs{i}_labels.csv")
    n = min(len(df), len(lab))
    df, lab = df.iloc[:n], lab.iloc[:n].reset_index(drop=True)
    acc = df[["acc_x_dashboard", "acc_y_dashboard", "acc_z_dashboard"]].to_numpy(np.float32) / G
    gyr = df[["gyro_x_dashboard", "gyro_y_dashboard", "gyro_z_dashboard"]].to_numpy(np.float32)
    t = df["timestamp"].to_numpy(np.float64)
    spd = df["speed"].to_numpy(np.float32) * 3.6
    return Drive(f"PVS {i}", acc, gyr, t, spd, lab, {"source": "pvs"})


def iter_pvs() -> Iterator[Drive]:
    for i in pvs_available():
        yield load_pvs(i)


# ---------------------------------------------------------------------------
# RoadSens-4M (Khandakar et al. 2025), smartphone via Sensor Logger, 100 Hz
# ---------------------------------------------------------------------------

def roadsens_files() -> list[Path]:
    files = []
    for f in sorted(glob.glob(str(DATA / "roadsens" / "x" / "*.csv"))):
        p = Path(f)
        if re.match(r"^\d+", p.stem):
            files.append(p)
    return sorted(files, key=lambda p: int(re.match(r"^\d+", p.stem).group()))


def load_roadsens(p: Path) -> Drive | None:
    head = pd.read_csv(p, nrows=1)
    if "seconds_elapsed" not in head.columns:
        return None   # 99.csv is a summary table, not a session
    cols = ["seconds_elapsed", "totalAcceleration_x", "totalAcceleration_y", "totalAcceleration_z",
            "gyroscope_x", "gyroscope_y", "gyroscope_z"]
    has_ann = "annotation_text" in head.columns
    if has_ann:
        cols.append("annotation_text")
    df = pd.read_csv(p, usecols=cols)
    df = df.dropna(subset=cols[:7])
    acc = df[["totalAcceleration_x", "totalAcceleration_y", "totalAcceleration_z"]].to_numpy(np.float32) / G
    gyr = df[["gyroscope_x", "gyroscope_y", "gyroscope_z"]].to_numpy(np.float32) * RAD2DEG
    t = df["seconds_elapsed"].to_numpy(np.float64)
    ann = df["annotation_text"].fillna("").astype(str).to_numpy() if has_ann else np.array([""] * len(df))
    lab = pd.DataFrame({"pothole": ann == "Pothole", "bump": ann == "Bump"})
    normal = "Normal" in p.stem
    return Drive(f"RoadSens {p.stem}", acc, gyr, t, None, lab,
                 {"source": "roadsens", "normal_road": normal})


def iter_roadsens() -> Iterator[Drive]:
    for p in roadsens_files():
        d = load_roadsens(p)
        if d is not None and len(d.t) > 200:
            yield d


def intervals(mask: np.ndarray) -> list[tuple[int, int]]:
    """Contiguous True runs as (start, end) index pairs, end exclusive."""
    m = np.asarray(mask, dtype=bool)
    if not m.any():
        return []
    d = np.diff(np.concatenate([[0], m.astype(np.int8), [0]]))
    starts = np.nonzero(d == 1)[0]
    ends = np.nonzero(d == -1)[0]
    return list(zip(starts.tolist(), ends.tolist()))


# ---------------------------------------------------------------------------
# VZCrash (Verizon Connect), parquet shards, 100 Hz IMU + 1 Hz GPS speed
# ---------------------------------------------------------------------------

def vzcrash_shards(split: str) -> list[Path]:
    return sorted((DATA / "vzcrash").glob(f"{split}-*.parquet"))


def iter_vzcrash(split: str, columns=("event_id", "label", "gsensor", "gyro", "gps_speed", "vehiclesize")):
    """Yield (shard_name, event_id, label, acc, gyr, speed_1hz, vehiclesize) per event,
    streaming one row group at a time so memory stays flat."""
    import pyarrow as pa
    import pyarrow.parquet as pq

    def split_list(col: pa.ChunkedArray, width: int):
        """list<fixed_size_list<float>[width]> -> list of (n_i, width) float32 views."""
        col = col.combine_chunks()
        offs = col.offsets.to_numpy()
        flat = col.flatten()                     # fixed_size_list<float>[width]
        if width > 1:
            flat = flat.flatten()                # float
        vals = flat.to_numpy(zero_copy_only=False).astype(np.float32, copy=False)
        if width > 1:
            vals = vals.reshape(-1, width)
        return [vals[offs[i]:offs[i + 1]] for i in range(len(offs) - 1)]

    for shard in vzcrash_shards(split):
        pf = pq.ParquetFile(shard)
        avail = [c for c in columns if c in pf.schema_arrow.names]
        for rg in range(pf.metadata.num_row_groups):
            tbl = pf.read_row_group(rg, columns=avail)
            ids = tbl["event_id"].to_pylist()
            labs = tbl["label"].to_pylist()
            accs = split_list(tbl["gsensor"], 3)
            gyrs = split_list(tbl["gyro"], 3)
            spds = split_list(tbl["gps_speed"], 1) if "gps_speed" in avail else [None] * len(ids)
            vss = tbl["vehiclesize"].to_pylist() if "vehiclesize" in avail else [None] * len(ids)
            for k in range(len(ids)):
                acc, gyr = accs[k], gyrs[k]
                m = min(len(acc), len(gyr))
                yield shard.name, ids[k], int(labs[k]), acc[:m], gyr[:m], spds[k], vss[k]
