#!/usr/bin/env python3
"""Sweep the road-branch thresholds and report, per setting:

  PVS      bump-stretch sensitivity (labelled speed-bump stretches with a road
           event), POTHOLE reports on asphalt/ok and on bump stretches (false),
           POTHOLE reports on bad/unpaved stretches (plausible)
  RoadSens pothole-stretch recall, POTHOLE reports in bump stretches / normal road

Prints a table; pick the setting and put it in engine_cfg_default().

    .venv\\Scripts\\python eval\\sweep_road.py
"""
from __future__ import annotations

import itertools
import sys
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parent))
import datasets as ds  # noqa: E402
from engine import EV_POTHOLE, EV_SPEED_BUMP, Engine  # noqa: E402

# load everything once
PVS = list(ds.iter_pvs())
RS = list(ds.iter_roadsens())
print(f"{len(PVS)} PVS drives ({sum(d.hours for d in PVS):.2f} h), {len(RS)} RoadSens sessions "
      f"({sum(d.hours for d in RS):.2f} h)", file=sys.stderr)


def evaluate(**cfg) -> dict:
    r = dict(cfg)
    # ---- PVS
    bump_int = bump_hit = 0
    pot_ok = pot_bad = pot_bump = 0
    h = 0.0
    for d in PVS:
        e = Engine(**cfg)
        ev, _ = e.run(d.acc, d.gyr)
        h += d.hours
        lab = d.labels
        bump = ((lab["speed_bump_asphalt"] == 1) | (lab["speed_bump_cobblestone"] == 1)).to_numpy()
        bad = ((lab["bad_road_left"] == 1) | (lab["bad_road_right"] == 1) |
               (lab["unpaved_road"] == 1)).to_numpy()
        road_idx = np.nonzero((ev == EV_POTHOLE) | (ev == EV_SPEED_BUMP))[0]
        for a, b in ds.intervals(bump):
            bump_int += 1
            if ((road_idx >= a) & (road_idx < b + 100)).any():
                bump_hit += 1
        for i in np.nonzero(ev == EV_POTHOLE)[0]:
            j = slice(max(0, i - 100), i + 1)
            if bump[j].any():
                pot_bump += 1
            elif bad[j].any():
                pot_bad += 1
            else:
                pot_ok += 1
    r.update(pvs_h=h, bump_sens=bump_hit / bump_int if bump_int else np.nan,
             pot_ok_per_h=pot_ok / h, pot_bad_per_h=pot_bad / h, pot_on_bump=pot_bump)
    # ---- RoadSens
    pot_int = pot_hit = bump_int2 = bump_false = 0
    normal_h = normal_false = 0
    for d in RS:
        e = Engine(**cfg)
        ev, _ = e.run(d.acc, d.gyr)
        pot_idx = np.nonzero(ev == EV_POTHOLE)[0]
        pm = d.labels["pothole"].to_numpy()
        bm = d.labels["bump"].to_numpy()
        for a, b in ds.intervals(pm):
            pot_int += 1
            if ((pot_idx >= a) & (pot_idx < b + 60)).any():
                pot_hit += 1
        for a, b in ds.intervals(bm):
            bump_int2 += 1
            if ((pot_idx >= a + 60) & (pot_idx < b)).any():
                bump_false += 1
        if d.meta.get("normal_road"):
            normal_h += d.hours
            normal_false += len(pot_idx)
    r.update(rs_pot_recall=pot_hit / pot_int if pot_int else np.nan,
             rs_bump_false=bump_false / bump_int2 if bump_int2 else np.nan,
             rs_normal_per_h=normal_false / normal_h if normal_h else np.nan)
    return r


def main():
    grid = {
        "road_trig_g": [0.30, 0.35, 0.45],
        "road_quiet_std_g": [0.10, 0.15, 0.25, 1.0],
        "pot_drop_g": [0.20, 0.30, 0.35],
        "pot_impact_g": [0.30, 0.45],
    }
    keys = list(grid)
    rows = []
    for vals in itertools.product(*grid.values()):
        cfg = dict(zip(keys, vals))
        rows.append(evaluate(**cfg))
        r = rows[-1]
        print(" ".join(f"{k}={v:g}" for k, v in cfg.items()),
              f"| PVS bump_sens={r['bump_sens']:.0%} pot_ok/h={r['pot_ok_per_h']:.1f} "
              f"pot_bad/h={r['pot_bad_per_h']:.1f} pot_on_bump={r['pot_on_bump']} "
              f"| RS pot_recall={r['rs_pot_recall']:.0%} bump_false={r['rs_bump_false']:.0%} "
              f"normal/h={r['rs_normal_per_h']:.1f}", flush=True)
    import json
    (ds.ROOT / "build" / "sweep_road.json").write_text(json.dumps(rows, indent=1, default=float))


if __name__ == "__main__":
    main()
