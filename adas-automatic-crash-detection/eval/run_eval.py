#!/usr/bin/env python3
"""Run the C engine over every available dataset and write evaluation.md.

    .venv\\Scripts\\python eval\\run_eval.py              # everything present
    .venv\\Scripts\\python eval\\run_eval.py --skip vzcrash
    .venv\\Scripts\\python eval\\run_eval.py --quick      # VZCrash: first 3000 events/shard

The engine is the compiled src/engine.c (through eval/engine.py), so the
numbers are for the exact code the ESP32 runs. Sections:

  1. PVS         false alarms per hour of continuous driving (crash/rollover),
                 pothole/bump reports vs road labels
  2. RoadSens-4M pothole stretch recall, false pothole reports on bump stretches
                 and normal-road sessions
  3. VZCrash     crash recall by severity tier, FPR on harsh-event negatives,
                 converted to false alarms per driving hour
  4. Timing      ns/sample on this host
"""
from __future__ import annotations

import argparse
import json
import sys
import time
from collections import Counter, defaultdict
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parent))
import datasets as ds  # noqa: E402
from engine import (EV_CRASH, EV_NAMES, EV_POTHOLE, EV_ROLLOVER, EV_SPEED_BUMP,  # noqa: E402
                    FEAT_NAMES, Engine)

ROOT = ds.ROOT
OUT_MD = ROOT / "evaluation.md"
OUT_JSON = ROOT / "build" / "evaluation.json"

# VZCrash paper (arXiv:2606.06074, Sec. V): 735,000 harsh-driving events from
# ~123,000 vehicles over 48 h of fleet operation. We do not know driving hours
# per vehicle; two bounds are reported.
HDE_EVENTS, HDE_VEHICLES, HDE_WALL_H = 735_000, 123_000, 48.0


def md_table(rows, cols, fmt=None):
    fmt = fmt or {}
    head = "| " + " | ".join(c[1] for c in cols) + " |"
    sep = "|" + "|".join("---" for _ in cols) + "|"
    body = []
    for r in rows:
        cells = []
        for k, _ in cols:
            v = r.get(k, "")
            if isinstance(v, float):
                v = fmt.get(k, "{:.3g}").format(v)
            cells.append(str(v).replace("|", "\\|"))
        body.append("| " + " | ".join(cells) + " |")
    return "\n".join([head, sep, *body])


def pct(x):
    return f"{100 * x:.1f}%"


# ---------------------------------------------------------------------------
# 1. PVS
# ---------------------------------------------------------------------------

def section_pvs(res: dict) -> str:
    ids = ds.pvs_available()
    if not ids:
        return "## 1. PVS continuous driving\n\n_data/pvs not present — skipped._\n"
    rows, tot_h, tot = [], 0.0, Counter()
    pot_by_label = Counter()
    for d in ds.iter_pvs():
        e = Engine()
        ev, _ = e.run(d.acc, d.gyr)
        h = d.hours
        tot_h += h
        c = Counter(ev[ev > 0].tolist())
        tot.update(c)
        lab = d.labels
        surface = np.where(lab["asphalt_road"] == 1, "asphalt",
                           np.where(lab["cobblestone_road"] == 1, "cobblestone", "dirt"))
        bump = (lab["speed_bump_asphalt"] == 1) | (lab["speed_bump_cobblestone"] == 1)
        bad = (lab["bad_road_left"] == 1) | (lab["bad_road_right"] == 1)
        for i in np.nonzero(ev == EV_POTHOLE)[0]:
            # the decision lands ~600 ms after the trigger; look back 1 s for the label
            j = slice(max(0, i - 100), i + 1)
            key = "speed_bump" if bump.iloc[j].any() else (
                f"{surface[i]}/{'bad' if bad.iloc[j].any() else 'ok'}")
            pot_by_label[key] += 1
        # sensitivity of the road branch: labelled bump stretches that produced any road event
        road_idx = np.nonzero((ev == EV_POTHOLE) | (ev == EV_SPEED_BUMP))[0]
        bump_int = ds.intervals(bump.to_numpy())
        bump_hit = sum(1 for a, b in bump_int if ((road_idx >= a) & (road_idx < b + 100)).any())
        tot["bump_int"] += len(bump_int)
        tot["bump_hit"] += bump_hit
        rows.append({"drive": d.name, "hours": h,
                     "surface": f"{np.mean(surface == 'asphalt'):.0%} asphalt, "
                                f"{np.mean(surface == 'cobblestone'):.0%} cobble, "
                                f"{np.mean(surface == 'dirt'):.0%} dirt",
                     "max_g": float(np.linalg.norm(d.acc, axis=1).max()),
                     "crash": c.get(EV_CRASH, 0), "roll": c.get(EV_ROLLOVER, 0),
                     "pothole": c.get(EV_POTHOLE, 0), "bump": c.get(EV_SPEED_BUMP, 0),
                     "bumps_labelled": f"{bump_hit}/{len(bump_int)}"})
    fa = tot.get(EV_CRASH, 0) + tot.get(EV_ROLLOVER, 0)
    bump_int_n, bump_hit_n = tot.pop("bump_int", 0), tot.pop("bump_hit", 0)
    res["pvs"] = {"hours": tot_h, "false_alarms": fa, "events": {EV_NAMES[k]: v for k, v in tot.items()},
                  "pothole_by_label": dict(pot_by_label),
                  "bump_stretches": bump_int_n, "bump_stretches_hit": bump_hit_n}
    lines = ["## 1. PVS — false alarms per hour of continuous driving", "",
             f"{len(rows)} drives, **{tot_h:.2f} h** of real driving (dashboard MPU-9250, ±8 g, 100 Hz, "
             "three cars, asphalt + cobblestone + dirt, speed bumps and potholes present). "
             "Every CRASH or ROLLOVER event here is a false alarm by definition.", "",
             md_table(rows, [("drive", "drive"), ("hours", "hours"), ("surface", "surface mix"),
                             ("max_g", "max ‖a‖ (g)"), ("crash", "CRASH"), ("roll", "ROLLOVER"),
                             ("pothole", "POTHOLE"), ("bump", "SPEED_BUMP (suppressed)"),
                             ("bumps_labelled", "labelled bump stretches seen by road branch")],
                      {"hours": "{:.2f}", "max_g": "{:.2f}"}), "",
             f"**Crash/rollover false alarms: {fa} in {tot_h:.2f} h = "
             f"{fa / tot_h if tot_h else float('nan'):.2f} per hour** (target ≤ 1/h).", "",
             f"Road-branch sensitivity: {bump_hit_n} of {bump_int_n} labelled speed-bump stretches "
             f"({pct(bump_hit_n / bump_int_n) if bump_int_n else '–'}) produced a road event "
             "(SPEED_BUMP or POTHOLE) — bumps are the only discrete road events PVS labels, so this "
             "is the best available check that the road branch reacts to real vertical events.", "",
             "Pothole reports by the road label at the time of the report:", "",
             md_table([{"label": k, "n": v} for k, v in sorted(pot_by_label.items())],
                      [("label", "label (surface/roughness)"), ("n", "POTHOLE reports")]), "",
             "PVS labels *stretches* of road (surface, roughness, speed bump), not individual "
             "holes, so a POTHOLE report on a `bad` stretch is plausible and one on a "
             "`speed_bump` or `asphalt/ok` stretch is a false report.", ""]
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# 2. RoadSens-4M
# ---------------------------------------------------------------------------

def section_roadsens(res: dict) -> str:
    files = ds.roadsens_files()
    if not files:
        return "## 2. RoadSens-4M potholes\n\n_data/roadsens/x not present — skipped._\n"
    tot_h = 0.0
    pot_int, pot_hit = 0, 0
    bump_int, bump_false = 0, 0
    normal_h, normal_false = 0.0, 0
    crash_fa = 0
    sess = 0
    hit_lat = []
    for d in ds.iter_roadsens():
        sess += 1
        e = Engine()
        ev, _ = e.run(d.acc, d.gyr)
        h = d.hours
        tot_h += h
        crash_fa += int(((ev == EV_CRASH) | (ev == EV_ROLLOVER)).sum())
        pot_idx = np.nonzero(ev == EV_POTHOLE)[0]
        pm = d.labels["pothole"].to_numpy()
        bm = d.labels["bump"].to_numpy()
        for a, b in ds.intervals(pm):
            pot_int += 1
            # allow the 0.6 s decision delay past the end of the stretch
            hits = pot_idx[(pot_idx >= a) & (pot_idx < b + 60)]
            if len(hits):
                pot_hit += 1
                hit_lat.append((hits[0] - a) / 100.0)
        for a, b in ds.intervals(bm):
            bump_int += 1
            if ((pot_idx >= a + 60) & (pot_idx < b)).any():   # strictly inside the bump stretch
                bump_false += 1
        if d.meta.get("normal_road"):
            normal_h += h
            normal_false += len(pot_idx)
    res["roadsens"] = {"hours": tot_h, "sessions": sess, "pothole_stretches": pot_int,
                       "pothole_stretches_hit": pot_hit, "bump_stretches": bump_int,
                       "bump_stretches_with_pothole_report": bump_false,
                       "normal_hours": normal_h, "normal_pothole_reports": normal_false,
                       "crash_false_alarms": crash_fa}
    lines = ["## 2. RoadSens-4M — pothole recall and false pothole reports", "",
             f"{sess} sessions, **{tot_h:.2f} h** (smartphone on the dashboard, 100 Hz, Bangladesh "
             "urban roads). An observer held a button over *stretches* of pothole road and over "
             "speed bumps, so the ground truth is per-stretch, not per-hole.", "",
             md_table([
                 {"k": "Pothole stretches with ≥1 POTHOLE report (recall)",
                  "v": f"{pot_hit} / {pot_int} = {pct(pot_hit / pot_int) if pot_int else '–'}"},
                 {"k": "Median delay from stretch start to first report",
                  "v": f"{np.median(hit_lat):.1f} s" if hit_lat else "–"},
                 {"k": "Speed-bump stretches that produced a POTHOLE report (false)",
                  "v": f"{bump_false} / {bump_int} = {pct(bump_false / bump_int) if bump_int else '–'}"},
                 {"k": "POTHOLE reports in normal-road sessions",
                  "v": f"{normal_false} in {normal_h:.2f} h"},
                 {"k": "CRASH/ROLLOVER false alarms", "v": f"{crash_fa} in {tot_h:.2f} h"},
             ], [("k", "metric"), ("v", "value")]), ""]
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# 3. VZCrash
# ---------------------------------------------------------------------------

def _model():
    """Read the fitted weights from src/crash_model.h so the offline threshold
    sweep uses exactly the engine's score."""
    import re
    txt = (ROOT / "src" / "crash_model.h").read_text()
    w = [float(x) for x in re.search(r"CRASH_MODEL_W\[\d+\] = \{([^}]*)\}", txt).group(1).replace("f", "").split(",")]
    b = float(re.search(r"CRASH_MODEL_BIAS = ([-\d.]+)f", txt).group(1))
    thr = float(re.search(r"CRASH_MODEL_THRESHOLD = ([-\d.]+)f", txt).group(1))
    return np.array(w, np.float64), b, thr


def run_vzcrash(split: str, quick: int | None):
    """Per event: label, fired (crash/rollover), tier of the strongest capture,
    peak features, max score over captures, override flag. Returns dict rows."""
    rows = []
    per_shard = Counter()
    e = Engine()
    w, b, _ = _model()
    override_g = e.cfg.override_sus50_g
    ov_peak, ov_peak_sus = e.cfg.override_peak_g, e.cfg.override_peak_sus_g
    for shard, eid, lab, acc, gyr, spd, vs in ds.iter_vzcrash(split):
        if quick and per_shard[shard] >= quick:
            continue
        per_shard[shard] += 1
        e.reset()
        ev, feats = e.run(acc, gyr)
        fired = bool(((ev == EV_CRASH) | (ev == EV_ROLLOVER)).any())
        rollover = bool((ev == EV_ROLLOVER).any())
        pothole = int((ev == EV_POTHOLE).sum())
        if len(feats):
            k = int(np.argmax(feats[:, 0]))          # strongest capture = highest peak_a
            f = feats[k]
            scores = b + np.log1p(np.clip(feats, 0, None)) @ w
            max_score = float(scores.max())
            override = bool(((feats[:, 1] >= override_g) |
                             ((feats[:, 0] >= ov_peak) & (feats[:, 1] >= ov_peak_sus))).any())
        else:
            f = np.zeros(len(FEAT_NAMES), np.float32)
            max_score, override = -99.0, False
        rows.append({"shard": shard, "event_id": eid, "label": lab, "fired": fired,
                     "pothole": pothole, "n_captures": len(feats), "vehiclesize": vs,
                     "max_score": max_score, "override": override, "rollover": rollover,
                     **{n: float(v) for n, v in zip(FEAT_NAMES, f)}})
    return rows


def tier_of(r, override_g=2.5, peak_g=4.0):
    if r["sus50_h"] >= override_g or r["peak_a"] >= peak_g:
        return "severe"
    if r["peak_a"] >= 3.0:
        return "moderate"
    if r["peak_a"] >= 1.8:
        return "minor (≥1.8 g)"
    return "below pre-trigger (<1.8 g)"


def section_vzcrash(res: dict, quick: int | None) -> str:
    if not ds.vzcrash_shards("test"):
        return ("## 3. VZCrash — crash recall by severity tier\n\n_data/vzcrash has no test shards "
                "— run eval/download_vzcrash.py (needs a Hugging Face login)._\n")
    t0 = time.time()
    rows = run_vzcrash("test", quick)
    dt = time.time() - t0
    lab = np.array([r["label"] for r in rows])
    fired = np.array([r["fired"] for r in rows])
    n_crash = int((lab == 0).sum())
    n_non = int((lab != 0).sum())
    recall_all = fired[lab == 0].mean() if n_crash else float("nan")
    fpr = fired[lab != 0].mean() if n_non else float("nan")

    # tiers
    tiers = ["severe", "moderate", "minor (≥1.8 g)", "below pre-trigger (<1.8 g)"]
    trows = []
    for tname in tiers:
        m = np.array([tier_of(r) == tname for r in rows])
        for lv, lname in ds.VZ_LABELS.items():
            mm = m & (lab == lv)
            trows.append({"tier": tname, "label": lname, "n": int(mm.sum()),
                          "fired": pct(fired[mm].mean()) if mm.any() else "–"})

    # threshold sweep: fired(thr) = override OR max_score >= thr (what the engine does)
    _, _, thr_cfg = _model()
    ovr = np.array([r["override"] for r in rows]) | np.array([r["rollover"] for r in rows])
    roll = np.array([r["rollover"] for r in rows])
    ms = np.array([r["max_score"] for r in rows])
    tier_arr = np.array([tier_of(r) for r in rows])
    hde_rate_drive = (HDE_EVENTS / HDE_VEHICLES) / (2 * 8.0)
    srows = []
    for thr in [0.0, 0.5, 1.0, 1.5, 2.0, thr_cfg, 2.5, 3.0, 3.5, 4.0, 5.0, 99.0]:
        fd = ovr | (ms >= thr)
        r = {"thr": ("**%.2f (configured)**" % thr) if thr == thr_cfg else ("override only" if thr == 99.0 else f"{thr:.2f}"),
             "all": pct(fd[lab == 0].mean()),
             "sev": pct(fd[(lab == 0) & (tier_arr == "severe")].mean()),
             "mod": pct(fd[(lab == 0) & (tier_arr == "moderate")].mean()),
             "min": pct(fd[(lab == 0) & (tier_arr == "minor (≥1.8 g)")].mean()),
             "fpr": pct(fd[lab != 0].mean()),
             "fph": f"{fd[lab != 0].mean() * hde_rate_drive:.4f}"}
        srows.append(r)
    consistent = bool(np.array_equal(fired, ovr | (ms >= thr_cfg)))

    # per-hour conversion
    hde_per_vehicle = HDE_EVENTS / HDE_VEHICLES
    rate_wall = hde_per_vehicle / HDE_WALL_H                 # per vehicle wall-clock hour
    rate_drive8 = hde_per_vehicle / (2 * 8.0)                # if each vehicle drove 8 h/day
    fp_wall, fp_drive = fpr * rate_wall, fpr * rate_drive8

    # fraction of negatives that even reach the pre-trigger (what the detector "sees")
    reach = np.array([r["peak_a"] >= 1.8 for r in rows])
    reach_non = reach[lab != 0].mean()

    res["vzcrash"] = {"n_events": len(rows), "n_crash": n_crash, "n_noncrash": n_non,
                      "recall_all": float(recall_all), "fpr": float(fpr),
                      "fp_per_vehicle_wall_hour": fp_wall, "fp_per_driving_hour_8h": fp_drive,
                      "tiers": trows, "seconds": dt, "quick": quick}
    by_label = Counter(lab.tolist())
    lines = ["## 3. VZCrash — crash recall by severity tier", "",
             f"{len(rows)} test-split events in {dt:.0f} s "
             f"({', '.join(f'{ds.VZ_LABELS[k]} {v}' for k, v in sorted(by_label.items()))})"
             + (f" — **quick mode, first {quick} events per shard**" if quick else "") + ".", "",
             "Every VZCrash event — including `normal_driving` — was pre-selected by the "
             "telematics unit's harsh-driving trigger (paper, Sec. II), so the negatives are "
             "the hardest 'what could fool us' population, not ordinary driving. Ordinary "
             "driving is section 1.", "",
             md_table([{"k": "Crash recall, all labelled crashes", "v": pct(recall_all)},
                       {"k": "False-positive rate on harsh-event negatives (near-miss + normal)",
                        "v": pct(fpr)},
                       {"k": "Negatives that even reach the 1.8 g pre-trigger", "v": pct(reach_non)}],
                      [("k", "metric"), ("v", "value")]), "",
             "### By severity tier (tier = strongest capture of the event)", "",
             md_table(trows, [("tier", "tier"), ("label", "label"), ("n", "n"), ("fired", "fired")]), "",
             "### Operating-point sweep (score threshold)", "",
             "The engine fires when a physics override holds, **or** the rollover branch "
             "fires, **or** the score of any capture reaches the threshold. The threshold is the "
             "one knob that trades recall for false alarms; the configured value was chosen on "
             "the *train* split's held-out fold (`eval/fit_model.py`), never on this test split. "
             f"Offline recomputation of the engine's decision at the configured threshold matches "
             f"the engine exactly: {'yes' if consistent else 'NO — investigate'}. The rollover "
             f"branch (tilt ≥ 60° for 1 s) fired on {int(roll[lab == 0].sum())} crashes and "
             f"{int(roll[lab != 0].sum())} non-crash events ({pct(roll[lab != 0].mean())}); the "
             "split contains no labelled rollovers, so its recall cannot be measured here.", "",
             md_table(srows, [("thr", "threshold"), ("all", "recall all"), ("sev", "severe"),
                              ("mod", "moderate"), ("min", "minor"), ("fpr", "FPR harsh"),
                              ("fph", "false alarms / driving h")]), "",
             "### Converted to false alarms per hour", "",
             f"The paper reports {HDE_EVENTS:,} harsh-driving events from {HDE_VEHICLES:,} vehicles "
             f"over {HDE_WALL_H:.0f} h → {hde_per_vehicle:.2f} harsh events per vehicle. "
             "Our detector only ever fires on a harsh event (nothing below the 1.8 g pre-trigger "
             "is scored), so false alarms/hour = FPR × harsh events/hour:", "",
             md_table([{"k": "per vehicle wall-clock hour (48 h window)", "r": rate_wall, "fp": fp_wall,
                        "h": 1 / fp_wall if fp_wall else float("inf")},
                       {"k": "per driving hour, assuming 8 h driving/day", "r": rate_drive8, "fp": fp_drive,
                        "h": 1 / fp_drive if fp_drive else float("inf")}],
                      [("k", "basis"), ("r", "harsh events / h"), ("fp", "false alarms / h"),
                       ("h", "hours per false alarm")],
                      {"r": "{:.3f}", "fp": "{:.4f}", "h": "{:.0f}"}), "",
             "Caveat: the commercial fleet in the paper drives more aggressively than a private "
             "car, and the harsh-event trigger threshold is not published; both push the "
             "real-world rate *down*, so these are conservative.", ""]
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# 4. timing
# ---------------------------------------------------------------------------

def section_timing(res: dict) -> str:
    rng = np.random.default_rng(0)
    n = 2_000_000
    acc = np.tile([0.0, 0.0, 1.0], (n, 1)).astype(np.float32) + rng.normal(0, 0.05, (n, 3)).astype(np.float32)
    gyr = rng.normal(0, 2.0, (n, 3)).astype(np.float32)
    e = Engine()
    best = 1e9
    for _ in range(3):
        t0 = time.perf_counter()
        e.run(acc, gyr)
        best = min(best, (time.perf_counter() - t0) / n * 1e9)
    res["timing_ns_per_sample_host"] = best
    return "\n".join(["## 4. Compute cost", "",
                      f"`engine_update` on this laptop (gcc -O2, x86-64): **{best:.0f} ns/sample** over "
                      f"{n:,} synthetic samples, including the Python→C batch call. At 100 Hz the budget "
                      "is 10,000,000 ns/sample; even if the ESP32 (240 MHz Xtensa LX6, single-precision "
                      f"FPU) were 300× slower it would use {best * 300 / 1e7:.1%} of it.", ""])


# ---------------------------------------------------------------------------

def main(argv):
    ap = argparse.ArgumentParser()
    ap.add_argument("--skip", nargs="*", default=[], choices=["pvs", "roadsens", "vzcrash", "timing"])
    ap.add_argument("--quick", action="store_true", help="VZCrash: 3000 events per shard")
    ap.add_argument("--print", action="store_true", help="also print evaluation.md to the terminal")
    args = ap.parse_args(argv)
    res = {}
    e = Engine()
    res["cfg"] = e.cfg_dict()
    sections = ["# Evaluation — Feature 3 engine (`src/engine.c`)", "",
                "Generated by `eval/run_eval.py`; do not edit by hand. Design: `algorithm.md`. "
                "The same compiled C runs here and on the ESP32.", "",
                "Engine configuration: `" + ", ".join(f"{k}={v:g}" for k, v in res["cfg"].items()) + "`", ""]
    if "pvs" not in args.skip:
        print("PVS ...", file=sys.stderr)
        sections.append(section_pvs(res))
    if "roadsens" not in args.skip:
        print("RoadSens ...", file=sys.stderr)
        sections.append(section_roadsens(res))
    if "vzcrash" not in args.skip:
        print("VZCrash ...", file=sys.stderr)
        sections.append(section_vzcrash(res, 3000 if args.quick else None))
    if "timing" not in args.skip:
        print("timing ...", file=sys.stderr)
        sections.append(section_timing(res))
    OUT_MD.write_text("\n".join(sections), encoding="utf-8")
    OUT_JSON.parent.mkdir(exist_ok=True)
    OUT_JSON.write_text(json.dumps(res, indent=1, default=str), encoding="utf-8")
    if args.print:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
        print("\n".join(sections))
        print()
    print(f"wrote {OUT_MD}")


if __name__ == "__main__":
    main(sys.argv[1:])
