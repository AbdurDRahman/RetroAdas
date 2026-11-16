#!/usr/bin/env python3
"""Load a Sensor Logger (Android/iOS app) export of your own drive and run
the engine over it, so potholes / speed bumps / hard braking on *your* roads
can be checked event by event. See own_data.md for how to record.

    .venv\\Scripts\\python eval\\own_recording.py data\\own\\2026-11-20_drive1.zip
    .venv\\Scripts\\python eval\\own_recording.py data\\own\\drive1\\       (unzipped folder)

Prints every engine event with its time, and — if you used the app's
annotation button — the annotation text active at that moment, plus a
summary table (events per annotation label).
"""
from __future__ import annotations

import sys
import zipfile
from collections import Counter
from io import BytesIO
from pathlib import Path

import numpy as np
import pandas as pd

sys.path.insert(0, str(Path(__file__).resolve().parent))
from datasets import G, RAD2DEG  # noqa: E402
from engine import EV_NAMES, Engine  # noqa: E402


def read_export(path: Path) -> dict[str, pd.DataFrame]:
    """Sensor Logger exports a zip (or folder) of per-sensor CSVs."""
    out = {}
    if path.is_dir():
        for f in path.glob("*.csv"):
            out[f.stem] = pd.read_csv(f)
    else:
        with zipfile.ZipFile(path) as z:
            for n in z.namelist():
                if n.lower().endswith(".csv"):
                    out[Path(n).stem] = pd.read_csv(BytesIO(z.read(n)))
    return out


def resample(df: pd.DataFrame, cols: list[str], t_grid: np.ndarray) -> np.ndarray:
    t = df["seconds_elapsed"].to_numpy(np.float64)
    return np.stack([np.interp(t_grid, t, df[c].to_numpy(np.float64)) for c in cols], axis=1)


def main(path: str):
    files = read_export(Path(path))
    acc_name = "TotalAcceleration" if "TotalAcceleration" in files else None
    if acc_name is None:
        raise SystemExit("export has no TotalAcceleration.csv — enable 'Total Acceleration' in the app")
    if "Gyroscope" not in files:
        raise SystemExit("export has no Gyroscope.csv — enable 'Gyroscope' in the app")
    acc_df, gyr_df = files[acc_name], files["Gyroscope"]
    t0 = max(acc_df["seconds_elapsed"].min(), gyr_df["seconds_elapsed"].min())
    t1 = min(acc_df["seconds_elapsed"].max(), gyr_df["seconds_elapsed"].max())
    t = np.arange(t0, t1, 0.01)
    acc = resample(acc_df, ["x", "y", "z"], t).astype(np.float32) / G
    gyr = resample(gyr_df, ["x", "y", "z"], t).astype(np.float32) * RAD2DEG
    print(f"{len(t) / 100:.0f} s at 100 Hz, peak |a| {np.linalg.norm(acc, axis=1).max():.2f} g")

    ann = None
    if "Annotation" in files and len(files["Annotation"]):
        a = files["Annotation"]
        ann = [(float(r["seconds_elapsed"]), str(r.get("text", ""))) for _, r in a.iterrows()]
        print(f"{len(ann)} annotations")

    def label_at(ts: float) -> str:
        if not ann:
            return ""
        # nearest annotation within 3 s before the event
        best = ""
        for ta, txt in ann:
            if 0 <= ts - ta <= 3.0:
                best = txt
        return best

    e = Engine()
    ev, feats = e.run(acc, gyr, max_feat=4096)
    summary = Counter()
    for i in np.nonzero(ev)[0]:
        name = EV_NAMES[int(ev[i])]
        lab = label_at(t[i] - 0.6)   # decision ~0.6 s after the trigger
        print(f"t={t[i]:8.2f}s  {name:12s} {lab}")
        summary[(name, lab or "(no annotation)")] += 1
    print("\nevents by annotation:")
    for (name, lab), n in sorted(summary.items()):
        print(f"  {name:12s} {lab:20s} {n}")
    if ann:
        hit = Counter()
        for ta, txt in ann:
            idx = (t >= ta - 0.5) & (t <= ta + 3.0)
            hit[(txt, "detected" if (ev[idx] > 0).any() else "missed")] += 1
        print("\nannotations by outcome (engine event within -0.5..+3 s):")
        for (txt, outcome), n in sorted(hit.items()):
            print(f"  {txt:20s} {outcome:9s} {n}")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    main(sys.argv[1])
