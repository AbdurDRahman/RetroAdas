# Feature 3 — Crash + Pothole Detection Algorithm (v2)

This is the design the firmware implements (`src/engine.c`). It replaces the
"Layer 1 / Layer 2" sketch in `info.md` with one pipeline that runs every
IMU sample on the ESP32 and produces three kinds of event: **CRASH**,
**ROLLOVER** and **POTHOLE** (plus a non-reported SPEED_BUMP class used to
reject bumps). Every constant here is justified either by physics, by the
cited literature, or by the benchmark in `evaluation.md` — the file says
which.

Hardware assumed: **ESP32 DevKit + MPU-6050** (accelerometer ±16 g,
gyroscope ±2000 °/s), rigidly fixed to the dashboard/body, sampled at
**100 Hz** with the MPU-6050's 44 Hz digital low-pass filter. 100 Hz was
chosen (over the 52 Hz in `info.md`) because every dataset used for
calibration is 100 Hz, so thresholds transfer with no conversion.

---

## 1. Why a crash is separable from everything else (the physics)

A road car can only decelerate through its tyres. On dry asphalt the
friction limit is about **1.0–1.2 g**; emergency braking therefore produces a
*long* pulse (0.5–3 s) whose magnitude never exceeds ~1.2 g. A pothole or
speed bump produces a *short* pulse (50–300 ms) that is mostly **vertical**
and whose net velocity change is ~0 (the body goes down and comes back up).
A collision produces a *short* pulse (30–150 ms) that is mostly
**horizontal**, well above the friction limit (≥ 2 g at the body for
anything beyond a parking tap), and with a **net velocity change** (ΔV) in
the direction of the impact — ΔV is the quantity airbag controllers and
crash-severity research use, because it integrates out single-sample noise.

So the three signatures separate on three axes:

| | duration | direction | sustained magnitude | net ΔV |
|---|---|---|---|---|
| Hard braking / near-miss | long | horizontal | ≤ 1.2 g | large but slow |
| Pothole / speed bump | short | vertical | up to 2–3 g at the dashboard | ≈ 0 |
| Collision | short | horizontal (any heading) | ≥ 2 g sustained 50 ms | > 0, fast |
| Rollover | long | gravity vector rotates | ~1 g | — |

The residual overlap (known from `results.md`): ~10–20 % of VZCrash's
labelled crashes are low-severity events whose body acceleration never
leaves the braking range. They are physically indistinguishable from hard
braking with a body-mounted IMU, and they are also the crashes where the
occupant is least likely to need automatic help. The accuracy target is
therefore **tiered by severity** (section 6).

---

## 2. Per-sample pipeline (100 Hz)

```
raw accel a (g), gyro ω (°/s)
   │
   ├─ 0. plausibility clamp ─ reject |a|>20 g, |ω|>2500 °/s, NaN, stuck sensor
   │
   ├─ 1. gravity tracker ──── g_est = LPF(a, τ = 1 s), frozen while |‖a‖−1| > 0.5 g
   │       ĝ = g_est/‖g_est‖
   │       a_lin = a − g_est                 linear acceleration (vector)
   │       a_v   = a_lin · ĝ                 vertical linear accel (+ up)
   │       a_h   = ‖a_lin − a_v ĝ‖           horizontal linear accel magnitude
   │       ω_h   = ‖ω − (ω·ĝ) ĝ‖             pitch/roll rate (yaw excluded)
   │
   ├─ 2. crash branch ──────── pre-trigger ‖a‖ ≥ 1.8 g opens a 500 ms capture
   │       features over the capture → linear score → tier → CRASH event
   │
   ├─ 3. rollover branch ───── complementary-filter tilt (α = 0.99) relative to
   │       mounting baseline, ≥ 60° sustained 1.0 s → ROLLOVER event
   │
   └─ 4. road branch ───────── |a_v| ≥ 0.30 g opens a 600 ms capture
           drop-then-impact + short + quiet-before → POTHOLE event
           lift-then-drop + longer → SPEED_BUMP (not reported)
```

Everything is O(1) per sample, `float` only, no heap, state in one struct.

### 2.0 Plausibility clamp

`results.md` found single-sample glitches > 20 g and > 10 000 °/s in normal
driving. A sample is rejected (previous valid sample is reused, a fault
counter incremented) when any axis is NaN, |a_axis| > 20 g, or |ω_axis| >
2500 °/s. If 100 consecutive samples are bit-identical the sensor is
flagged stuck (`fault` bit) — a real MPU-6050 at 100 Hz never repeats 1 s
of identical readings.

### 2.1 Gravity tracker

The sensor's mounting orientation is unknown, so "vertical" has to be
estimated. A first-order low-pass of the accelerometer vector (τ = 1 s)
converges to gravity while the car is driving normally. The update is
**frozen** whenever ‖a‖ is more than 0.5 g from 1 g, so an impact or a
pothole does not drag the estimate. Seeded with the first valid sample.
This is the same idea as the complementary filter's accelerometer path,
done on the vector instead of on angles, so no trigonometry is needed in
the hot path.

### 2.2 Crash branch

**Pre-trigger.** ‖a‖ ≥ 1.8 g on any sample. Cheap, and from `results.md`
Test 2 it is below the 1.5–2 g region where 96–99 % of labelled crashes
already fire, so almost nothing is lost here. Below 1.8 g nothing is
computed, which is what keeps the false-alarm rate on ordinary driving at
zero by construction — ordinary driving (`PVS`, 3.6 h) never reaches it.

**Capture.** For 500 ms after the pre-trigger the engine accumulates:

| feature | meaning | why |
|---|---|---|
| `peak_a` | max ‖a‖ | the single best rule in `results.md` |
| `sus50_h` | max over the window of the 50 ms moving mean of a_h | "sustained horizontal g" — rejects 1-sample spikes and vertical pothole hits; a value above the friction limit means contact with something |
| `dv100` | max over the window of ‖Σ a_lin·dt‖ over 100 ms (m/s) | windowed ΔV, the airbag-controller quantity |
| `dv300` | same over 300 ms | separates a crash pulse from braking (braking keeps integrating, a crash pulse stops) |
| `vratio` | max |a_v| / max a_h | vertical-dominated → pothole, horizontal → crash |
| `peak_wh` | max ω_h | spin/pitch after impact |
| `tilt90` | peak relative tilt from the α = 0.90 filter | `results.md` Test 3b showed it adds 12–17 points of recall as a *weighted* feature |

**Score.** `s = b + Σ w_i · log1p(f_i)`. The log makes each feature's
contribution proportional to its *relative* size, which is what the
threshold sweeps in `results.md` showed matters. Weights come from a
logistic regression fitted on the VZCrash **train** split and scored on the
**test** split (`eval/fit_model.py` writes `src/crash_model.h`). A fitted
linear score is still a rule: 8 multiplies and adds, fully inspectable, and
it is what the Chalmers smartphone study and VZCrash's own baseline
comparison found closes most of the gap between a single threshold (~10
FP/h) and a learned model.

**Physics overrides.** Independently of the score, a CRASH fires when
either holds:

1. `sus50_h ≥ 2.5 g` — 2.5 g sustained for 50 ms horizontally is more than
   twice the tyre friction limit: no braking manoeuvre can produce it, and a
   pothole produces it vertically, not horizontally.
2. `peak_a ≥ 4.0 g` **and** `sus50_h ≥ 1.2 g` — a very large impact whose
   horizontal part is still above what tyres can deliver. This exists for
   crashes with a big vertical component (kerb strikes, ditches, under-rides):
   in the VZCrash test split 7 of 2,555 severe crashes had 4–5 g peaks with
   only 1.3–2.2 g sustained horizontal, and the score alone missed them.
   Adding the rule costs 53 extra harsh-event negatives (FPR 2.18 → 2.45 %,
   ≈ one extra false alarm per 1,000 driving hours).

Neither rule ever fired on 4.7 h of real-road data: the worst real-road
capture was 3.54 g peak / 1.45 g sustained horizontal (PVS, cobblestone).
Together they guarantee the **severe tier** is caught even if the fitted
weights are wrong.

**Decision latency.** 500 ms after the pre-trigger. The alarm then runs a
15 s cancel countdown, so this delay is irrelevant to the user.

### 2.3 Rollover branch

Unchanged from `results.md`'s recommendation: `tilt_filter_update` with
α = 0.99 (τ ≈ 1 s at 100 Hz), relative to a mounting baseline latched after
2 s of quiet at boot, `≥ 60°` held for ≥ 1.0 s. That split cannot measure
rollover recall (there are no labelled rollovers) but it can measure the
false-alarm rate: 0.3 % of events at the 60°/α = 0.99 point, and 0 on the
continuous PVS drives. A car resting on its side or roof also shows
`a·ĝ₀ < 0.3 g` for seconds, which the same branch catches.

### 2.4 Road branch (pothole vs speed bump vs rough road)

Runs only when the crash branch is idle. On a_v (vertical linear
acceleration, + up):

- **Open** a 600 ms capture when |a_v| ≥ 0.30 g. Mednis et al.'s Z-THRESH
  optimum was 0.4 g for a phone on a dashboard; the sweep in
  `eval/sweep_road.py` on PVS showed 0.30 g is where the branch sees
  92–94 % of labelled speed bumps (vs ~50 % at 0.45 g) while still
  misclassifying none of them as potholes.
- **Quiet-before gate.** The 500 ms before the trigger must have
  std(a_v) < 0.15 g. Cobblestone/dirt roads keep a_v oscillating
  continuously; a pothole on an otherwise smooth road does not. This is
  the single biggest false-alarm reducer for the pothole class.
- **Shape.** Within the capture record `min_av` (drop), `max_av` (impact),
  the time of each, and the width (time between first and last crossing of
  ±0.2 g).
  - POTHOLE: `min_av ≤ −0.35 g` **and** `max_av ≥ 0.40 g` **and** the drop
    precedes the impact by ≤ 250 ms **and** width ≤ 400 ms. This is the
    "free-fall then hit the far edge" signature (Mednis G-ZERO + Z-THRESH
    combined). Severity = `max_av − min_av`. 0.40 g is the middle of the
    swept range: 0.30 g reports ~2.5 potholes/h on PVS asphalt that the
    labels call "ok", 0.45 g reports 0.4/h but drops bump sensitivity to
    69 %. PVS does not label individual potholes, so the "ok" reports are
    an upper bound on false reports, not a measured false rate — see
    `own_data.md` for how to get an event-level ground truth.
  - SPEED_BUMP: impact precedes drop (the wheel is lifted first), or
    width > 400 ms. Not reported.
  - Anything else (e.g. a single positive bump, a kerb strike) is ignored.
- **Rate limit.** One POTHOLE event per 2 s (the Jetson/cloud deduplicates
  on position anyway).

The event is reported to the Jetson, which attaches GPS and posts
`event_tag: "pothole"` to `adas-cloud` (Feature 2). It never sounds the
alarm.

---

## 3. State machine (`engine_state_t`)

```
 IDLE ──crash score ≥ θ or physics override──▶ ALARM (buzzer, t=0)
   ▲                                              │
   │                       cancel button / $CANCEL│          15 s elapsed
   │                                              ▼                 ▼
   └──── 60 s lockout ◀──────────── CANCELLED           CONFIRMED ──▶ $EVT to Jetson
```

- ROLLOVER enters ALARM the same way (its own event type).
- POTHOLE never changes state; it is emitted as a one-shot event.
- During ALARM/lockout no new crash events are raised (a crash is one
  event, not one per sample), but the engine keeps updating features so
  the strongest values are reported.
- GPS speed (if the Jetson is awake and sending `$SPD`) is **not** a gate.
  `results.md` Test 4 showed a speed drop cannot reject near-misses (99 %
  of them also drop). It is attached to the event for the cloud and for
  the log.

---

## 4. Interfaces

- `engine_init(&e, cfg)`; `engine_update(&e, &sample, t_ms) → event code`.
- Caller (firmware or harness) owns the struct. No globals, no heap.
- `engine_batch(...)` runs an array of samples in one call; the laptop
  harness uses it through `ctypes` so the **exact bytes** that run on the
  ESP32 are what gets benchmarked.
- Jetson link: NMEA-style ASCII lines over UART, `protocol.md`.

---

## 5. Compute cost

Per sample: ~40 float multiply-adds, one `sqrtf` for ‖a‖, two for a_h/ω_h,
two `atan2f` only in the rollover filter. Measured on the host with the
same harness as `results.md` (see `evaluation.md`); on a 240 MHz ESP32 with
single-precision FPU this is well under 50 µs against a 10 000 µs sample
period.

---

## 6. Accuracy target (agreed 2026-11-16)

Tiered by severity, reported honestly for all tiers:

| tier | definition (VZCrash label = crash) | target |
|---|---|---|
| Severe | `sus50_h ≥ 2.5 g` or `peak_a ≥ 4 g` | **100 % recall** (guaranteed by the physics overrides) |
| Moderate | `peak_a` 3–4 g | ≥ 95 % |
| Minor | `peak_a` < 3 g | reported, no target — overlaps hard braking |
| False alarms | continuous driving (PVS) | **≤ 1 per hour**, expected 0 |
| False alarms | VZCrash harsh-event negatives | converted to per-hour via the paper's real-world rate (section 7 of `evaluation.md`) |

Potholes: precision and recall against the annotated intervals in
RoadSens-4M, false reports per hour on PVS asphalt.

---

## 7. References

- Bianconcini et al., *VZCrash: A Large-Scale IMU Dataset of Ego-Vehicle
  Crashes*, IEEE ITSC 2026, arXiv:2606.06074 — dataset, trigger
  description, real-world 735 k harsh-event stream (143 crashes).
- Mednis et al., *Real Time Pothole Detection Using Android Smartphones
  with Accelerometers*, DCOSS 2011 — Z-THRESH / Z-DIFF / STDEV(Z) / G-ZERO
  and their tuned thresholds (0.4 g / 0.2 g / 0.2 g, 20 samples / 0.8 g).
- Pranoto et al., *Real-Time Vehicle Emergency Braking Detection with
  Moving Average Method…*, Vehicles 2025 — "high accel + high gyro =
  pothole/bump, high accel + low gyro = braking"; ESP32 feasibility.
- Ibrahim, Aly & Far, *A System for Vehicle Collision and Rollover
  Detection*, CCECE 2016 — complementary filter, MPU-6050, ±8 g.
- Chikaka & Longe, RTSI 2021 — 70–80° rollover tilt, physical cancel switch.
- Chalmers (Lindqvist & al.), *Development of a Smartphone-based Crash
  Notification System* — threshold detector ≈ 10 FP/h at 80 % sensitivity;
  classifier "vastly better".
- Menegazzo & von Wangenheim, *PVS — Passive Vehicular Sensors Datasets*
  (Kaggle / UFSC, CC BY-NC-ND 4.0) — 9 drives, MPU-9250 dashboard, 100 Hz.
- Khandakar et al., *RoadSens-4M*, 2025, Figshare
  doi:10.6084/m9.figshare.30341143 (CC BY 4.0) — 103 sessions, 100 Hz,
  annotated bump/pothole intervals.
- Airbag ΔV logic: US 6,236,308 and related — windowed ΔV thresholds; SAE
  07-12-02-0012 — side-airbag deployment lower limit 3–5 g.
