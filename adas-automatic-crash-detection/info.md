# Feature 3: Automatic Crash/Accident Detection — ESP32 Feasibility & Algorithm Notes

> Working notes compiled from research/discussion for RetroADAS Feature 3
> (Automatic Crash/Accident Detection). Supplements `feature-3-crash-detection.md`.

## Two-Layer Detection Model

The feature is split into two distinct, independently-justified layers — don't
conflate them:

1. **Pre-crash / near-miss layer — Emergency braking detection**
   Detects hard braking *before* anything has necessarily gone wrong, using
   accelerometer + gyroscope magnitude fusion and a moving average.
2. **Post-impact layer — Actual crash/rollover detection**
   Detects the collision/rollover event itself, using G-force magnitude
   thresholds and a fused tilt angle from a complementary filter.

These are genuinely different physical events with different math — the cited
literature reflects that split (see below).

---

## Layer 1: Emergency Braking Detection (Moving Average Method)

**Primary reference:** Pranoto et al. (2025), *"Real-Time Vehicle Emergency
Braking Detection with Moving Average Method Based on Accelerometer and
Gyroscope Data,"* Vehicles (MDPI), DOI: 10.3390/vehicles7040106.

### Hardware used in the paper
- Data acquisition: **Orange Pi 5 SBC** + six-axis IMU (**ASM330LHH**)
- Feasibility/benchmark test only: **ESP32 (WROOM-32)**, using the
  `RunningAverage` library (`getFastAverage`)
- (Arduino Uno appears only in the paper's *related-works* table, referring to
  a different, simpler threshold-based system — not this paper's own setup.)

### Sampling
- IMU output data rate: **52 Hz** (a setting inside the IMU chip itself,
  independent of bus speed)
- I2C bus clock: **400 kHz** (just the data-transfer speed — not a
  "downsampled from 400kHz to 52Hz" relationship; two separate parameters)
- A ~2-second emergency-braking pulse ≈ ~104 samples at 52 Hz — enough
  resolution to resolve rise/peak/decay shape

### Algorithm
1. **Magnitude fusion** (orientation-independent):
   - `A = √(Ax² + Ay² + Az²)` (accelerometer magnitude)
   - `G = √(Ωpitch² + Ωroll² + Ωyaw²)` (gyroscope magnitude)
2. **Moving average** (sliding window, NOT cumulative average):
   - `AA[n] = (1/k) × Σ (i = n-k+1 to n) A[i]`
   - Cumulative average (averaging *all* points since start) would dilute a
     short pulse into insignificance — moving average stays responsive
     because it only looks back a fixed *k* samples.
3. **Optimal window length k = 30 samples** (~0.58s at 52Hz) — found
   empirically by sweeping k and maximizing separation of emergency-braking
   points from a convex hull boundary drawn around all non-emergency events
   on a (peak accel, peak gyro) scatter plot.
4. **Key discriminator:** emergency braking = **high acceleration + LOW
   angular velocity** (straight-line deceleration); potholes/speed bumps =
   high acceleration + **HIGH angular velocity** (oscillation/vibration).
   Acceleration alone can't separate these — gyroscope is required.

### Dataset in the paper (single vehicle, single driver, flat roads)
| Event | Condition | # sequences (trials) |
|---|---|---|
| Engine idle | Parked | 2 |
| Regular braking | 50–60 km/h | 11 |
| Emergency braking | 10–40 km/h | 13 |
| Damaged road | 40–100cm wide, 7–15cm deep potholes, <20km/h | 16 |
| Speed bump | 8–15cm high, 30–50cm long, <20km/h | 16 |

- "Sequence" = one full trial/recording of an event (a few seconds long),
  **not** a single 52Hz data point. Each sequence itself contains ~100–200
  individual signal-level samples.
- Emergency braking pulse: short, ~2s, stop by ~15s. Regular braking: longer,
  ~5s pulse.

### Reported accuracy
- The paper does **not** report a single accuracy/precision/recall
  percentage in the accessible excerpts. Their validation is **geometric
  separability**: emergency-braking points fall clearly outside a convex-hull
  decision boundary built from all other event types on the (peak accel,
  peak gyro) plot; window length k was chosen to maximize that separation
  margin. Frame this to a panel as "validated via separability, not a stated
  accuracy %" if asked.

### ESP32 compute cost (measured in the paper)
- Moving average via `RunningAverage`/`getFastAverage`: **~533–536 µs per
  update**, flat regardless of window length (25–100 samples tested) —
  because a sliding-window average just adds the newest sample and drops the
  oldest, not a full re-sum.
- At 52 Hz, budget per sample = 1/52 ≈ **19,200 µs (19.2 ms)**.
- 535 µs ÷ 19,200 µs ≈ **2.8% of the time budget** — large headroom left for
  GPS polling, cancel button, buzzer, and Jetson communication.

---

## Layer 2: Actual Impact/Rollover Detection (Complementary Filter)

**Supporting references:**
- Ibrahim, Aly & Far (2016), *"A System for Vehicle Collision and Rollover
  Detection,"* IEEE CCECE 2016, DOI: 10.1109/CCECE.2016.7726720 — closest
  one-to-one match: MPU-6050 accel+gyro, complementary filter, detects both
  collision (acceleration spike) and rollover (tilt angle). Rollover
  threshold: vehicle roll/pitch angle exceeding **±46°**. Sensor registers:
  accelerometer at **±8g** range, gyroscope at **±500°/s** range. Validated
  against inclinometers at 100%/97% angle-calculation accuracy, and against
  225 simulated rollover trials.
- Razali et al. (2021), IEEE ICRAIE 2021, DOI: 10.1109/ICRAIE52900.2021.9703970
  — G-force severity bands: **mild 5–20 G, moderate 20–40 G, severe >40 G**
  (<5 G = non-accident).
- Chikaka & Longe (2021), IEEE RTSI 2021, DOI: 10.1109/RTSI50628.2021.9597212
  — rollover tilt-angle thresholds: **80° (X-axis), 70° (Y-axis)**, vibration
  >4000 mV/G, using an MPU-6050. Includes a physical reset/cancel switch for
  minor crashes (precedent for this project's False Alarm Switch), and a
  ~4s turnaround from confirmed crash to notification dispatch.

### Math

**1. Accelerometer-derived tilt angle** (accurate at rest, noisy under
linear acceleration):

```
θ_accel = arctan( ay / √(ax² + az²) ) × 180/π      (pitch)
φ_accel = arctan( -ax / √(ay² + az²) ) × 180/π     (roll)
```

**2. Gyroscope-derived angle** (smooth short-term, drifts long-term via
integration):

```
θ_gyro[n] = θ[n-1] + ωx[n] × Δt        (Δt ≈ 1/52 s)
```

**3. Complementary filter** (fuses both into one stable estimate):

```
θ[n] = α × (θ[n-1] + ωx[n]·Δt) + (1-α) × θ_accel[n]
```
where α ≈ 0.9–0.98 (trust gyro short-term, accel long-term).

**4. G-force magnitude:**

```
‖A‖g = √(ax² + ay² + az²) / g     (g = 9.81 m/s²)
```

**5. Threshold decision logic:**

```
Event =
  No accident   if ‖A‖g < 5
  Mild          if 5 ≤ ‖A‖g < 20
  Moderate      if 20 ≤ ‖A‖g < 40
  Severe        if ‖A‖g ≥ 40
  OR: |θ[n]| > θ_rollover   (e.g. ~70–80° per cited literature)
```
Flagged as crash/rollover if *either* the G-force band or the tilt-angle
threshold trips — whichever fires first.

**6. Alternative/complementary collision rule (Ibrahim et al.):** instead of
(or alongside) the G-force magnitude bands above, compare the delta between
two consecutive raw accelerometer readings against a threshold, combined
with a simultaneous GPS speed drop:

```
Collision = (|A[n] - A[n-1]| > threshold) AND (GPS speed dropping)
```

Both conditions must hold. Threshold is register-unit-dependent (the source
paper used a raw-register example of ~2000 at ±8g accelerometer range, not
a G-force value) — needs recalibrating against whatever ADC/register range
this project's IMU is configured to.

### ESP32 feasibility (Layer 2)
- Complementary filter cost: one `atan2` call, one integration step, one
  weighted sum per sample — **no matrices, no iterative solving**. One of the
  lightest sensor-fusion techniques that exists; standard on cheap MCUs.
- **Why the ESP32 handles this fast:** its CPU core is a **Xtensa LX6**
  (licensed from Cadence/Tensilica) with a **hardware floating-point unit
  (FPU)** — `atan2`, `sqrt`, trig run as native instructions, not
  software-emulated. Xtensa isn't a separate/added component — it's simply
  the processor architecture already inside the ESP32 chip.
- Estimated cost: comfortably **under 100 µs/sample** (reasoned estimate
  based on typical FPU trig cost — **not** a number directly measured/reported
  by any cited paper; be upfront about this distinction if asked).
- Combined budget check: Layer 1 (~535 µs) + Layer 2 (<100 µs, estimated)
  together still use **well under 5%** of the 19,200 µs/sample budget at
  52 Hz — large headroom remains for GPS, cancel button, buzzer, Jetson comms.

---


**The resolution — two separate literatures, not one paper stretched thin:**
1. Braking/near-miss layer/pothole → Pranoto et al. (empirically validated on real,
   safe driving data).
2. Impact/rollover layer → Ibrahim et al. / Razali et al. / Chikaka & Longe
   (established G-force/tilt-angle thresholds derived from prior
   standardized crash-test literature, not from live crash experiments).


**Datasets identified 
 **VZCrash** (Verizon Connect, 2026) — Hugging Face
  (huggingface.co/datasets/vzc-research-chapter/VZCrash), ~190,000 events
  incl. 31,090 verified crashes, 100 Hz tri-axial accel (g) + gyro (deg/s).
  Access-gated (must accept terms). Companion paper: arXiv:2606.06074
  (accepted IEEE ITSC 2026).


---

## Architecture Fit (per diagram)

- Both layers run on the **independent onboard Controller (ESP32-class)**
  shown in the diagram — decoupled from Jetson state, so crash detection
  still works during Sentry Mode or a Jetson reboot cycle.
- Controller inputs: Gyro/IMU sensor, False Alarm Switch, Buzzer output.
- On confirmed event → signal to Jetson → GPS location attached → pushed to
  cloud (`event_tag: "accident"`) → visible to nearby RetroADAS vehicles
  (integration point with Feature 2, still needs confirmation per original
  doc's open questions).

- Emergency contact mechanism: sends signal to the bigger device jetson ( that uploads it onto cloud , also contacts emergency can be a dummy function or some simple api call)

- False-positive rate target — not yet quantified; to be defined once
  real calibration data is collected.
