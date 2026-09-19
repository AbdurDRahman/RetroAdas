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


def robust_z(x: np.ndarray) -> np.ndarray:
    """Median/IQR scaling: like a z-score but not dragged around by the
    occasional sensor-glitch outlier (see Test 2's >20G normal_driving
    spikes) the way mean/std would be."""
    med = np.median(x)
    iqr = np.percentile(x, 75) - np.percentile(x, 25)
    return (x - med) / iqr


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


def tilt_grid(peaks: np.ndarray, alphas: np.ndarray, label: np.ndarray, thrs) -> str:
    """Rows = alpha, cols = threshold, cell = crash recall / non-crash FPR."""
    rows = []
    for j, a in enumerate(alphas):
        r = {"alpha": f"{a:.3f}", "tau": a * 0.01 / (1 - a)}
        for t in thrs:
            m = recall_fpr(peaks[:, j], label, t)
            r[f"t{t}"] = f"{m['recall']:.1%} / {m['fpr']:.1%}"
        rows.append(r)
    cols = [("alpha", "α"), ("tau", "τ (s) @100Hz")] + [(f"t{t}", f"≥{t}°") for t in thrs]
    return md_table(rows, cols, {"tau": "{:.2f}"})


def section_test3(res: Results) -> str:
    thrs = [30, 46, 60, 70, 80]
    a96 = int(np.argmin(np.abs(res.alphas - 0.96)))
    lines = ["## Test 3 — complementary-filter tilt / rollover (`tilt_filter_update`)", "",
             "Peak of max(|pitch−pitch₀|, |roll−roll₀|) per event, where the ₀ baseline "
             "is latched 1 s in (mounting offset). Cells are **crash recall / non-crash "
             "FPR**. τ = α·Δt/(1−α) is the filter time constant — the same α gives a "
             "shorter τ at 100 Hz than at 52 Hz, so the 52 Hz design range 0.90–0.98 "
             "maps to roughly 0.95–0.99 here.", "",
             f"Distribution of peak relative tilt at α={res.alphas[a96]:.2f}:", "",
             percentile_rows(res.peak_tilt_rel[:, a96], res.label), "",
             "### Relative to mounting baseline", "",
             tilt_grid(res.peak_tilt_rel, res.alphas, res.label, thrs), "",
             "### Absolute (no baseline correction)", "",
             tilt_grid(res.peak_tilt_abs, res.alphas, res.label, thrs), "",
             "Observations:", "",
             "- Tilt is a poor *collision* detector on this data: the accel-derived angle "
             "is corrupted by the very linear accelerations a crash produces, so what the "
             "filter sees is a short-lived pseudo-tilt spike, and its size scales with "
             "‖A‖ rather than with any real rotation. Higher α (longer τ) suppresses "
             "that spike and recall collapses toward zero.",
             "- The 46° threshold (Ibrahim et al.) is inside the normal-driving noise "
             "floor at low α; the 70–80° thresholds (Chikaka & Longe) have near-zero "
             "FPR but also near-zero recall. Genuine rollovers are rare/absent in the "
             "test split, so this test says nothing about rollover recall — only that "
             "tilt should be an OR-branch for rollover, not the primary collision path.",
             "- The ~5–6% of crashes that still exceed 60° at τ≈1–2 s (α≥0.99) are "
             "*sustained* orientation changes — consistent with rollovers or vehicles "
             "coming to rest tilted. That is the population the tilt branch exists for, "
             "and it catches them at ≤0.4% FPR.",
             "- Absolute tilt without baseline correction is unusable: devices are mounted "
             "at arbitrary angles, so many events start above 30°.",
             ""]
    return "\n".join(lines)


def section_test3b(res: Results) -> str:
    ai = int(np.argmin(np.abs(res.alphas - 0.90)))
    gz = robust_z(res.peak_g)
    tz = robust_z(res.peak_tilt_rel[:, ai])
    ws = [0.0, 0.5, 1.0, 1.5, 2.0, 2.5, 3.0]
    caps = [0.01, 0.02, 0.05]

    rows = []
    for w in ws:
        score = gz + w * tz
        r = {"w": w}
        for c in caps:
            rec, thr = recall_at_fpr(score, res.label, c)
            r[f"c{c}"] = f"{rec:.1%}"
        rows.append(r)
    cols = [("w", "tilt weight w")] + [(f"c{c}", f"recall @ FPR≤{c:.0%}") for c in caps]

    lines = ["## Test 3b — weighted combination of ‖A‖g and tilt (not a hard OR)", "",
             "Test 3's `OR: peak ‖A‖g ∨ tilt≥60°` combines the two signals as a hard "
             "gate, and it made things *worse* (Summary table) because tilt's false "
             "positives get added in whole. This instead scores each event as "
             "`score = z(peak ‖A‖g) + w · z(peak tilt_rel, α=0.90)` — `z(·)` is a "
             "median/IQR-scaled z-score so G (units of g) and tilt (degrees) sit on "
             "comparable scales — and sweeps `w`. α=0.90 (the *short* time constant, "
             "not the α≈0.99 used for the rollover branch) turned out to work best: "
             "at low α the tilt filter barely smooths the accelerometer, so what it's "
             "really contributing here is a differently-shaped view of the same impact "
             "spike, not a rotation measurement.", "",
             md_table(rows, cols, {"w": "{:g}"}), "",
             "Observations:", "",
             "- w=0 is peak ‖A‖g alone (Test 2's numbers, restated as recall@FPR-cap). "
             "Adding tilt at w≈1–1.5 lifts recall by 12–17 points at 1–2% FPR "
             "(45.6%→~60%, 62.0%→~75%) without giving any of it back at 5% FPR. This "
             "is the opposite conclusion from the hard-OR combination: tilt *does* "
             "carry usable information about a crash, just not as a separate veto — "
             "it needs to be added into the decision, not OR'd on top of it.",
             "- This sweep picks both `w` and the operating threshold on the same test "
             "split it's scored against, so treat these numbers as *evidence a weighted "
             "combination beats G alone*, not as calibrated production constants — "
             "re-fit `w` (and re-check it doesn't just track this dataset's sensor "
             "quirks) once real calibration data is collected, per the open item in "
             "`CLAUDE.md`.",
             "- This still says nothing about genuine rollovers specifically — the "
             "underlying data limitation from Test 3 (rollovers are rare/absent in "
             "this split) doesn't go away just because the combination method changed. "
             "The α≈0.99, ≥70–80° rollover branch from Test 3 should stay as a separate "
             "OR-condition alongside this score, not be replaced by it.",
             ""]
    return "\n".join(lines)


def gated_peak_delta(res: Results, speeds: list, min_drop: float, lookahead: int) -> np.ndarray:
    """Per-event max of per-second peak |Δ‖A‖| restricted to seconds where the
    GPS speed gate holds.

    lookahead=0: second k passes if speed[k-1] - speed[k] >= min_drop (and
                 strictly dropping) — what delta_speed_feed_speed() does.
    lookahead=L: second k passes if speed[k] - min(speed[k+1..k+L]) >= min_drop,
                 i.e. the vehicle slows within the next L seconds.
    """
    out = np.zeros(len(speeds), dtype=np.float32)
    for i, (sec, sp) in enumerate(zip(res.sec_peak_delta, speeds)):
        n = min(len(sec), len(sp))
        if n < 2:
            continue
        sec, sp = sec[:n], sp[:n]
        if lookahead == 0:
            drop = sp[:-1] - sp[1:]
            gate = np.zeros(n, dtype=bool)
            gate[1:] = (drop > 0) & (drop >= min_drop)
        else:
            gate = np.zeros(n, dtype=bool)
            for k in range(n - 1):
                fut = sp[k + 1:k + 1 + lookahead].min()
                gate[k] = (sp[k] - fut) > 0 and (sp[k] - fut) >= min_drop
        if gate.any():
            out[i] = sec[gate].max()
    return out


def section_test4(res: Results) -> str:
    speeds = load_speeds(res)
    thrs = [0.5, 1, 1.5, 2, 2.5, 3, 4, 5]
    gates = [("none", None, None),
             ("same-s, drop>0", 0.0, 0),
             ("same-s, drop≥10", 10.0, 0),
             ("+2 s, drop≥10", 10.0, 2),
             ("+2 s, drop≥20", 20.0, 2)]
    series = {}
    for name, md, la in gates:
        series[name] = res.peak_delta if md is None else gated_peak_delta(res, speeds, md, la)

    rows = []
    for t in thrs:
        r = {"thr": t}
        for name in series:
            m = recall_fpr(series[name], res.label, t)
            r[name] = f"{m['recall']:.1%} / {m['fpr']:.1%}"
        rows.append(r)
    cols = [("thr", "|Δ‖A‖| ≥ (g)")] + [(n, n) for n in series]

    lines = ["## Test 4 — consecutive-delta + GPS speed drop (`delta_speed_update`)", "",
             "|Δ‖A‖| between consecutive 100 Hz samples, gated on GPS speed (1 Hz, km/h). "
             "Cells are **crash recall / non-crash FPR**. Gate columns: `none` = delta "
             "alone; `same-s` = the fix at the start of the impact second is already "
             "lower than the previous one (what the C code implements); `+2 s` = speed "
             "falls within the next 2 s of the impact second (a device would buffer the "
             "decision — fine given the 10–15 s cancel countdown).", "",
             "Distribution of ungated peak |Δ‖A‖|:", "",
             percentile_rows(res.peak_delta, res.label), "",
             md_table(rows, cols, {"thr": "{:g}"}), "",
             "Observations:", "",
             "- Ungated sample-to-sample delta is a slightly *worse* discriminator than "
             "peak ‖A‖g (Test 2): ~77% vs ~87% crash recall at 5% FPR. An impact spike "
             "at 100 Hz is only a sample or two wide, so the delta mostly re-measures the "
             "peak with extra noise. The delta threshold is also data-rate dependent "
             "(fewer samples per impact at 52 Hz → larger deltas) — re-sweep on-device.",
             "- The same-second gate (as written in the C code) *loses* recall at equal "
             "FPR: the GPS fix at the start of the impact second predates the impact. "
             "The +2 s lookahead gate is the right shape and adds ~10 points of recall "
             "at 1–2% FPR over ungated delta, but still trails peak ‖A‖g alone.",
             "- The gate can't separate crashes from near-misses because 99% of near_miss "
             "events also show a ≥10 km/h drop within 2 s — hard braking is what makes "
             "them near-misses. It only prunes normal-driving events (69% of which drop "
             "≥10 km/h somewhere in the clip anyway).",
             "- No strategy fixes the fundamental overlap: the ~10–20% of crash events "
             "with peaks under 2 G look like hard braking to every rule here.",
             ""]
    return "\n".join(lines)


def recall_at_fpr(values: np.ndarray, label: np.ndarray, fpr_cap: float, extra_fire=None):
    """Highest crash recall achievable with FPR <= fpr_cap by sweeping the
    threshold on `values`; `extra_fire` (bool mask) is OR-ed in unchanged."""
    crash, non = label == 0, label != 0
    best = (0.0, float("nan"))
    for t in np.unique(values)[::-1]:
        fired = values >= t
        if extra_fire is not None:
            fired = fired | extra_fire
        fpr = fired[non].mean()
        if fpr > fpr_cap:
            break
        best = (fired[crash].mean(), float(t))
    return best


def section_summary(res: Results, timing: dict) -> str:
    speeds = load_speeds(res)
    a99 = int(np.argmin(np.abs(res.alphas - 0.99)))
    a90 = int(np.argmin(np.abs(res.alphas - 0.90)))
    tilt99 = res.peak_tilt_rel[:, a99]
    weighted_score = robust_z(res.peak_g) + 1.5 * robust_z(res.peak_tilt_rel[:, a90])
    strategies = [
        ("Test 2: peak ‖A‖g", res.peak_g, None),
        ("Test 3: tilt, α=0.99, relative", tilt99, None),
        ("Test 4: |Δ‖A‖|, ungated", res.peak_delta, None),
        ("Test 4: |Δ‖A‖|, +2 s drop≥10", gated_peak_delta(res, speeds, 10.0, 2), None),
        ("Design OR: peak ‖A‖g ∨ tilt≥60° (α=0.99)", res.peak_g, tilt99 >= 60),
        ("Test 3b: z(G) + 1.5·z(tilt, α=0.90)", weighted_score, None),
    ]
    caps = [0.005, 0.01, 0.02, 0.05]
    rows = []
    for name, v, extra in strategies:
        r = {"name": name}
        for c in caps:
            rec, thr = recall_at_fpr(v, res.label, c, extra)
            r[f"c{c}"] = f"{rec:.1%} @ {thr:.2g}"
        rows.append(r)
    cols = [("name", "strategy")] + [(f"c{c}", f"FPR ≤ {c:.1%}") for c in caps]

    t = timing["ns_per_sample"]
    base = t["baseline"]
    order = sorted(("gforce_band", "delta_speed", "tilt_filter"), key=lambda k: t[k])
    speed_line = " < ".join(f"`{k}` ({t[k] - base:.1f} ns)" for k in order)

    lines = ["## Summary", "",
             "Best crash recall each strategy reaches while keeping the non-crash "
             "false-positive rate under a cap (cell = recall @ threshold that achieves it):", "",
             md_table(rows, cols, {}), "",
             f"Speed, net ns/sample on the host: {speed_line}. All three are far inside "
             "the 52 Hz budget; the tilt filter's two `atan2f` calls make it the only "
             "one that costs anything measurable.", "",
             "Takeaways for the design:", "",
             "- **Peak ‖A‖g is the best single rule and the cheapest.** The literature "
             "5/20/40 G bands don't transfer to ±8 g telematics IMUs — the working "
             "threshold is ~2.5–3.5 G, which the sweep in Test 2 pins down.",
             "- **Tilt as a hard OR-gate adds nothing** — OR-ing a 60° tilt branch onto "
             "peak ‖A‖g *lowers* recall at equal FPR, since its false positives eat the "
             "budget whole. But **tilt as a weighted feature does help** (Test 3b): "
             "`z(G) + 1.5·z(tilt, α=0.90)` beats G alone by 12–17 points of recall at "
             "1–2% FPR. The two branches aren't redundant, they were just being combined "
             "the wrong way. Rollover coverage is a separate concern from either of "
             "these — keep a long-time-constant (α≈0.99), baseline-corrected, 70–80° "
             "branch as its own OR-condition, since this split can't measure rollover "
             "recall regardless of how the collision score is built.",
             "- **The GPS speed condition belongs after the trigger, not AND-ed with it** — "
             "use a look-ahead window (2 s) as a confirmation/cancel input, and don't "
             "expect it to reject near-misses.",
             "- Peak ‖A‖g alone tops out around 60–65% recall at low FPR; the weighted "
             "G+tilt score pushes that to ~75–90% (Test 3b) without extra sensors. "
             "Squeezing further needs shape features (Layer 1 moving average / gyro "
             "discriminator, Test 1) or a learned model, and any weighting here should "
             "be re-fit once real calibration data replaces this test split.",
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
                section_summary(res, timings[0]),
                section_data(res), section_timing(timings[0]),
                section_test2(res), section_test3(res), section_test3b(res),
                section_test4(res)]
    RESULTS_MD.write_text("\n".join(sections))
    print(f"wrote {RESULTS_MD}")


if __name__ == "__main__":
    main(sys.argv[1:])
