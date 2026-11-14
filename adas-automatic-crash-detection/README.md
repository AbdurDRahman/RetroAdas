# Feature 3 — Automatic Crash Detection

RetroADAS subsystem that detects a collision or rollover from an IMU
(accelerometer + gyroscope) and raises an alarm. It runs on its **own
ESP32-class microcontroller**, separate from the Jetson, so it keeps working
during Sentry Mode or while the Jetson reboots.

> New to this folder? Read [`handover.md`](handover.md) first — it covers
> what is done, what is not, the known problems, and what to do next.

## Status

| Part | State |
|---|---|
| Layer 2 detectors (G-force, tilt filter, delta + speed drop) | Written in C, benchmarked on VZCrash |
| Benchmark harness + results | Done — see [`results.md`](results.md) |
| Layer 1 (emergency braking, moving average) — Test 1 | **Not started** |
| ESP32 firmware loop (IMU driver, buzzer, cancel button, Jetson signal) | **Not started** |
| Jetson → `adas-cloud` accident report | **Not started**, and still needs team confirmation |

## Documents — which one to read for what

| File | What it holds |
|---|---|
| [`handover.md`](handover.md) | Current state, known problems, next steps. Start here. |
| [`info.md`](info.md) | Research notes: hardware target, the detection math, literature thresholds, citations. |
| [`stats.md`](stats.md) | The benchmark plan (Tests 1–4) — what each test measures and with which values. |
| [`results.md`](results.md) | **Generated** benchmark results. Never edit by hand; re-run `bench/run_bench.py`. |
| [`../features/feature-3-crash-detection.md`](../features/feature-3-crash-detection.md) | The original feature spec. Where it disagrees with `info.md`, `info.md` wins. |

Important: the G-force thresholds in `info.md` come from crash-test
literature and **do not work** on this class of sensor. The thresholds that
do work are in `results.md`. See `handover.md` → "Key findings".

## Folder layout

```
src/crash_detect.h   detector API — plain C99, float only, no heap
src/crash_detect.c   detector implementations
bench/export_events.py   VZCrash parquet → flat binary (data/bench/*.bin)
bench/bench.c            C harness: runs every detector over every event,
                         writes per-event peak values + ns/sample timing
bench/run_bench.py       builds + runs bench.c, sweeps thresholds,
                         writes results.md
requirements.txt     Python packages for the bench scripts
```

Not in git (see `.gitignore`): `data/` (dataset, ~1.8 GB), `build/`
(compiled harness and cached results), `.venv/`.

## Setup

Needs `gcc` and Python 3.11+.

```bash
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
```

### Getting the dataset (VZCrash)

The benchmark uses the **test split** of
[VZCrash](https://huggingface.co/datasets/vzc-research-chapter/VZCrash)
(24,174 labelled events, 100 Hz accelerometer + gyroscope, 1 Hz GPS speed).
It is access-gated and licensed CC-BY-NC-4.0 (non-commercial use only):

1. Log in to Hugging Face, open the dataset page and accept its terms.
2. Create an access token (Settings → Access Tokens, read permission).
3. Download the two test shards into `data/vzcrash/`:

```bash
export HF_TOKEN=hf_...   # your token
.venv/bin/python3 - <<'EOF'
from huggingface_hub import snapshot_download
snapshot_download("vzc-research-chapter/VZCrash", repo_type="dataset",
                  allow_patterns=["*test-*.parquet"], local_dir="data/vzcrash-raw")
EOF
mkdir -p data/vzcrash
find data/vzcrash-raw -name 'test-*.parquet' -exec mv {} data/vzcrash/ \;
```

You should end up with exactly:

```
data/vzcrash/test-00000-of-00002.parquet   12,087 events, contains all 4,221 crashes
data/vzcrash/test-00001-of-00002.parquet   12,087 events, no crashes at all
```

Column reference: `event_id`, `label` (0 = crash, 1 = near_miss,
2 = normal_driving), `vehiclesize`, `gyro_is_hd`, `gsensor` (3-axis, g),
`gyro` (3-axis, deg/s), `gps_speed` (km/h).

## Commands

```bash
# compile check of the detector code alone
gcc -std=c99 -Wall -Wextra -c src/crash_detect.c -o /dev/null

# once: convert parquet to the binary the C harness reads (~1 min, ~890 MB)
.venv/bin/python3 bench/export_events.py

# build the harness, run it on both shards, regenerate results.md
.venv/bin/python3 bench/run_bench.py

# only re-run the Python analysis on the cached peaks in build/
# (use this after changing a sweep or a table in run_bench.py)
.venv/bin/python3 bench/run_bench.py --no-build
```

`results.md` is reproducible: on the same data every accuracy number comes
out identical. Only the ns/sample timing table changes between runs and
machines.

For quick exploration of the raw data, `pandas` is installed:

```bash
.venv/bin/python3 -c "
import pandas as pd
df = pd.read_parquet('data/vzcrash/test-00000-of-00002.parquet')
print(df['label'].value_counts())
"
```

## Commit convention

One commit per block of code; the message says what that block does, e.g.
`Test 3: complementary-filter tilt estimator`. The history then reads as a
walkthrough of how the feature was built — `git log --oneline -- .` in this
folder.
