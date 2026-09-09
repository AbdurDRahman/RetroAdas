# Feature 4: Sentry Mode (Parked Vehicle Monitoring)

> **Status: tentative**, per your own framing — but it's actually the most
> fully-specified feature in your appendix (power budget, wake latency, and
> architecture are already worked out). Marked tentative mainly on scope/priority, not readiness.

## Overview
While the vehicle is parked and off, keep a low-power motion sensor active
to detect potential tampering or impact. On motion detection, wake the
system, record footage, and alert the owner if the event is confirmed
significant.

## Scope
- Always-on low-power motion sensing while parked
- Wake-on-motion for the main compute (Jetson)
- Buffered local footage recording around the trigger event
- Alert to owner's phone (via server) if event is confirmed

## Functional Requirements
1. While parked, run cameras at reduced fps/resolution and keep a motion sensor (radar) always-on at very low power.
2. Keep Jetson in deep sleep (SC7) between events to conserve power.
3. On motion detection by the radar, wake the Jetson from sleep.
4. Jetson boots, begins recording, and buffers the last few minutes of footage locally (rolling buffer, not just post-trigger).
5. If the event is confirmed significant (e.g. contact/impact detected, not just a passing pedestrian), send an alert to the owner's phone via the server.
6. Jetson returns to deep sleep after the event window ends with no further motion.

## Technical Approach (from appendix — power budget already validated)
- **Sensor:** Microwave radar, always-on, ~0.05 W, detects motion at ~10 m range
- **Compute sleep state:** Jetson SC7 deep sleep, ~0.35 W
- **Total standby draw:** ~0.4 W
- **Power source:** Car battery (primary) with a 20,000 mAh power bank as failover when the car is off/disconnected
- **Wake latency:** Jetson boot time 4–7 s; at typical "someone approaching a parked car" speeds (~5 kph), an object detected at 10 m only covers ~1.4 m during boot — still within a safe detection window
- **Data flow:** Cameras (reduced fps/res) → Jetson (on wake) → local buffered footage (last few minutes) → server (if confirmed) → owner's phone alert

## Power / Duration Budget (already calculated in appendix — reference)
| Mode | Draw | Source | Duration |
|---|---|---|---|
| Active driving (4 cams + AI) | ~10 W | Car battery (270 Wh usable) | ~27 h before drain |
| Sentry standby | ~0.4 W | Car battery (270 Wh usable) | ~675 h (~28 days) |
| Sentry standby (failover) | ~0.4 W | 20,000 mAh power bank (~63 Wh usable) | ~157 h (~6.5 days) |

This is why Sentry Mode *requires* the sleep-state design — running the
active-mode power draw while parked would drain the car battery in about a day.

## KPIs (to formalize)
- Standby duration on car battery: target ~28 days (per calculation)
- Standby duration on power bank failover: target ~6.5 days
- Wake latency: ≤ 7 s (boot time)
- False trigger rate (non-threat motion, e.g. leaves, other pedestrians walking by, causing unnecessary wake/recording)

## Dependencies / Components
- Microwave radar/motion sensor — **not yet in resource availability table, needs sourcing**
- Jetson Orin Nano SC7 deep sleep support (verify carrier board wake-on-GPIO/interrupt capability)
- 20,000 mAh power bank
- Local storage for buffered footage (SD card / eMMC — capacity needs sizing based on buffer length × resolution)
- Server-side push notification path (shared with Feature 2's infrastructure)

## Open Questions
- What exactly counts as a "confirmed" event worth alerting the owner vs. just logging locally? (Simple motion vs. contact/proximity/dwell-time based?)
- Local footage storage: how much buffer, how is old footage rotated out, and does it survive power loss?
- Does Sentry Mode need its own tamper-alarm (e.g. audible siren) or is it purely a silent recording + notify system?
- How does the system distinguish "car being broken into" from "car being normally approached by the owner" (e.g. keyfob proximity, app-based disarm)?
