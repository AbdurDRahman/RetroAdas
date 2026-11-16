"""Diagnostics: what do pothole / bump / normal stretches look like in RoadSens,
and which branch produced the crash false alarms? Writes nothing; prints."""
import sys
from collections import Counter
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parent))
import datasets as ds  # noqa: E402
from engine import EV_CRASH, EV_NAMES, EV_ROLLOVER, FEAT_NAMES, Engine  # noqa: E402


def vertical_linear(acc, tau=1.0, dt=0.01):
    """Same gravity tracker as engine.c, in numpy, to look at a_v offline."""
    a = dt / (tau + dt)
    g = acc[0].copy()
    av = np.zeros(len(acc), np.float32)
    ah = np.zeros(len(acc), np.float32)
    for i, s in enumerate(acc):
        m = np.linalg.norm(s)
        if abs(m - 1) <= 0.5:
            g += a * (s - g)
        u = g / max(np.linalg.norm(g), 0.2)
        lin = s - g
        av[i] = lin @ u
        ah[i] = np.linalg.norm(lin - av[i] * u)
    return av, ah


def main():
    stats = {k: [] for k in ("pothole", "bump", "none")}
    fa = []
    ev_types = Counter()
    for d in ds.iter_roadsens():
        pm = d.labels["pothole"].to_numpy()
        bm = d.labels["bump"].to_numpy()
        av, ah = vertical_linear(d.acc)
        amag = np.linalg.norm(d.acc, axis=1)
        # per 1 s block: std(a_v), min, max, max|a|
        n = len(av) // 100
        for k in range(n):
            sl = slice(k * 100, (k + 1) * 100)
            cls = "pothole" if pm[sl].mean() > 0.5 else ("bump" if bm[sl].mean() > 0.5 else "none")
            stats[cls].append((av[sl].std(), av[sl].min(), av[sl].max(), amag[sl].max(), ah[sl].max()))
        e = Engine()
        ev, feats = e.run(d.acc, d.gyr)
        for i in np.nonzero((ev == EV_CRASH) | (ev == EV_ROLLOVER))[0]:
            ev_types[EV_NAMES[ev[i]]] += 1
            # feature row of the last capture before i
            fa.append((d.name, i / 100, EV_NAMES[ev[i]], feats[-1] if len(feats) else None,
                       amag[max(0, i - 60):i + 1].max()))
    for cls, rows in stats.items():
        r = np.array(rows)
        if not len(r):
            continue
        print(f"{cls:8s} n={len(r):5d} 1-s blocks | std(a_v) p50={np.percentile(r[:,0],50):.3f} "
              f"p90={np.percentile(r[:,0],90):.3f} | min a_v p10={np.percentile(r[:,1],10):.2f} "
              f"p50={np.percentile(r[:,1],50):.2f} | max a_v p50={np.percentile(r[:,2],50):.2f} "
              f"p90={np.percentile(r[:,2],90):.2f} | max|a| p50={np.percentile(r[:,3],50):.2f} "
              f"p90={np.percentile(r[:,3],90):.2f} p99={np.percentile(r[:,3],99):.2f} "
              f"| max a_h p90={np.percentile(r[:,4],90):.2f} p99={np.percentile(r[:,4],99):.2f}")
    print("\nfalse alarms by type:", dict(ev_types))
    for name, t, typ, f, pk in fa:
        print(f"  {name:22s} t={t:6.1f}s {typ:9s} peak|a| last 0.6s={pk:.2f} "
              + (" ".join(f"{n}={v:.2f}" for n, v in zip(FEAT_NAMES, f)) if f is not None else ""))


if __name__ == "__main__":
    main()
