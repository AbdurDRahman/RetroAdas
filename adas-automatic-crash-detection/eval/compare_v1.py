#!/usr/bin/env python3
"""Run the v1 (first engineer's) detection rules and the v2 engine on the SAME
data and write comparison.md.

    .venv\\Scripts\\python eval\\compare_v1.py

v1 rules, reproduced exactly from src/crash_detect.c and bench/run_bench.py:

  v1-A  peak ||A|| >= 5 G         the only preset in crash_detect.h (GFORCE_CFG_LITERATURE "mild")
  v1-B  peak ||A|| >= 3 G         the threshold handover.md recommends ("e.g. 3.0 G")
  v1-C  peak ||A|| >= 3 G  OR  tilt(alpha=0.99, relative) >= 60 deg   (the "design OR", results.md)
  v1-D  z(peak G) + 1.5 z(peak tilt alpha=0.90) >= thr   (Test 3b, v1's best; z = median/IQR
        of the test split, thr = the value giving FPR <= 2% on the test split — i.e. tuned on
        the very data it is scored on, which flatters v1)
  v1-E  moving average of ||A|| over 0.58 s (58 samples) >= thr  — Layer 1 (Pranoto et al.),
        designed in info.md but never implemented; thr matched to v2's FPR
  v1-F  peak |delta ||A||| between consecutive samples >= thr (Test 4, ungated); thr matched

and v1-B / v1-D re-run at the threshold that gives the SAME harsh-event FPR as
v2, so recall can be compared at equal false-alarm cost.

Continuous driving (PVS, RoadSens): a per-sample rule fires when it crosses its
threshold; repeated crossings within 60 s count once (the same lockout v2 has).
v1-D is scored on 16 s windows sliding by 1 s — the same window length its
thresholds were tuned on (VZCrash events are 16 s).
"""
from __future__ import annotations

import json
import sys
import time
from pathlib import Path

import numpy as np
from scipy.signal import lfilter

sys.path.insert(0, str(Path(__file__).resolve().parent))
import datasets as ds  # noqa: E402
from engine import EV_CRASH, EV_ROLLOVER, Engine  # noqa: E402

ROOT = ds.ROOT
OUT_MD = ROOT / "comparison.md"
OUT_JSON = ROOT / "build" / "compare_v1.json"
RAD2DEG = 57.29577951308232
DT = 0.01
CALIB = 100          # v1 bench: mounting baseline latched after 1 s
MA_K = 58            # Layer 1 window: k=30 @ 52 Hz = 0.58 s = 58 samples @ 100 Hz
LOCKOUT_S = 60.0
HDE_EVENTS, HDE_VEHICLES = 735_000, 123_000
HDE_RATE_DRIVE = (HDE_EVENTS / HDE_VEHICLES) / (2 * 8.0)   # harsh events per driving hour


# ---------------------------------------------------------------------------
# v1 signal primitives (numpy versions of src/crash_detect.c)
# ---------------------------------------------------------------------------

def tilt_rel(acc: np.ndarray, gyr: np.ndarray, alpha: float) -> np.ndarray:
    """Complementary-filter tilt exactly as tilt_filter_update(): pitch from
    atan2(ay, sqrt(ax^2+az^2)) fused with gx, roll from atan2(-ax, sqrt(ay^2+az^2))
    fused with gy, seeded with the accelerometer angle, baseline latched at
    sample CALIB-1, returns max(|dpitch|, |droll|) (0 before calibration)."""
    ax, ay, az = acc[:, 0].astype(np.float64), acc[:, 1].astype(np.float64), acc[:, 2].astype(np.float64)
    p_acc = np.arctan2(ay, np.sqrt(ax * ax + az * az)) * RAD2DEG
    r_acc = np.arctan2(-ax, np.sqrt(ay * ay + az * az)) * RAD2DEG
    out = []
    for ang, rate in ((p_acc, gyr[:, 0]), (r_acc, gyr[:, 1])):
        u = alpha * rate.astype(np.float64) * DT + (1 - alpha) * ang
        u[0] = ang[0]
        out.append(lfilter([1.0], [1.0, -alpha], u))
    p, r = out
    n = len(p)
    rel = np.zeros(n)
    if n >= CALIB:
        p0, r0 = p[CALIB - 1], r[CALIB - 1]
        rel[CALIB - 1:] = np.maximum(np.abs(p[CALIB - 1:] - p0), np.abs(r[CALIB - 1:] - r0))
        rel[CALIB - 1] = 0.0
    return rel


def amag(acc: np.ndarray) -> np.ndarray:
    return np.sqrt((acc.astype(np.float64) ** 2).sum(axis=1))


def moving_avg(x: np.ndarray, k: int) -> np.ndarray:
    if len(x) < k:
        return np.array([x.mean()]) if len(x) else np.zeros(1)
    c = np.cumsum(np.concatenate([[0.0], x]))
    return (c[k:] - c[:-k]) / k


def count_triggers(mask: np.ndarray, hz: float = 100.0) -> int:
    """Rising edges of mask, with crossings inside a 60 s lockout counted once."""
    idx = np.nonzero(mask)[0]
    if not len(idx):
        return 0
    n, last = 0, -1e18
    for i in idx:
        if i - last >= LOCKOUT_S * hz:
            n += 1
            last = i
    return n


def robust_z_consts(x: np.ndarray):
    med = float(np.median(x))
    iqr = float(np.percentile(x, 75) - np.percentile(x, 25))
    return med, iqr


# ---------------------------------------------------------------------------
# VZCrash test split: per-event peaks for every v1 rule + the v2 engine
# ---------------------------------------------------------------------------

def tier_of(f) -> str:
    if f is None:
        return "below"
    peak_a, sus50 = float(f[0]), float(f[1])
    if sus50 >= 2.5 or peak_a >= 4.0:
        return "severe"
    if peak_a >= 3.0:
        return "moderate"
    if peak_a >= 1.8:
        return "minor"
    return "below"


def vzcrash_table():
    rows = []
    e = Engine()
    t0 = time.time()
    for shard, eid, lab, acc, gyr, spd, vs in ds.iter_vzcrash("test"):
        m = amag(acc)
        e.reset()
        ev, feats = e.run(acc, gyr)
        f = feats[int(np.argmax(feats[:, 0]))] if len(feats) else None
        rows.append({
            "label": lab,
            "v2": bool(((ev == EV_CRASH) | (ev == EV_ROLLOVER)).any()),
            "tier": tier_of(f),
            "peak_g": float(m.max()),
            "tilt90": float(tilt_rel(acc, gyr, 0.90).max()),
            "tilt99": float(tilt_rel(acc, gyr, 0.99).max()),
            "ma": float(moving_avg(m, MA_K).max()),
            "delta": float(np.abs(np.diff(m)).max()) if len(m) > 1 else 0.0,
        })
    print(f"VZCrash test: {len(rows)} events in {time.time() - t0:.0f} s", file=sys.stderr)
    return rows


def thr_for_fpr(values: np.ndarray, neg: np.ndarray, target: float) -> float:
    """Lowest threshold whose FPR on negatives is <= target."""
    v = np.sort(values[neg])[::-1]
    k = int(np.floor(target * len(v)))
    # fire if value >= thr; the k-th largest negative value is the boundary
    if k >= len(v):
        return float(v[-1])
    return float(np.nextafter(v[k], np.inf))


# ---------------------------------------------------------------------------
# continuous driving
# ---------------------------------------------------------------------------

def continuous_counts(drives, rules: dict) -> dict:
    """rules: name -> callable(acc, gyr, m) -> bool mask per sample. Returns
    name -> (false alarms, hours)."""
    out = {k: 0 for k in rules}
    hours = 0.0
    v2 = 0
    for d in drives:
        hours += d.hours
        m = amag(d.acc)
        cache = {}
        for name, fn in rules.items():
            out[name] += count_triggers(fn(d.acc, d.gyr, m, cache))
        e = Engine()
        ev, _ = e.run(d.acc, d.gyr)
        v2 += int(((ev == EV_CRASH) | (ev == EV_ROLLOVER)).sum())
    out["v2"] = v2
    return out, hours


def window_score_mask(acc, gyr, m, cache, zc, w, thr):
    """v1-D on 16 s windows sliding by 1 s; returns a mask with one True at the
    end of every firing window."""
    if "t90" not in cache:
        cache["t90"] = tilt_rel(acc, gyr, 0.90)
    t90 = cache["t90"]
    (gm, gi), (tm, ti) = zc
    n = len(m)
    mask = np.zeros(n, bool)
    win, step = 1600, 100
    if n < win:
        s = (m.max() - gm) / gi + w * (t90.max() - tm) / ti
        mask[-1] = s >= thr
        return mask
    for a in range(0, n - win + 1, step):
        s = (m[a:a + win].max() - gm) / gi + w * (t90[a:a + win].max() - tm) / ti
        if s >= thr:
            mask[a + win - 1] = True
    return mask


# ---------------------------------------------------------------------------

def pct(x):
    return f"{100 * x:.1f}%"


def main():
    rows = vzcrash_table()
    lab = np.array([r["label"] for r in rows])
    neg = lab != 0
    crash = lab == 0
    tier = np.array([r["tier"] for r in rows])
    col = {k: np.array([r[k] for r in rows]) for k in ("peak_g", "tilt90", "tilt99", "ma", "delta")}
    v2 = np.array([r["v2"] for r in rows])
    v2_fpr = float(v2[neg].mean())

    # v1-D constants exactly as run_bench.py: median/IQR over ALL test events
    zc = (robust_z_consts(col["peak_g"]), robust_z_consts(col["tilt90"]))
    (gm, gi), (tm, ti) = zc
    s3b = (col["peak_g"] - gm) / gi + 1.5 * (col["tilt90"] - tm) / ti
    thr_3b_2pct = thr_for_fpr(s3b, neg, 0.02)
    thr_3b_match = thr_for_fpr(s3b, neg, v2_fpr)
    thr_g_match = thr_for_fpr(col["peak_g"], neg, v2_fpr)
    thr_ma_match = thr_for_fpr(col["ma"], neg, v2_fpr)
    thr_d_match = thr_for_fpr(col["delta"], neg, v2_fpr)

    rules = [
        ("v1-A", "peak ‖A‖ ≥ 5 G (the preset in crash_detect.h)", col["peak_g"] >= 5.0, ("g", 5.0)),
        ("v1-B", "peak ‖A‖ ≥ 3 G (handover.md's suggestion)", col["peak_g"] >= 3.0, ("g", 3.0)),
        ("v1-C", "peak ‖A‖ ≥ 3 G OR tilt ≥ 60° (design OR)",
         (col["peak_g"] >= 3.0) | (col["tilt99"] >= 60.0), ("or", 3.0)),
        ("v1-D", f"z(G)+1.5·z(tilt) ≥ {thr_3b_2pct:.2f} (Test 3b, tuned on test for FPR ≤ 2 %)",
         s3b >= thr_3b_2pct, ("3b", thr_3b_2pct)),
        ("v1-B*", f"peak ‖A‖ ≥ {thr_g_match:.2f} G (v1-B at v2's FPR)", col["peak_g"] >= thr_g_match, ("g", thr_g_match)),
        ("v1-D*", f"Test 3b ≥ {thr_3b_match:.2f} (at v2's FPR)", s3b >= thr_3b_match, ("3b", thr_3b_match)),
        ("v1-E*", f"moving avg ‖A‖ (0.58 s) ≥ {thr_ma_match:.2f} G (Layer 1, never built; at v2's FPR)",
         col["ma"] >= thr_ma_match, ("ma", thr_ma_match)),
        ("v1-F*", f"Δ‖A‖ ≥ {thr_d_match:.2f} G (Test 4 ungated; at v2's FPR)",
         col["delta"] >= thr_d_match, ("d", thr_d_match)),
        ("v2", "this engine (src/engine.c)", v2, ("v2", None)),
    ]

    # continuous-driving rule callables
    def mk(kind, thr):
        if kind == "g":
            return lambda acc, gyr, m, c: m >= thr
        if kind == "or":
            def f(acc, gyr, m, c):
                if "t99" not in c:
                    c["t99"] = tilt_rel(acc, gyr, 0.99)
                return (m >= thr) | (c["t99"] >= 60.0)
            return f
        if kind == "3b":
            return lambda acc, gyr, m, c: window_score_mask(acc, gyr, m, c, zc, 1.5, thr)
        if kind == "ma":
            def f(acc, gyr, m, c):
                ma = moving_avg(m, MA_K)
                out = np.zeros(len(m), bool)
                out[MA_K - 1:MA_K - 1 + len(ma)] = ma >= thr
                return out
            return f
        if kind == "d":
            def f(acc, gyr, m, c):
                out = np.zeros(len(m), bool)
                out[1:] = np.abs(np.diff(m)) >= thr
                return out
            return f
        raise ValueError(kind)

    cont_rules = {name: mk(*spec) for name, _, _, spec in rules if spec[0] != "v2"}
    pvs = list(ds.iter_pvs())
    rs = list(ds.iter_roadsens())
    pvs_fa, pvs_h = continuous_counts(pvs, cont_rules)
    rs_fa, rs_h = continuous_counts(rs, cont_rules)

    table = []
    for name, desc, fired, _ in rules:
        r = {
            "name": name, "desc": desc,
            "all": float(fired[crash].mean()),
            "severe": float(fired[crash & (tier == "severe")].mean()),
            "moderate": float(fired[crash & (tier == "moderate")].mean()),
            "minor": float(fired[crash & (tier == "minor")].mean()),
            "fpr": float(fired[neg].mean()),
            "missed_severe": int((~fired & crash & (tier == "severe")).sum()),
            "pvs_fa": pvs_fa[name], "pvs_h": pvs_h,
            "rs_fa": rs_fa[name], "rs_h": rs_h,
        }
        r["fph"] = r["fpr"] * HDE_RATE_DRIVE
        table.append(r)

    n_sev = int((crash & (tier == "severe")).sum())
    lines = [
        "# v1 vs v2 — the same data through both", "",
        "Generated by `eval/compare_v1.py`; do not edit by hand. v1 = the rules in "
        "`src/crash_detect.c` / `bench/run_bench.py` written by the first engineer (plus the "
        "moving-average Layer 1 that `info.md` designed but never implemented). v2 = "
        "`src/engine.c`. Every row runs on the identical VZCrash test split "
        f"({len(rows):,} events, {int(crash.sum()):,} crashes of which {n_sev:,} severe), "
        f"the identical {pvs_h:.2f} h of PVS driving and {rs_h:.2f} h of RoadSens phone data.", "",
        "Rows marked `*` re-run a v1 rule at the threshold that gives **the same harsh-event "
        f"false-positive rate as v2 ({pct(v2_fpr)})**, so recall is compared at equal "
        "false-alarm cost. v1-D's threshold and z-score constants come from the test split itself "
        "(that is how v1 was evaluated), which favours v1.", "",
        "| rule | what it is | recall all | severe | moderate | minor | severe missed | FPR harsh | false alarms / driving h (fleet conversion) | PVS false alarms (real driving) | RoadSens false alarms |",
        "|---|---|---|---|---|---|---|---|---|---|---|",
    ]
    for r in table:
        lines.append(
            f"| **{r['name']}** | {r['desc']} | {pct(r['all'])} | {pct(r['severe'])} | "
            f"{pct(r['moderate'])} | {pct(r['minor'])} | {r['missed_severe']} | {pct(r['fpr'])} | "
            f"{r['fph']:.4f} | {r['pvs_fa']} in {r['pvs_h']:.1f} h ({r['pvs_fa'] / r['pvs_h']:.2f}/h) | "
            f"{r['rs_fa']} in {r['rs_h']:.1f} h |")
    lines += ["", "How to read it:", "",
              "- *severe missed* is the number that matters most for an emergency-call feature: "
              "each one is a violent crash where nobody would be called.",
              "- *PVS false alarms* is the honest real-road number: 3 h of ordinary driving in three "
              "cars over asphalt, cobblestone, dirt and speed bumps. Every alarm there is false.",
              "- A rule that looks fine on VZCrash (which only contains pre-filtered harsh events) "
              "can still be unusable on real roads if it fires on cobblestone or potholes; that is "
              "what the last two columns show.", ""]
    OUT_MD.write_text("\n".join(lines), encoding="utf-8")
    OUT_JSON.parent.mkdir(exist_ok=True)
    OUT_JSON.write_text(json.dumps({"rows": table, "v2_fpr": v2_fpr,
                                    "z_consts": zc}, indent=1), encoding="utf-8")
    print(f"wrote {OUT_MD}")


if __name__ == "__main__":
    main()
