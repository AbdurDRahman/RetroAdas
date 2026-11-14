# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

This is the home for Feature 3 (Automatic Crash Detection) of the RetroADAS
monorepo, one level up at `../`. The Layer 2 detectors exist as
ESP32-portable C and have been benchmarked against VZCrash; Layer 1
(Test 1 in `stats.md`) and the on-device firmware loop are not written yet.

Read these before writing code here:
- `../features/feature-3-crash-detection.md` — the original feature spec and
  open questions.
- `info.md` — feasibility research and the algorithm design (hardware
  target, detection math, thresholds, references). Authoritative where it
  conflicts with the older feature spec.
- `stats.md` — the benchmark/test plan (Tests 1–4).
- `results.md` — **generated** benchmark results for Tests 2–4 (accuracy
  and per-sample cost). Read this before trusting any threshold in
  `info.md`: the literature G-force bands do not transfer to this data.

## Code layout & commands

- `src/crash_detect.{h,c}` — the detectors, plain C99, float-only, no heap,
  state in caller-owned structs. Meant to drop into an Arduino/ESP-IDF
  project unchanged. One `*_update()` call per IMU sample.
- `bench/export_events.py` — parquet → `data/bench/*.bin` (run once).
- `bench/bench.c` — host harness: per-event peaks + ns/sample timing.
- `bench/run_bench.py` — builds and runs the harness on both shards, sweeps
  thresholds, writes `results.md`. Never edit `results.md` by hand.

```bash
.venv/bin/python3 bench/export_events.py        # once, ~1 min, 888 MB
.venv/bin/python3 bench/run_bench.py            # build + run + results.md
.venv/bin/python3 bench/run_bench.py --no-build # re-analyse cached peaks
gcc -std=c99 -Wall -Wextra -c src/crash_detect.c -o /dev/null  # compile check
```

Commit convention here: one commit per block of code, message = what that
block does (e.g. `Test 3: complementary-filter tilt estimator`), so the
history reads as a walkthrough.

The project-level overview is `../README.md`, and hardware context (Jetson
Orin Nano, IMU/GPS modules, cellular options) is in `../hardware_bom.md`.

## Architecture: independent controller, two detection layers

This subsystem runs on its **own ESP32-class microcontroller**, decoupled
from the Jetson — not as a service on the Jetson itself. That's a resolved
decision (was previously open): it means crash detection keeps working
during Sentry Mode or a Jetson reboot, and the code here is
embedded/MCU-style (C/C++, Arduino-ish libraries), not a Python/Linux
service.

Controller I/O: IMU (accel+gyro) in, False Alarm Switch in, Buzzer out,
signal to Jetson out (on confirmed event).

Detection is split into two independently-justified layers — don't conflate
them, they use different math and answer different questions:

1. **Layer 1 — Emergency braking (pre-crash / near-miss).** Magnitude-fused
   accelerometer + gyroscope through a **sliding moving average** (window
   k=30 samples @ 52 Hz ≈ 0.58s). Discriminator: emergency braking = high
   accel + *low* angular velocity (straight-line deceleration); potholes/speed
   bumps = high accel + *high* angular velocity (oscillation). Reference:
   Pranoto et al. (2025), DOI: 10.3390/vehicles7040106. Measured ESP32 cost:
   ~535µs/update, ~2.8% of the 19.2ms/sample budget at 52Hz.
2. **Layer 2 — Impact/rollover (post-impact).** G-force magnitude
   (`‖A‖g = √(ax²+ay²+az²)/g`) banded into mild (5–20G) / moderate (20–40G) /
   severe (>40G), OR a **complementary filter** tilt angle
   (`θ[n] = α·(θ[n-1] + ω·Δt) + (1-α)·θ_accel`, α≈0.95–0.98) exceeding
   ~70–80°. Either condition tripping fires the event. References: Ibrahim,
   Aly & Far (2016) DOI: 10.1109/CCECE.2016.7726720; Razali et al. (2021) DOI:
   10.1109/ICRAIE52900.2021.9703970; Chikaka & Longe (2021) DOI:
   10.1109/RTSI50628.2021.9597212. Estimated ESP32 cost: <100µs/sample
   (reasoned, not measured in any cited source — flag as such if it matters).

Both layers together use well under 5% of the per-sample time budget at
52Hz, leaving headroom for GPS polling, the cancel button, buzzer, and
Jetson comms on the same loop. Full formulas, the IMU dataset table from the
reference paper, and per-threshold literature citations are in `info.md` —
don't re-derive them, look them up there when implementing.

**Sampling:** 52 Hz IMU output data rate (a chip setting), separate from the
400 kHz I2C bus clock — don't conflate the two when configuring the sensor.

## Resolved vs. still-open questions

Resolved by `info.md` (previously open in the feature spec):
- **Compute target** → independent ESP32-class MCU, not the Jetson.
- **Sentry Mode availability** → yes, works independently since it's off
  Jetson entirely.
- **Emergency notification path** → ESP32 signals the Jetson on a confirmed
  event; the Jetson attaches GPS and pushes to the cloud. The
  call/SMS-to-emergency-contact step can be a dummy function or a simple API
  call for now — not a hard cellular-module requirement.

Still open / not yet quantified:
- **False-positive rate target** — no number yet; depends on calibration
  data.
- **Threshold calibration against real data** — the G-force/tilt thresholds
  above come from prior crash-test literature, not from this project's own
  calibration. The **VZCrash** dataset (Hugging Face, access-gated, ~190k
  events / 31,090 verified crashes, 100Hz tri-axial accel+gyro) is the
  identified candidate for that; see `info.md` for the link and companion
  paper.
- **Feature 2 auto-post integration** — still needs confirmation per the
  original spec (see below).

## Integration contract with `adas-cloud`

The ESP32 does **not** talk to the cloud directly — it has no network
connectivity of its own here. The path is: ESP32 detects/confirms →
signals the Jetson → Jetson attaches GPS → Jetson pushes to `adas-cloud` as
a hazard, so nearby RetroADAS vehicles see it via Feature 2:

- Endpoint: `POST /v1/hazards` (implemented in `../adas-cloud/internal/hazards/deduplication.go`)
- Use `event_tag: "accident"` (see the `hazards` table definition in
  `../adas-cloud/database/migrations/002_create_hazards_table.sql` for the
  full row shape: `location` as a PostGIS geography point, `confidence`,
  `reported_by` as this device's `device_id`, etc.)
- Devices authenticate as `device_id` + API key against the `devices` table
  (see `../adas-cloud/internal/devices/authentication.go`) — the same
  identity/auth flow the perception stack uses for hazard reporting applies
  here; the Jetson makes this call on behalf of the ESP32's event.

This auto-post-on-crash integration is itself flagged as unconfirmed in the
feature spec ("needs confirmation") — verify it's still the agreed behavior
before wiring it up. The emergency-contact call/SMS step can be stubbed as a
dummy function or simple API call for now (per `info.md`), not a full
cellular integration.

## Local benchmarking dataset (VZCrash)

A slice of the VZCrash dataset (the candidate calibration dataset named in
`info.md`) is checked out locally at `data/vzcrash/` for testing/benchmarking
the detection algorithm against real labelled events, without pulling the
full ~7.7GB dataset:

- `data/vzcrash/test-00000-of-00002.parquet` and `test-00001-of-00002.parquet`
  — the full **test split** (24,174 events, ~940MB), pulled from
  `huggingface.co/datasets/vzc-research-chapter/VZCrash`.
- Columns: `event_id`, `label` (0=crash, 1=near_miss, 2=normal_driving),
  `vehiclesize`, `gyro_is_hd`, `gsensor` (tri-axial accel, g), `gyro`
  (tri-axial, deg/s), `gps_speed` (km/h). Accel/gyro are **100Hz**, not the
  52Hz assumed in the ESP32 design notes — resample/account for that when
  comparing against the Layer 1/2 thresholds above.
- Label balance across the two shards: shard `-00001-` has *no* crash-labeled
  rows (near_miss/normal_driving only) — use `-00000-` (4,221 crash /
  1,366 near_miss / 6,500 normal_driving) when you need actual crash
  examples.
- `data/` is gitignored — **not** committed. It's local-only scratch data;
  re-download it (or the train/validation splits) rather than expecting it
  to exist on a fresh clone.
- License: CC-BY-NC-4.0 (non-commercial). Dataset is access-gated on
  Hugging Face — re-downloading requires an `HF_TOKEN` from an account that
  has accepted the dataset's terms at the URL above.

`.venv/` (also gitignored) has `pandas` + `pyarrow` installed for reading
these files, e.g.:

```bash
.venv/bin/python3 -c "
import pandas as pd
df = pd.read_parquet('data/vzcrash/test-00000-of-00002.parquet')
print(df['label'].value_counts())
"
```
