# stats.md — Benchmark & Threshold Test Plan

Test plan for validating the Layer 1 / Layer 2 detection design (see
`info.md` for full derivations, formulas, and citations) against the local
VZCrash sample at `data/vzcrash/`. Read `info.md` first — this file only
lists what to test and with which values, not why those values were chosen.

## Test data

- `data/vzcrash/test-00000-of-00002.parquet` — 12,087 events: 4,221 crash
  (label 0), 1,366 near_miss (label 1), 6,500 normal_driving (label 2). Use
  this shard for crash-recall tests.
- `data/vzcrash/test-00001-of-00002.parquet` — 12,087 events: 0 crash, 2,267
  near_miss, 9,820 normal_driving. Use for false-positive-rate tests
  (crash-free).
- Columns: `gsensor` (tri-axial accel, g), `gyro` (tri-axial, deg/s),
  `gps_speed` (km/h), all **100Hz**, 1,020–4,100 samples/event.
- **Resample or re-derive window sizes at 100Hz** before applying any
  52Hz-derived constant below — don't apply the sample-count values
  (k=30, etc.) directly against 100Hz data without converting.

## Test 1 — Layer 1: emergency-braking separability

**Parameters to test:**
- Magnitude fusion: `A = √(Ax²+Ay²+Az²)`, `G = √(ωpitch²+ωroll²+ωyaw²)`
- Moving average window: k=30 samples @ 52Hz (≈0.58s) → recompute as ~58
  samples at 100Hz to preserve the same time window
- Discriminator: peak `A` high + peak `G` low → near_miss; peak `A` high +
  peak `G` high → normal driving (pothole/bump-style oscillation)

**Procedure:** compute moving-average `AA[n]` and gyro moving-average over
each event in both shards, plot peak-accel vs. peak-gyro per event, check
whether `label=1` (near_miss) events separate from `label=2`
(normal_driving) events via a convex-hull boundary on that scatter (same
validation method as the source methodology).

**Metric:** separation margin between near_miss and normal_driving clusters;
sweep k across a range (e.g. 20–100 samples at 100Hz) and pick the value
that maximizes separation, same as the source method's own k-selection
process.

## Test 2 — Layer 2: G-force banding

**Parameters to test:**
```
‖A‖g = √(ax²+ay²+az²) / g
No accident: <5G | Mild: 5–20G | Moderate: 20–40G | Severe: ≥40G
```

**Procedure:** compute peak `‖A‖g` per event across both shards. Check what
fraction of `label=0` (crash) events land in Moderate/Severe (target: high
recall) vs. what fraction of `label=1`/`label=2` events land there (target:
low false-positive rate).

**Metric:** recall on crash events at the Moderate+ threshold; false-positive
rate on near_miss + normal_driving.

## Test 3 — Layer 2: complementary-filter tilt/rollover

**Parameters to test:**
- `θ[n] = α·(θ[n-1] + ω·Δt) + (1-α)·θ_accel[n]`, Δt = 1/100s (data rate)
- Sweep α across 0.9–0.98
- Rollover flag: `|θ| > 46°` and, separately, `|θ| > 70–80°` — test both,
  since the two source thresholds differ (see `info.md`)

**Procedure:** run the filter over each event's `ax, ay, az, gyro` stream,
check whether peak `|θ[n]|` crosses each threshold for `label=0` events
(recall) vs. `label=1`/`label=2` events (false-positive rate).

**Metric:** same recall/false-positive framing as Test 2, compared across
the two threshold candidates (46° vs. 70–80°) to pick one.

## Test 4 — Alternative collision rule (consecutive-delta + speed drop)

**Parameters to test:**
```
Collision = (|A[n] - A[n-1]| > threshold) AND (gps_speed dropping)
```

**Procedure:** VZCrash's `gsensor` is already in g's (not raw register
units), so the threshold from `info.md` (~2000, register-unit-specific)
does not transfer directly — sweep a g-based delta threshold instead, gated
on `gps_speed[n] < gps_speed[n-1]` (speed decreasing between samples).

**Metric:** recall/false-positive rate, same framing as Tests 2–3, and
compare directly against Test 2's G-force-banding result on the same
events — pick whichever gives better separation, or combine both as an OR
condition per the "either condition trips" logic already in the design.

## Output to produce

For each test: a recall/false-positive-rate number per parameter setting,
and the parameter value that best balances the two (informs the
**false-positive rate target** and **threshold calibration** items still
marked open in `CLAUDE.md`). No benchmarking code exists yet — this file is
the plan, not the results.
