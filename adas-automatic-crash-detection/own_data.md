# Recording your own calibration drive (phone, 30 minutes)

The public datasets give crash recall (VZCrash) and false-alarm rates on
real roads (PVS, RoadSens-4M), but none of them has *event-level* pothole
ground truth on Pakistani roads. One recorded drive with a phone fixes that
and is the most convincing thing to show a panel: "we drove over these 20
potholes and 10 speed bumps, here is what the unit reported".

## What you need

- Any Android or iPhone with the free **Sensor Logger** app (Kelvin Choi) —
  the same app RoadSens-4M was recorded with, so the loader already exists
  (`eval/own_recording.py`).
- A rigid phone mount on the dashboard or windscreen. **Not** a loose phone
  in a cup holder: a loose phone rotates and rattles, which is exactly what
  produced the crash false alarms on the RoadSens phone data.
- A passenger to press the annotation button (the driver drives).

## App settings (once)

Sensor Logger → Settings:

- Sampling frequency: **100 Hz** (or "Fastest").
- Enable: **Total Acceleration**, **Gyroscope**, **Location** (GPS speed).
  Everything else can stay off.
- Export format: CSV.

## The drive

1. Mount the phone, start recording, leave the car still for 5 s (the engine
   latches its mounting baseline in the first 2 s).
2. Drive a loop that includes, as far as safe:
   - 10–20 **potholes** of different sizes, at 20–50 km/h
   - 5–10 **speed bumps**
   - 3–5 **hard brakes** from 40–60 km/h on an empty road (this is the
     near-miss class — the alarm must *not* fire)
   - a stretch of **bad / unpaved road**, if available
   - ordinary smooth driving, 10+ minutes
3. The passenger presses the annotation button in the app at the moment of
   each event and types one of: `pothole`, `bump`, `brake`, `rough`.
   Consistency matters more than precision — within a second is fine.
4. Stop, export, copy the `.zip` to `data/own/` on the laptop.

## Running it

```bash
.venv\Scripts\python eval\own_recording.py data\own\2026-11-20_drive1.zip
```

It prints every engine event with the annotation active at that moment,
then two tables: events per annotation, and annotations by whether the
engine reported something within 3 s. A `pothole` annotation with a
`POTHOLE` event is a hit; a `brake` annotation with a `CRASH` event is a
false alarm.

If the pothole thresholds need adjusting for your mount, change them in
`src/engine.c` (`engine_cfg_default`), rerun `eval/run_eval.py` to make sure
nothing regressed on PVS/RoadSens, and `firmware/sync_src.py` before
flashing.

## Same drive with the real unit

Once the ESP32 box exists, do the same drive with the box mounted and the
laptop logging its USB output (`jetson/crash_link.py --port COMx`); the
`$EVT` lines in `crash_link.log` line up with the phone annotations by
wall-clock time.
