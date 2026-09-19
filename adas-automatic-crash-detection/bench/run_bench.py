#!/usr/bin/env python3
"""Build and run the C benchmark on both VZCrash shards, sweep thresholds,
and write results.md.

    .venv/bin/python3 bench/run_bench.py            # full run
    .venv/bin/python3 bench/run_bench.py --no-build # reuse build/res-*.bin

Requires data/bench/test-0000{0,1}.bin from export_events.py.
"""
import json
import struct
import subprocess
import sys
from pathlib import Path

import numpy as np
import pyarrow.parquet as pq

ROOT = Path(__file__).resolve().parent.parent
BENCH_BIN = ROOT / "build" / "bench"
DATA = ROOT / "data" / "bench"
PARQUET = ROOT / "data" / "vzcrash"
RESULTS_MD = ROOT / "results.md"
SHARDS = (0, 1)
LABELS = {0: "crash", 1: "near_miss", 2: "normal_driving"}
CFLAGS = ["-std=c99", "-O2", "-Wall", "-Wextra"]

MAGIC_OUT = 0x56435232


# ---------------------------------------------------------------------------
# build + run
# ---------------------------------------------------------------------------

def build() -> None:
    BENCH_BIN.parent.mkdir(exist_ok=True)
    cmd = ["gcc", *CFLAGS, "-o", str(BENCH_BIN),
           str(ROOT / "bench" / "bench.c"), str(ROOT / "src" / "crash_detect.c"), "-lm"]
    print("$", " ".join(cmd))
    subprocess.run(cmd, check=True)


def run_shard(idx: int) -> dict:
    src = DATA / f"test-0000{idx}.bin"
    out = BENCH_BIN.parent / f"res-0000{idx}.bin"
    print(f"$ bench {src.name} -> {out.name}")
    proc = subprocess.run([str(BENCH_BIN), str(src), str(out)],
                          check=True, capture_output=True, text=True)
    return json.loads(proc.stdout)


# ---------------------------------------------------------------------------
# results loader
# ---------------------------------------------------------------------------

class Results:
    """Per-event peak values from one or more shards, as flat numpy arrays."""

    def __init__(self):
        self.event_id, self.label, self.shard = [], [], []
        self.peak_g, self.peak_gyro, self.peak_delta = [], [], []
        self.peak_tilt_rel, self.peak_tilt_abs = [], []
        self.sec_peak_delta = []   # list of 1-D arrays, one per event
        self.alphas = None

    def load(self, idx: int) -> None:
        buf = (BENCH_BIN.parent / f"res-0000{idx}.bin").read_bytes()
        magic, n, na = struct.unpack_from("<III", buf, 0)
        assert magic == MAGIC_OUT
        alphas = np.frombuffer(buf, "<f4", na, 12)
        if self.alphas is None:
            self.alphas = alphas
        assert np.array_equal(self.alphas, alphas)
        off = 12 + 4 * na
        for _ in range(n):
            eid, lab, n_imu, n_speed = struct.unpack_from("<qiii", buf, off)
            off += 20
            v = np.frombuffer(buf, "<f4", 3 + 2 * na, off)
            off += 4 * (3 + 2 * na)
            sec = np.frombuffer(buf, "<f4", n_speed, off)
            off += 4 * n_speed
            self.event_id.append(eid)
            self.label.append(lab)
            self.shard.append(idx)
            self.peak_g.append(v[0])
            self.peak_gyro.append(v[1])
            self.peak_tilt_rel.append(v[2:2 + na])
            self.peak_tilt_abs.append(v[2 + na:2 + 2 * na])
            self.peak_delta.append(v[2 + 2 * na])
            self.sec_peak_delta.append(sec)
        assert off == len(buf)

    def finalize(self) -> None:
        for k in ("event_id", "label", "shard", "peak_g", "peak_gyro",
                  "peak_delta", "peak_tilt_rel", "peak_tilt_abs"):
            setattr(self, k, np.asarray(getattr(self, k)))


def load_speeds(res: Results) -> list:
    """gps_speed per event, aligned with res.event_id (for Test 4 gating)."""
    by_id = {}
    for idx in SHARDS:
        tbl = pq.read_table(PARQUET / f"test-0000{idx}-of-00002.parquet",
                            columns=["event_id", "gps_speed"])
        for eid, sp in zip(tbl["event_id"].to_pylist(), tbl["gps_speed"].to_pylist()):
            by_id[eid] = np.asarray(sp, dtype=np.float32)
    return [by_id[e] for e in res.event_id]


# ---------------------------------------------------------------------------
# metrics
# ---------------------------------------------------------------------------

def recall_fpr(values: np.ndarray, label: np.ndarray, thr: float) -> dict:
    """Event fires if value >= thr. Recall on crash, FPR on the rest."""
    fired = values >= thr
    out = {"thr": thr}
    for lab, name in LABELS.items():
        m = label == lab
        out[name] = fired[m].mean() if m.any() else float("nan")
    non = label != 0
    out["fpr"] = fired[non].mean()
    out["recall"] = out["crash"]
    return out


def sweep(values: np.ndarray, label: np.ndarray, thrs) -> list:
    return [recall_fpr(values, label, t) for t in thrs]


def md_table(rows: list, cols: list, fmt: dict) -> str:
    head = "| " + " | ".join(c[1] for c in cols) + " |"
    sep = "|" + "|".join("---" for _ in cols) + "|"
    body = []
    for r in rows:
        cells = []
        for key, _ in cols:
            v = r[key]
            cells.append(fmt.get(key, "{}").format(v) if not (isinstance(v, float) and np.isnan(v)) else "–")
        body.append("| " + " | ".join(cells) + " |")
    return "\n".join([head, sep, *body])


PCT = "{:.1%}"
SWEEP_COLS = [("thr", "threshold"), ("recall", "crash recall"),
              ("near_miss", "near_miss fires"), ("normal_driving", "normal fires"),
              ("fpr", "FPR (non-crash)")]
SWEEP_FMT = {"thr": "{:g}", "recall": PCT, "near_miss": PCT, "normal_driving": PCT, "fpr": PCT}


# ---------------------------------------------------------------------------
# report sections
# ---------------------------------------------------------------------------

def section_data(res: Results) -> str:
    lines = ["## Data", "",
             f"- Events: {len(res.label)} across shards {SHARDS} "
             f"(crash {int((res.label == 0).sum())}, near_miss {int((res.label == 1).sum())}, "
             f"normal_driving {int((res.label == 2).sum())})",
             "- Each event is one decision: a strategy *fires* on an event if its peak "
             "per-sample value crosses the threshold anywhere in the event.",
             "- Recall = fraction of crash events fired. FPR = fraction of "
             "near_miss + normal_driving events fired (both shards).",
             "- IMU is 100 Hz; GPS speed is 1 Hz. Mounting baseline for tilt is "
             "latched after the first 1 s (100 samples).", ""]
    return "\n".join(lines)


def section_timing(timing: dict) -> str:
    t = timing["ns_per_sample"]
    base = t["baseline"]
    ref = t["gforce_band"] - base
    rows = []
    for name in ("gforce_band", "tilt_filter", "delta_speed"):
        rows.append({"name": name, "raw": t[name], "net": t[name] - base,
                     "rel": (t[name] - base) / ref})
    lines = ["## Speed (host x86-64, gcc -O2, single core)", "",
             f"Measured over {timing['n_samples']:,} samples of shard 0, best of 3 passes. "
             f"`baseline` = {base:.2f} ns/sample just to load a sample from memory; "
             "`net` subtracts it. Absolute numbers will differ on the ESP32 "
             "(Xtensa LX6 @ 240 MHz, single-precision FPU) — use these for the "
             "*relative* ordering only. For scale: even at 1000× slower, the heaviest "
             "detector would use <0.2% of the 19,231 µs sample period at 52 Hz.",
             "",
             md_table(rows, [("name", "detector"), ("raw", "ns/sample"), ("net", "net ns/sample"),
                             ("rel", "cost vs gforce_band")],
                      {"raw": "{:.2f}", "net": "{:.2f}", "rel": "{:.1f}×"}),
             ""]
    return "\n".join(lines)


def percentile_rows(values: np.ndarray, label: np.ndarray, pcts=(50, 90, 99, 100)) -> str:
    rows = []
    for lab, name in LABELS.items():
        x = values[label == lab]
        r = {"label": name, "n": len(x)}
        for p in pcts:
            r[f"p{p}"] = float(np.percentile(x, p))
        rows.append(r)
    cols = [("label", "label"), ("n", "n")] + [(f"p{p}", f"p{p}" if p < 100 else "max") for p in pcts]
    return md_table(rows, cols, {f"p{p}": "{:.2f}" for p in pcts})


def section_test2(res: Results) -> str:
    thrs = [1.5, 2, 2.5, 3, 3.5, 4, 4.5, 5, 6, 7, 8, 20, 40]
    rows = sweep(res.peak_g, res.label, thrs)
    lines = ["## Test 2 — G-force banding (`gforce_band_update`)", "",
             "Peak ‖A‖g per event (gravity included, so 1.0 at rest). Distribution by label:", "",
             percentile_rows(res.peak_g, res.label), "",
             "Sweep of the *mild* threshold (event fires if peak ‖A‖g ≥ threshold). "
             "20 and 40 are the literature moderate/severe boundaries:", "",
             md_table(rows, SWEEP_COLS, SWEEP_FMT), "",
             "Observations:", "",
             "- No crash event in this dataset exceeds ~7.3 G; per-axis peaks top out at "
             "~6 G (the telematics units look like ±8 g accelerometers). The 20 G / 40 G "
             "*moderate* / *severe* bands from Razali et al. never fire — on this hardware "
             "class they cannot be used as detection thresholds, only as severity labels "
             "if a higher-range sensor is fitted.",
             "- The 5 G *mild* boundary is the only literature value that does anything, "
             "and it sits right in the middle of the crash distribution "
             f"(recall {rows[thrs.index(5)]['recall']:.1%}).",
             "- A handful of normal_driving events carry sensor glitches "
             "(single-sample peaks >20 G, gyro >10,000 deg/s) — a real deployment "
             "needs a plausibility clamp before any of these detectors.",
             ""]
    return "\n".join(lines)


# ---------------------------------------------------------------------------

def main(argv) -> None:
    if "--no-build" not in argv:
        build()
        timings = {i: run_shard(i) for i in SHARDS}
        (BENCH_BIN.parent / "timing.json").write_text(json.dumps(timings, indent=1))
    timings = {int(k): v for k, v in json.loads((BENCH_BIN.parent / "timing.json").read_text()).items()}

    res = Results()
    for i in SHARDS:
        res.load(i)
    res.finalize()
    print(f"loaded {len(res.label)} events")

    sections = ["# Benchmark results — Layer 2 strategies on VZCrash test split", "",
                "Generated by `bench/run_bench.py`; do not edit by hand. "
                "Design in `info.md`, test plan in `stats.md`, detector code in "
                "`src/crash_detect.c`.", "",
                section_data(res), section_timing(timings[0]),
                section_test2(res)]
    RESULTS_MD.write_text("\n".join(sections))
    print(f"wrote {RESULTS_MD}")


if __name__ == "__main__":
    main(sys.argv[1:])
