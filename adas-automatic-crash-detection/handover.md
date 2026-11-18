# Handover — Feature 3: Automatic Crash Detection

This document hands Feature 3 over to the next engineer. Setup and commands
are in [`README.md`](README.md).

## v2 (16 Nov 2026) — where things stand now

Everything below the line is the **v1 handover** from the first engineer and
is kept because its findings still hold (the literature G thresholds don't
work on car IMUs; ~10–20 % of labelled crashes look like hard braking; speed
is a confirmation, not a gate). v2 built on it:

| | v1 | v2 |
|---|---|---|
| Design | three separate primitives, no decision rule | one engine (`src/engine.c`): plausibility clamp → gravity tracker → crash branch (physics override + fitted score) → rollover → pothole/speed-bump → alarm state machine. [`algorithm.md`](algorithm.md) |
| Crash rule | peak ‖A‖ threshold ≈ 3 G | sustained-horizontal ≥ 2.5 g override (tyre friction limit argument) **or** a logistic score over 8 window features fitted on the VZCrash *train* split |
| Potholes | not in scope | recognised from the vertical drop-then-impact signature, reported to the Jetson as a `pothole` hazard, never an alarm |
| Benchmark | event-level recall/FPR on VZCrash test | same, by severity tier, **plus** false alarms per hour on 3.0 h of real driving (PVS) and a conversion of the VZCrash FPR to per-hour using the paper's fleet rate. [`evaluation.md`](evaluation.md) |
| Firmware | none | `firmware/retroadas_crash/` — MPU-6050 register driver, 100 Hz, buzzer, cancel button, UART protocol, 3 s raw log, `$SIM` bench mode. **Not yet run on a board.** |
| Jetson side | none | `jetson/crash_link.py` + [`protocol.md`](protocol.md); demonstrated on the laptop with `eval/replay.py` |
| Sample rate | 52 Hz | **100 Hz** (every calibration dataset is 100 Hz; MPU-6050 does it trivially) |

Reading order for v2 (about an hour): this section → `algorithm.md` →
`evaluation.md` → `src/engine.h` → `protocol.md` → `git log --oneline -- .`.

### Results at a glance (16 Nov 2026, full numbers in `evaluation.md`)

| what | result |
|---|---|
| Severe crashes (VZCrash test, n = 2,555) | **100.0 %** detected — guaranteed by the two physics overrides |
| Moderate crashes (3–4 g peak, n = 769) | 79.3 % |
| Minor crashes (1.8–3 g, n = 817) | 31.8 % — these overlap hard braking on any body-mounted IMU |
| All labelled crashes (n = 4,221) | 81.1 % |
| False alarms on 3.0 h of real driving, three cars, asphalt/cobblestone/dirt (PVS) | **0** (rule of three: < 1 per hour at 95 % confidence) |
| False alarms on 1.7 h of phone data on rough urban roads (RoadSens-4M) | 0 |
| False-positive rate on VZCrash's *harsh-event* negatives | 2.5 % → ≈ 1 false alarm per 100–300 driving hours using the paper's fleet rate |
| Road branch sees labelled speed bumps (PVS) | 82 %, none misreported as potholes |
| Compute | ~50–150 ns/sample on a laptop (varies with load); < 0.5 % of the 100 Hz budget even at 300× slower |

The whole recall/false-alarm trade-off is one threshold (`CRASH_MODEL_THRESHOLD`);
`evaluation.md` has the sweep table so the operating point can be moved
with the team's agreement (e.g. threshold 1.0 → 95 % moderate recall at
3.7 % harsh-event FPR).

### Decisions taken in v2 (with the team member who owns Feature 3)

- Hardware: ESP32 DevKit + MPU-6050 at ±16 g / ±2000 °/s, 100 Hz, DLPF 44 Hz.
- Accuracy target is **tiered**: 100 % on severe crashes (guaranteed by the
  override, which never fired in 4.7 h of real-road data — max sustained
  horizontal seen was 1.45 g), reported honestly for moderate and minor,
  ≤ 1 false alarm per driving hour. "Zero false negatives on every labelled
  crash" is not achievable with a body-mounted IMU and nobody claims it
  (Apple, Verizon's own models); the write-up says why.
- Potholes: report, don't alarm. Speed bumps: recognise, don't report.
- GPS speed comes from the Jetson over UART (`$SPD`), is attached to events,
  and is never a detection gate.

### What is still open

1. **Run the firmware on the real board.** Expect to touch only
   `retroadas_crash.ino` (pins, I2C address if AD0 is high). Verify the
   100 Hz rate with the `$HB` `n_samples` counter (should grow by ~100/s).
2. **Own-car calibration drive** ([`own_data.md`](own_data.md)): the only
   event-level pothole ground truth we can get; also re-checks the crash
   false-alarm rate on Pakistani roads with the real mount.
3. **Jetson → adas-cloud**: `crash_link.py` posts to `/v1/hazards` with a
   device id + API key once those exist; until then it logs what it would post.
4. **Mounting**: the unit must be rigid (double-sided tape to the dash is
   fine; a loose box is not). The RoadSens phone data shows what a loose
   mount does: peak rotation rates > 100 °/s on ordinary bumps.
5. If the fitted score is ever refitted, keep `fit_model.py`'s rule: fit on
   train, pick the threshold on the held-out fold, report on test. Never tune
   on the test split.

---

## v1 handover (original, kept for the findings)

Suggested reading order (about an hour):

1. This file.
2. [`info.md`](info.md) — the design and the maths behind it.
3. [`results.md`](results.md) → "Summary" section — what the benchmark found.
4. [`src/crash_detect.h`](src/crash_detect.h) — the detector API (short and
   heavily commented).
5. `git log --oneline -- .` in this folder — each commit is one step, in the
   order it was built.

---

## 1. What this feature is

When the car crashes or rolls over, the system should notice from the IMU,
sound a buzzer, give the driver 10–15 seconds to cancel a false alarm with a
physical button, and if not cancelled, report the accident.

### Decided architecture

- Runs on an **independent ESP32-class microcontroller**, not on the Jetson.
  Reason: it must keep working during Sentry Mode and while the Jetson
  reboots. So all detection code is embedded C, not a Python/Linux service.
- Controller I/O: IMU in, False Alarm (cancel) switch in, buzzer out, one
  signal out to the Jetson on a confirmed event.
- The ESP32 has **no network access** of its own. Reporting path:
  ESP32 confirms → signals the Jetson → Jetson attaches GPS → Jetson posts to
  `adas-cloud` (see section 6).
- Emergency contact (call/SMS) can be a **dummy function or a simple API
  call** for now. A cellular module is not required.

### Two detection layers (keep them separate)

They detect different physical events and use different maths:

| | Layer 1 — Emergency braking | Layer 2 — Impact / rollover |
|---|---|---|
| Detects | Hard braking before/without a crash (near-miss) | The collision or rollover itself |
| Method | Moving average of accel and gyro magnitude; braking = high accel + **low** rotation, potholes/bumps = high accel + **high** rotation | Peak G-force magnitude, and a complementary-filter tilt angle |
| Main paper | Pranoto et al. 2025 | Ibrahim et al. 2016, Razali et al. 2021, Chikaka & Longe 2021 |
| State | **Not written** | Written + benchmarked |

Full formulas and citations: `info.md`.

### Sample rates — two things not to mix up

- The design targets **52 Hz** IMU output (a setting inside the IMU chip).
  The I2C bus clock is 400 kHz; that is just transfer speed, unrelated.
- The VZCrash dataset is **100 Hz** IMU, **1 Hz** GPS. Any constant given as
  a *number of samples* must be converted between the two (e.g. Layer 1's
  window k = 30 samples at 52 Hz ≈ 0.58 s ≈ 58 samples at 100 Hz). The tilt
  filter's α also changes meaning with the rate — see `results.md` Test 3.

---

## 2. What is done

**Detector library** — `src/crash_detect.{h,c}`. Plain C99, `float` only (the
ESP32 FPU is single-precision; `double` is slow software emulation), no heap,
no globals, state in structs the caller owns, one `*_update()` call per IMU
sample. It should compile unchanged in Arduino or ESP-IDF.

| Function | Test | What it does |
|---|---|---|
| `gforce_band_update` | 2 | ‖A‖ = √(ax²+ay²+az²) in g, classified into none / mild / moderate / severe |
| `tilt_filter_init`, `tilt_filter_update` | 3 | Complementary filter for pitch and roll; latches a mounting baseline after N samples and returns tilt relative to it |
| `delta_speed_init`, `delta_speed_feed_speed`, `delta_speed_update` | 4 | Sample-to-sample jump in ‖A‖, gated on GPS speed dropping |

**Benchmark** — `bench/`. Converts the VZCrash test split (24,174 events:
4,221 crashes, 3,633 near-misses, 16,320 normal driving) to a binary file,
runs every detector over every event in C, then sweeps thresholds in Python
and writes `results.md` (crash recall vs. false-positive rate per setting,
plus speed per sample). Re-running it reproduces the same accuracy numbers.

---

## 3. Key findings (read before trusting any threshold)

From `results.md`. "FPR" = fraction of non-crash events that would set off
the alarm.

1. **The literature G-force bands do not work on this hardware class.**
   No crash in VZCrash goes above ~7.3 G (these telematics units are ±8 g
   sensors). The 20 G and 40 G bands never fire; the 5 G "mild" boundary
   catches only 28% of crashes. The usable threshold is **~2.5–3.5 G**:
   e.g. 3.0 G → 79% of crashes caught at 3.6% FPR.
2. **Peak ‖A‖g is the best single rule and also the cheapest** (~1.6 ns per
   sample on a laptop).
3. **Tilt as an OR-gate makes things worse**, but **tilt added as a weighted
   score helps**: `z(peak G) + 1.5·z(peak tilt, α=0.90)` catches 75% of
   crashes at 2% FPR versus 62% for G alone (Test 3b). Caveat: the weight
   was chosen on the same data it is scored on — treat it as evidence, not a
   production constant.
4. **Rollover is a separate concern.** The test split has few or no genuine
   rollovers, so rollover recall cannot be measured. Keep a slow
   (α ≈ 0.99), baseline-corrected, 70–80° tilt check as its own OR condition.
5. **GPS speed drop should confirm after the trigger, not gate it.** Use a
   ~2 s look-ahead during the cancel countdown. It cannot reject near-misses
   (99% of them also drop ≥ 10 km/h — that is what a near-miss is).
6. **About 10–20% of crashes peak below ~2.5–3 G** and look like hard
   braking to every rule tested. Getting those needs Layer 1's shape features or a
   learned model.
7. **Speed is not a concern.** All detectors together use a negligible
   fraction of the 19.2 ms available per sample at 52 Hz, even allowing for
   the ESP32 being ~1000× slower than the benchmark laptop.

---

## 4. Known problems in the current code

These are real gaps, not style notes. Fix or consciously accept each one.

1. **`delta_speed_update` uses the wrong speed gate.** The C code checks
   whether the *last* GPS fix was lower than the one before (same-second
   gate). The benchmark shows this *loses* recall, because at 1 Hz the fix
   that arrives at impact time was taken before the impact. The better shape
   (+2 s look-ahead) exists only in the Python analysis
   (`gated_peak_delta` in `bench/run_bench.py`). On the device, implement it
   as a confirmation step during the countdown, not inside the per-sample
   detector.
2. **`GFORCE_CFG_LITERATURE` (5 / 20 / 40 G) is still the only preset** in
   `crash_detect.h`. On a ±8 g IMU it never reports moderate or severe. A
   calibrated preset (around 3 G for detection) needs adding; keep the
   literature bands only as severity labels if a higher-range sensor is
   fitted.
3. **No plausibility clamp.** A few normal-driving events contain sensor
   glitches (single samples > 20 G, gyro > 10,000 deg/s). A real device needs
   to reject impossible samples before they reach any detector.
4. **The Test 3b weighted score is not in C.** It exists only as analysis in
   `run_bench.py` (`section_test3b`). Its z-scores use the median / IQR of
   the dataset; the device needs fixed constants, which need choosing.
5. **Delta threshold depends on sample rate.** Δ‖A‖ was measured at 100 Hz;
   at 52 Hz an impact spans fewer samples, so jumps are bigger. Re-sweep on
   the real device.
6. **Tilt filter `n` counter** saturates at 65,535 samples — harmless (it is
   only used for calibration) but worth knowing.

---

## 5. What is left to do (suggested order)

1. **Test 1 — Layer 1 emergency-braking separability.** Plan in `stats.md`.
   Add a moving-average detector to `src/crash_detect.c` (a ring buffer of k
   floats in a caller-owned struct; no heap), export its per-event peaks from
   `bench/bench.c`, add a section to `run_bench.py`. Check whether near-miss
   and normal-driving events separate on a (peak averaged accel, peak
   averaged gyro) scatter, and sweep k ≈ 20–100 samples at 100 Hz.
2. **Fix the problems in section 4** (at least 1–3) and regenerate
   `results.md`.
3. **Pick the final decision rule** and a target false-positive rate (still
   undecided — no number has been agreed). Present the trade-off from the
   `results.md` Summary table to the group.
4. **ESP32 firmware loop** — none written yet:
   - IMU driver at 52 Hz output rate, ±8 g or higher accel range.
   - Per sample: plausibility clamp → detectors → decision.
   - On trigger: buzzer on, start 10–15 s countdown, watch the cancel
     button, check GPS speed drop as confirmation.
   - If not cancelled: signal the Jetson (protocol not decided — UART or a
     GPIO line are the obvious options).
   - Log the event locally (timestamp, sensor window, GPS) — required by the
     feature spec.
5. **On-device timing.** The benchmark times are from a laptop; measure the
   real ns/sample on the ESP32. The <100 µs figure for the tilt filter in
   `info.md` is a reasoned estimate, not a measurement.
6. **Jetson side** — receive the signal, attach GPS, post to `adas-cloud`
   (section 6), call the emergency-contact stub.

---

## 6. Integration with `adas-cloud` (unconfirmed — check before building)

The feature spec marks "auto-post a confirmed crash as a hazard" as **needs
confirmation**. Confirm with the group before wiring it up. If agreed:

- The **Jetson** (not the ESP32) calls `POST /v1/hazards` with
  `event_tag: "accident"`. Handler logic:
  `../adas-cloud/internal/hazards/deduplication.go`.
- Row shape (PostGIS `location`, `confidence`, `reported_by` = this device's
  `device_id`, …): `../adas-cloud/database/migrations/002_create_hazards_table.sql`.
- Authentication: the same `device_id` + API key flow the perception stack
  uses — `../adas-cloud/internal/devices/authentication.go`.
- Nearby RetroADAS vehicles then see the accident through Feature 2
  (crowdsourced hazards).

---

## 7. Open questions

| Question | Status |
|---|---|
| Run on ESP32 or Jetson? | **Decided:** separate ESP32. |
| Works during Sentry Mode? | **Decided:** yes, because it is off the Jetson. |
| How are emergency contacts reached? | **Decided for now:** via the Jetson; a stub/simple API call is fine. |
| Target false-positive rate | **Open.** Needs a group decision using `results.md`. |
| Thresholds calibrated on our own hardware | **Open.** Current numbers come from VZCrash, a different IMU at 100 Hz. Ideally record our own data (hard braking, potholes, speed bumps) with the actual sensor. |
| Auto-post crashes to the hazards table (Feature 2)? | **Open.** Needs confirmation. |
| ESP32 → Jetson signalling protocol | **Open.** Not discussed yet. |

---

## 8. Data and licence notes

- VZCrash is **CC-BY-NC-4.0** (non-commercial). Fine for the FYP; note it
  in the report. Cite the companion paper, arXiv:2606.06074 (IEEE ITSC 2026).
- The dataset is **not in git** (`data/` is ignored, ~1.8 GB). Download
  steps are in `README.md`. Only the test split was used; the train and
  validation splits (~190k events in total) are available if more data is
  needed — e.g. to fit the Test 3b weight on train and score on test, which
  would fix the caveat in finding 3.
- Shard `test-00001` has **no crashes**. Use `test-00000` whenever you need
  crash examples.
