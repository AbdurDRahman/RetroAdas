# Feature 3: Automatic Crash/Accident Detection (IMU-Based)

> **Note:** This feature does not currently appear anywhere in the title
> defense deck (system diagrams, domains, or KPIs). It's a strong addition,
> but it needs to be integrated into the architecture and team ownership
> before your next review — see Open Questions.

## Overview
Use an onboard motion sensor (accelerometer/gyroscope) to detect signatures
consistent with a vehicle collision — abrupt Z-axis acceleration (rollover,
impact) or sudden deceleration from higher speed — and trigger an emergency
alert, with a short window for the driver to cancel a false alarm.

## Scope
- Continuous motion monitoring while the vehicle is being driven
- Crash-signature detection (impact / rollover / sudden deceleration)
- Alarm trigger with countdown and false-alarm cancellation
- Emergency notification (call/SMS to emergency contact or service, with GPS location) if not cancelled

## Functional Requirements
1. Continuously sample accelerometer + gyroscope data while the system is active/driving.
2. Detect crash-signature events:
   - Abrupt Z-axis spike (vehicle jump/rollover)
   - Sudden large deceleration from a meaningfully high speed (hard stop consistent with a collision, not normal braking)
3. On detection, trigger a local alarm (audible + on-screen) immediately.
4. Start a cancellation countdown (e.g. 10–15 seconds) during which the driver can dismiss a false alarm via a physical button or on-screen tap.
5. If not cancelled within the countdown, automatically:
   - Send the vehicle's current GPS location and event details to an emergency contact / configured service
   - Optionally push the event to the server as a hazard (`event_tag: "accident"`) so it's visible to other RetroADAS vehicles nearby (integration point with Feature 2 — needs confirmation)
6. Log the event (timestamp, sensor readings, GPS trace) locally for later review.

## Technical Approach
- **Sensor:** Accelerometer + gyroscope (IMU), either a dedicated low-cost module (e.g. MPU6050 class) or reused from an existing sensor already on the platform if available
- **Processing:** Lightweight threshold/rule-based detection is the natural MVP (e.g. |Δaccel_z| > threshold OR Δspeed/Δt > threshold); can evolve to a simple ML classifier later if false-positive rate is too high
- **Compute:** Runs on Jetson (or a cheap always-on microcontroller if you want it independent of Jetson's boot/sleep state — worth deciding, since Jetson may be asleep in Sentry Mode but should NOT be asleep while driving)
- **Location:** GPS module (already planned as part of Embedded Systems domain)
- **Notification:** SMS/call via a cellular module, or push via the mobile app + server if network connectivity exists

## KPIs (to define — not yet in your deck)
- False positive rate (alarms triggered by potholes, hard braking, speed bumps, etc. that are NOT crashes)
- False negative rate (real crashes missed)
- Detection-to-alarm latency
- Cancellation window duration (long enough for driver to react, short enough for real emergencies)
- Notification delivery latency once countdown expires

## Dependencies / Components
- IMU/accelerometer module — **not yet listed in your resource table, needs sourcing**
- GPS module (shared with Embedded Systems domain)
- Cellular/SMS capability or reliance on server+mobile app push (depends on connectivity assumptions)
- Physical/on-screen cancel button

## Open Questions
- **Ownership:** Which domain owns this? Likely Domain 4 (Embedded Systems — Jetson, GPS, sensors) with Domain 2 (Backend) for the notification/emergency-contact logic.
- **Independence from Jetson state:** If Jetson is asleep (Sentry Mode) or between boot cycles, can this still function, or is it explicitly a "driving-only" feature?
- **Threshold calibration:** What real-world data will you use to set the abrupt-motion thresholds? (You may need to either simulate crash-like motion safely or find an existing accelerometer crash dataset.)
- **Integration with Feature 2:** Should a confirmed crash auto-post to the shared hazards table so nearby cars slow down/reroute?
- **Emergency contact mechanism:** Does this rely on the driver's phone (via a companion mobile app foreground/background service) or does the edge device need its own cellular connectivity? This affects hardware cost and design significantly.
