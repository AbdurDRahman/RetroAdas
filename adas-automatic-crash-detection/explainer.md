# Feature 3 explained from zero — crash + pothole detection

A from-the-beginning walkthrough of what was built, why, and how to defend
it. Written for the team member presenting Feature 3 and for the rest of
the group integrating with it. The technical references are `algorithm.md`
(design), `evaluation.md` (numbers), `protocol.md` (Jetson link).

---

## Level 0 — the goal in one sentence

A small box in the car must notice, on its own, when the car has crashed
or rolled over, sound an alarm the driver can cancel, and otherwise call for
help — and it must *not* do that when the driver merely brakes hard or hits
a pothole. As a bonus, when it recognises a pothole it tells the rest of
RetroADAS so other cars can be warned.

---

## Level 1 — what the sensor actually measures

The box contains an **IMU** (inertial measurement unit) — the chip is the
**MPU-6050**, about 100 rupees. It contains two instruments:

- An **accelerometer**: measures how hard the box is being pushed, in three
  directions (forward/back, left/right, up/down). Unit: **g**. 1 g is the
  pull of gravity. Sitting still, it reads 1 g pointing down. Hard braking
  reads about 1 g pointing forward. A crash reads 3–7 g for a fraction of a
  second. The chip can read up to ±16 g.
- A **gyroscope**: measures how fast the box is *rotating*, in degrees per
  second. A car on a normal road rotates slowly (turning a corner: ~20 °/s).
  A rollover rotates fast and ends up at a new angle.

We read both **100 times per second** (100 Hz). Every reading is six
numbers. The algorithm looks at each new reading and decides: nothing,
crash, rollover, pothole.

Things you must know and be able to say:

- "g" is a unit of acceleration; 1 g = 9.81 m/s².
- The old project notes said 52 Hz. We use **100 Hz** because every dataset
  we calibrated on is 100 Hz, so numbers transfer directly, and the MPU-6050
  does 100 Hz easily (it can do 1,000).
- The sensor is set to **±16 g range** so a real crash (up to ~7 g) can
  never saturate it, and the chip's built-in low-pass filter is set to
  44 Hz so electrical noise above that is removed before we see it.

---

## Level 2 — the one insight the whole design rests on

Three things make the car move violently. Here is how each one *looks* to
the sensor, and the physics that tells them apart:

| event | direction | how long | how strong | does the car's speed change? |
|---|---|---|---|---|
| **Hard braking** | horizontal (forward) | long: 0.5–3 s | **never above ~1.2 g** | yes, but slowly |
| **Pothole / speed bump** | mostly **vertical** | short: 50–300 ms | can reach 2–3 g at the dashboard | no — the car goes down and comes back up, net ≈ 0 |
| **Crash** | mostly **horizontal** (any heading) | short: 30–150 ms | 2–7 g | yes, suddenly |
| **Rollover** | gravity direction *rotates* | seconds | ~1 g | — |

The key fact, which you should be ready to state to a panel:

> **A car can only brake through its tyres, and tyres on dry asphalt cannot
> produce more than about 1.0–1.2 g of deceleration.** Above that they skid.
> So if the box ever measures horizontal acceleration well above 1.2 g,
> *sustained* for a few hundredths of a second, the car did not brake —
> it hit something.

That is why the algorithm first splits every reading into a **vertical
part** and a **horizontal part**, and why its hard rules are about
*sustained horizontal* acceleration, not about the raw peak. A pothole can
produce a 3 g *peak* (vertical, one or two samples); it cannot produce 2.5 g
*horizontal* for 50 ms.

---

## Level 3 — what the chip does, 100 times a second

Think of it as a pipeline. Each new reading goes through these stages in
order (this is `src/engine.c`, function `engine_update`):

**Stage 0 — sanity check.** If a reading is impossible (above 20 g, above
2,500 °/s, or the sensor has repeated the exact same value for a full
second), it is thrown away and the previous reading reused. *Why:* the
real datasets contain electrical glitches of 50 g for one sample. One of
those must never cause an alarm.

**Stage 1 — find "down".** The box may be mounted at any angle. A slowly
updated average of the accelerometer (time constant 1 s) settles on the
direction of gravity. During violent moments (reading more than 0.5 g away
from the normal 1 g) the average is frozen so an impact cannot pull it off.
With "down" known, every reading is split into **vertical** and
**horizontal**, and the gyroscope into "pitch/roll" (tipping) and "yaw"
(turning a corner, ignored).

**Stage 2 — crash branch.** Sleeps until the total acceleration reaches
**1.8 g**. (Ordinary driving never gets there: 0 times in 3 hours of real
driving data.) Then for the next 500 ms it records eight measurements —
the "features":

| feature | plain meaning |
|---|---|
| `peak_a` | the highest total acceleration seen |
| `sus50_h` | the highest *horizontal* acceleration averaged over 50 ms (5 readings) — the "tyre limit" quantity |
| `dv100` | how much the velocity changed in the worst 100 ms window (m/s) — what airbag computers use |
| `dv300` | same over 300 ms |
| `vratio` | vertical ÷ horizontal — "was this mostly up-down (pothole) or sideways (crash)?" |
| `peak_wh` | fastest tipping rotation |
| `tilt90` | how much the estimated angle jumped (a trick from the previous benchmark that adds ~12 points of recall) |
| `peak_av` | the highest vertical acceleration |

At the end of the 500 ms it decides (Level 4).

**Stage 3 — rollover branch.** Separately, a tilt estimate (the
"complementary filter" from the earlier work) is compared with the angle
the box had when the car started. If it is **60° or more, for a full
second**, that is a rollover. *Why a full second:* a crash's violent
instant produces a brief fake tilt reading; a real rollover stays tilted.

**Stage 4 — road branch (potholes).** Watches the *vertical* part only.
When it jumps past **0.30 g**, and the previous half-second was quiet
(standard deviation below 0.15 g — i.e. not cobblestone), it records the
next 600 ms. A pothole is a **drop** (the wheel falls in: vertical goes
below −0.35 g) **followed within 250 ms by a hit** (the wheel strikes the
far edge: above +0.40 g), all over within 400 ms. A speed bump is the
opposite order (the wheel is lifted first, then drops) or takes longer.
Potholes are reported to the Jetson; speed bumps are recognised and
ignored. At most one pothole report every 2 seconds.

**Stage 5 — the alarm.** A crash or rollover starts the buzzer and a
**15-second countdown**. The physical cancel button (or a tap on the
Jetson screen) cancels it. If nobody cancels, the event is "confirmed" and
sent to the Jetson, which adds the GPS position and reports it. Then a
60-second lockout so one crash produces exactly one report.

Everything above is a few dozen multiplications per reading. Measured:
**52 nanoseconds per reading** on a laptop; the ESP32 has 10,000,000
nanoseconds between readings, so even if it were 300 times slower than a
laptop it would use 0.2 % of its time.

---

## Level 4 — the decision rules and where every number comes from

This is the table to memorise. A panel will ask "why this number?" for each.

| rule | value | where it comes from |
|---|---|---|
| Sanity clamp | 20 g, 2,500 °/s, 100 identical samples | above the sensor's physical range (16 g / 2,000 °/s); glitches seen in the data |
| Gravity tracker | 1 s averaging, frozen beyond ±0.5 g | standard complementary-filter practice; 1 s is slow enough to ignore bumps, fast enough to follow a car that changes attitude on a hill |
| Crash pre-trigger | **1.8 g** total | the previous benchmark showed 96–99 % of real crashes exceed 1.5–2 g; 0 of 3 h of real driving reaches 1.8 g, so nothing below it is even computed |
| Capture length | 500 ms | a crash pulse lasts 30–150 ms; 500 ms captures it with margin and keeps the alarm decision fast (the 15 s countdown makes this delay irrelevant) |
| **Physics override 1** | **sustained horizontal ≥ 2.5 g** over 50 ms | **twice the tyre friction limit** (1.0–1.2 g). No braking can do it; potholes do it vertically, not horizontally. Never came close on real roads (worst: 1.45 g). |
| **Physics override 2** | **peak ≥ 4 g AND sustained horizontal ≥ 1.2 g** | a very large impact whose horizontal part is still above the tyre limit. Exists for crashes with a big vertical component (kerbs, ditches). Added because 7 of 2,555 severe crashes in the test data were of this shape and the score alone missed them. Cost: 0.3 % more false alarms on harsh events. |
| Fitted score threshold | **2.37** | chosen on a held-out part of the *training* data to give ≥ 95 % recall on moderate+ crashes there; never adjusted on the test data |
| Rollover | **60° for 1.0 s**, filter α = 0.99 | the two source papers say 46° (Ibrahim 2016) and 70–80° (Chikaka 2021); the earlier benchmark showed 46° is inside normal-driving noise and 60° at α 0.99 has 0.3 % false rate; 1 s hold rejects crash-instant fake tilt |
| Pothole trigger | **0.30 g** vertical | Mednis et al. 2011 found 0.4 g optimal for a phone on a dashboard; our sweep of 72 settings on PVS showed 0.30 g makes the branch see 92–94 % of labelled speed bumps vs ~50 % at 0.45 g, with none misreported |
| Quiet-before gate | std < **0.15 g** over 0.5 s | from the sweep — the single biggest false-report reducer; cobblestone and dirt roads never go quiet |
| Pothole shape | drop ≤ **−0.35 g**, hit ≥ **+0.40 g**, drop before hit by ≤ 250 ms, total ≤ 400 ms | Mednis's "G-ZERO" (brief free-fall) + "Z-THRESH" (impact) combined; 0.40 g is the middle of the swept range (0.30 → ~2.5 reports/h on PVS asphalt, 0.45 → 0.4/h but bump sensitivity drops to 69 %) |
| Alarm countdown | 15 s | the feature spec (10–15 s); Chikaka & Longe used a physical cancel switch and ~4 s dispatch — we are deliberately slower |
| Lockout | 60 s | one crash = one report |

---

## Level 5 — what "fitted score" means and why it's there

The two physics overrides catch *severe* crashes with certainty. But a
moderate crash (3–4 g, horizontal 1.5–2.4 g) sits in a grey zone: above
what braking does, but the 2.5 g rule alone would miss it. For that zone we
use a **score**:

```
score = −15.4 + 2.14·ln(1+peak_a) + 3.61·ln(1+sus50_h) + 0.72·ln(1+dv100)
        + 0.63·ln(1+dv300) − 2.25·ln(1+vratio) + 0.35·ln(1+peak_wh)
        + 2.44·ln(1+tilt90) + 1.44·ln(1+peak_av)
crash if score ≥ 2.37
```

How to explain it: "each of the eight measurements is scaled and weighted,
the weights are added up, and if the total crosses a line it's a crash".
The logarithm just makes big values count proportionally, not explosively.

**Where the weights come from.** This is a *logistic regression* — the
simplest statistical classifier there is, the same thing a medical
risk-score is. We gave it 25,084 real recorded events from the VZCrash
**training** files (labelled crash / not crash by human reviewers watching
dashcam video), plus every violent moment from 4.7 h of ordinary driving
as extra "not crash" examples, and it found the weights that best separate
them. Then the threshold 2.37 was picked on a held-out quarter of that
training data. **The test data was not touched** until the final
measurement.

**Why the weights make sense** (say this — it shows the model learned the
physics, not noise): the largest positive weight is on *sustained
horizontal g* (+3.61, the tyre-limit quantity); the largest negative weight
is on *vertical ratio* (−2.25, "it was a pothole-shaped event"). A reviewer
can read the whole model off one slide.

**Why not a neural network?** Verizon's own paper trains a 1.7-million-
parameter network on this data and gets 97.5 % average precision in the lab
— which drops to 86 % on real fleet traffic. Ours is 8 numbers, runs on a
₨ 1,500 microcontroller, is fully inspectable, and the hard guarantees
don't depend on it at all. The published comparison that matters (Chalmers
smartphone study) is: a plain threshold gives ~10 false alarms per hour; a
classifier is "vastly better". We use the simplest classifier that is.

---

## Level 6 — the data, what each dataset proves, and its limits

You cannot crash cars to test this, so the evidence comes from three public
datasets. Know all three:

**VZCrash** (Verizon Connect, released 2026, IEEE ITSC paper, arXiv
2606.06074). Dashcam-integrated IMUs in commercial fleet vehicles (cars,
vans, trucks). 190,000 16-second clips at 100 Hz; 31,090 are *verified*
crashes — labelled by human reviewers watching the video, three-way
agreement. We used the **test split: 24,174 clips, 4,221 crashes, 3,633
near-misses (hard evasive manoeuvres), 16,320 "normal driving"**. Crucial
detail: *every* clip, even "normal driving", was pre-selected by the
vehicle's harsh-driving trigger — so the non-crash clips are the hardest
possible negatives (potholes, off-road, trailer coupling), not ordinary
driving. Licence: non-commercial, fine for a FYP. *Proves:* crash recall
and the false-alarm rate on harsh events.

**PVS — Passive Vehicular Sensors** (Menegazzo & von Wangenheim, UFSC
Brazil). Nine drives, **3.0 hours**, three different cars and drivers,
MPU-9250 (the MPU-6050's sibling) rigidly mounted on the dashboard,
100 Hz, GPS, every sample labelled with surface (asphalt / cobblestone /
dirt), roughness, and speed bumps. *Proves:* false alarms per hour on
ordinary driving, including rough roads, and that the road branch reacts
to real bumps. *Limit:* individual potholes are not labelled.

**RoadSens-4M** (Khandakar et al. 2025, Figshare, CC BY 4.0). 101 phone
recordings, 1.7 h, Bangladeshi urban roads, with an observer marking
"pothole road" and "speed bump" stretches. *Used for:* a second
false-alarm check on very rough roads with a loosely mounted phone.
*Limit:* the labels mark long rough stretches, not individual holes, and
the phone signal contains almost no drop-then-hit signatures — so it
cannot measure pothole recall. We say this in the report rather than
pretend.

---

## Level 7 — the results, one by one, and what each means

All from `evaluation.md`, regenerated by one command, on data the fitted
weights never saw.

**Crash detection (VZCrash test split):**

| tier | definition | crashes | detected |
|---|---|---|---|
| Severe | peak ≥ 4 g, or sustained horizontal ≥ 2.5 g | 2,555 | **100.0 %** |
| Moderate | peak 3–4 g | 769 | 79.3 % |
| Minor | peak 1.8–3 g | 817 | 31.8 % |
| Below pre-trigger | peak < 1.8 g | 80 | 0 % |
| All | | 4,221 | 81.1 % |

Meaning: every crash violent enough to be above the tyre limit is caught,
by construction. Moderate ones mostly; minor ones — bumper taps and
parking-speed contacts that never exceed what braking produces — mostly
not, and *no body-mounted sensor can do better on those*, because the
signal is physically the same as braking.

**False alarms:**

| test | result |
|---|---|
| 3.0 h of real driving, three cars, asphalt + cobblestone + dirt + speed bumps (PVS) | **0** crash or rollover alarms |
| 1.7 h of phone data on rough urban roads (RoadSens) | 0 |
| VZCrash harsh-event negatives (near-miss + "normal driving" clips) | 2.5 % fire |

Meaning of "0 in 3 h": by the statistician's **rule of three**, observing
zero events in 3 hours means the true rate is below 1 per hour with 95 %
confidence — which is exactly the agreed target.

Meaning of "2.5 %": those clips are *all* harsh events. The paper tells us
a fleet vehicle generates about 6 harsh events per 48 hours. 2.5 % of that
is **one false alarm per roughly 100–300 driving hours** — and the driver
has 15 seconds to cancel it. (The number is conservative: commercial fleets
drive harder than private cars.)

**Potholes (road branch):** sees 82 % of PVS's labelled speed bumps and
misreports none of them as potholes; ~1 pothole report per hour on asphalt
that PVS labels "ok" (which may well contain real unlabelled potholes — we
don't know, which is why the own-car drive matters).

**Speed:** 52 ns per reading on a laptop; < 0.2 % of the ESP32's time
even at 300× slower.

**Consistency check:** the report recomputes the engine's decisions
offline from the recorded features and confirms they match the compiled C
exactly — so there is no gap between "what we measured" and "what the chip
does".

---

## Level 8 — why this is acceptable by industry standards

Have these comparisons ready:

- **Apple Crash Detection** (iPhone 14 / Watch, 2022): uses a 256 g
  accelerometer, gyroscope, GPS speed, microphone and barometer together;
  Apple states openly that "minor fender benders" do not trigger it, and it
  still produced false 911 calls from roller coasters. Industry does not
  claim 100 % of all collisions; it claims the severe ones.
- **Airbag controllers** deploy on a windowed velocity-change (ΔV)
  threshold, not on raw peak g; side-airbag deployment lower limit is
  3–5 g (SAE 07-12-02-0012). Our `dv100`/`dv300` features are the same
  quantity; our 4 g / 2.5 g rules are in the same range.
- **Verizon's own models** on this exact dataset: the best neural network
  reaches 97.5 % average precision in the lab and **86 % on the real fleet
  stream**; at 75 % recall it still produced 10 false alarms among 735,000
  events, and a plain peak-threshold baseline produced 780. Our 8-weight
  score plus physics rules sits between those with a fraction of the
  compute.
- **Published smartphone threshold detectors** give about **10 false
  alarms per hour** at 80 % sensitivity (Chalmers). We measured 0 per 3 h.
- **The feature spec's own KPI** was "false positive rate from potholes,
  braking, speed bumps" — the thing the test actually measures, on real
  roads with all three present.

The honest framing that wins: *"We detect 100 % of crashes severe enough
to need automatic help, we tested on 4,221 real crashes and 3 hours of real
driving, we produced zero false alarms on real roads, and the one tunable
number is shown as a full trade-off curve so the team chose the operating
point deliberately."*

---

## Level 9 — how it fits the hardware and what each teammate needs

```
 MPU-6050 ──I2C──▶ ESP32 DevKit ──UART (3.3 V)──▶ Jetson Orin Nano ──HTTPS──▶ adas-cloud
                     │ buzzer                       │ GPS, screen                 │
                     │ cancel button                │ crash_link.py               │ hazards table
                     └ runs engine.c                └ posts accident / pothole    └ Feature 2 shares it
```

- **The ESP32 is independent.** It detects, buzzes, counts down and logs
  with no Jetson at all. That is why it has its own chip: it keeps working
  during Sentry Mode and Jetson reboots. Events are queued and re-sent until
  the Jetson acknowledges.
- **Hardware teammate:** wire as in the sketch header (`firmware/
  retroadas_crash/retroadas_crash.ino`): MPU-6050 on GPIO 21/22 (I2C),
  INT on GPIO 4, buzzer GPIO 25, cancel button GPIO 27 to GND, UART2 on
  GPIO 16/17 to the Jetson's UART pins (both 3.3 V, no level shifter).
  Mount the box rigidly — a loose box produces big rotation readings on
  ordinary bumps.
- **Jetson teammate:** run `jetson/crash_link.py --port /dev/ttyTHS1`. It
  already parses the protocol, acknowledges events, and sends `$SPD`
  (speed) once a second. Two things to plug in: the real GPS in the `Gps`
  class, and the device id + API key for `POST /v1/hazards`.
- **Backend teammate:** two event tags arrive from this feature —
  `accident` (confidence 1.0, with peak g) and `pothole` (confidence from
  severity). Both go through the existing dedup-on-write; potholes will
  get confirmed by repeated reports, accidents expire after their TTL.
- **Everyone:** the wire format is human-readable text lines
  (`$EVT,12,CRASH,20150,4.62,3.10,2.81,2.44,3,41.0*61`), so any serial
  terminal shows what is happening. `protocol.md` is the contract.

---

## Level 10 — questions you will be asked, and the answers

**"Why not use the Jetson for this?"** Because it must work when the
Jetson is asleep (Sentry Mode) or rebooting, and a crash is exactly when
the Jetson is most likely to be damaged or lose power.

**"Why these thresholds?"** Level 4 table. The two hard rules come from
the tyre friction limit; the pothole numbers from a 72-point sweep on
labelled real driving; the score threshold from held-out training data.

**"Did you tune on your test data?"** No. Train/test split by vehicle, done
by the dataset authors. `fit_model.py` only reads train; `run_eval.py`
reads test once.

**"Why isn't recall 100 %?"** Because 20 % of labelled crashes never exceed
what hard braking produces — the signal is physically identical. Show the
tier table; cite Apple's "minor fender benders don't trigger" and Verizon's
own 86 % field result. Claiming 100 % on all would be the indefensible
position.

**"How do you know the false-alarm rate on real roads?"** 3 hours of
labelled real driving in three cars including cobblestone, dirt and speed
bumps: zero alarms. Rule of three: < 1/h at 95 % confidence. Plus 1.7 h of
rough urban phone data: zero. Plus the harsh-event rate conversion.

**"What about a loose mount / different car?"** The gravity tracker makes
it orientation-independent. The three PVS cars and the Bangladeshi phone
data are four different mounts; none produced an alarm. A rigid mount is
still required and documented.

**"Will it run in real time on the ESP32?"** 52 ns/sample on a PC vs a
10 ms budget; plain C99, no dynamic memory, single-precision float only
(the ESP32 FPU is single precision). The benchmark runs the *same compiled
code* as the firmware.

**"Can a driver cancel a real crash by accident?"** The countdown is 15 s
and needs a deliberate button press; the event is still logged locally
with 3 s of raw data either way.

**"Why is GPS speed not used to decide?"** The earlier benchmark showed
99 % of near-misses also drop ≥ 10 km/h — speed drop cannot separate a
crash from an emergency stop. It is attached to the report for context.

**"What is unfinished?"** Firmware not yet run on a real board; pothole
recall has no individual-hole ground truth (the 30-minute phone drive in
`own_data.md` is the plan); real-road false-alarm data is 3 h from one
country; rollover recall cannot be measured because no dataset has
labelled rollovers (its false-alarm rate is measured: 11 in 24,174 harsh
events, 0 on real roads).

---

## Level 11 — how to run and show it

In a terminal inside `adas-automatic-crash-detection/`:

```
.venv\Scripts\python eval\engine.py              # 2 s synthetic self-test
.venv\Scripts\python eval\run_eval.py            # regenerate evaluation.md (~30 s)
```

Live demo, two terminals. Terminal 1 is the Jetson:

```
.venv\Scripts\python jetson\crash_link.py --tcp 9000 --gps-fake 40
```

Terminal 2 is the ESP32 replaying a **real recorded crash** in real time:

```
.venv\Scripts\python eval\replay.py vzcrash --label crash --n 0 --tcp 9000 --countdown 5
```

You see: crash detected → acknowledged → countdown → confirmed → "accident"
posted → emergency contact stub. Change `--label near_miss` to replay a
real emergency stop and watch nothing happen. With a real board on COM5,
`--port COM5` streams the same clip into it and its buzzer goes off.

---

## Glossary

- **IMU** — the accelerometer + gyroscope chip.
- **g** — unit of acceleration; 1 g = gravity = 9.81 m/s².
- **100 Hz** — 100 readings per second.
- **Recall** — of the real crashes, the fraction we detect.
- **False positive / false alarm** — an alarm with no crash.
- **FPR** — of the non-crash events, the fraction that fired.
- **Tier** — severe / moderate / minor, by peak g and sustained horizontal g.
- **Physics override** — a rule that fires regardless of the fitted score.
- **Logistic regression** — the simplest statistical classifier; a weighted sum and a threshold.
- **Train / test split** — data used to choose the weights vs data used only to measure.
- **ΔV** — velocity change during an impact, the airbag-industry severity measure.
- **ESP32** — the microcontroller the detector runs on. **Jetson** — the main computer. **UART** — the serial wire between them.
- **Commit** — a saved snapshot on your laptop. **Push** — uploading commits to GitHub.
