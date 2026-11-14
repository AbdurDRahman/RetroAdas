# Feature 4: Sentry Mode (Parked Vehicle Monitoring)

> **Status: tentative on scope/priority — architecture and power budget now
> updated** following a redesign of the recording pipeline and removal of
> Jetson deep sleep.

## Overview
While the vehicle is parked and off, the system keeps 4 cameras and 4
mmWave radar sensors continuously active. Each camera constantly feeds a
rolling local buffer of the last few seconds of footage. When a radar
detects motion near the vehicle, the buffered pre-event footage is saved
together with the live footage that follows, giving a continuous record
that starts a few seconds *before* the triggering motion. There is no
AI classifier and no server notification — footage is stored locally only,
and the owner reviews it manually if something goes wrong.

## Scope

- Always-on cameras (4×, 8 fps, 720p) continuously writing to a rolling
  local buffer
- Always-on radar (4× RD-03D) for motion detection, one per camera
- On radar trigger: retain the pre-event buffer + continue recording live
  footage for the event window
- Periodic purging of buffered footage that was never triggered (to
  manage storage)
- No AI classifier, no cloud/server communication, no push notifications —
  purely local capture and storage

## Functional Requirements
1. Keep all 4 cameras capturing continuously at 8 fps / 720p while parked.
2. Keep all 4 RD-03D radar sensors active, each watching its paired
   camera's field of view.
3. Each camera writes continuously into a rolling buffer covering the last
   N seconds (buffer length to be sized against storage capacity).
4. On motion detected by any radar, mark that buffer segment (and the
   corresponding cameras) for retention: keep the pre-trigger buffer and
   continue recording live footage until motion stops + a cooldown window.
5. Footage not flagged by a radar trigger is periodically purged on a
   rolling basis to free storage.
6. No inference, no classification, and no network/server communication
   occur in this mode. The Jetson does **not** enter a sleep state — it
   remains awake and running the capture/encode pipeline the entire time
   Sentry Mode is active.
7. The owner reviews retained footage manually (e.g. by connecting to the
   device or pulling the storage media) if they suspect tampering/impact.

## Technical Approach (updated — no deep sleep, always-on capture)
- **Cameras:** 4×, 8 fps, 720p (1280×720), continuously buffering
- **Radar:** 4× Ai-Thinker RD-03D mmWave radar (24–24.25 GHz FMCW,
  multi-target, ~0.5–8 m range, UART @ 256000 bps), one per camera
- **Compute:** Jetson Orin Nano stays fully awake — **no SC7 deep sleep**.
  The Orin Nano has no NVENC hardware video encoder, so all 4 streams are
  encoded in software (libx264/x264enc) on the CPU; this is the dominant
  cost in the new power budget.
- **Power source:** Car battery (primary, 270 Wh usable) with a 20,000 mAh
  power bank as failover when the car is off/disconnected
- **Data flow:** 4× camera → rolling buffer (local) ⇄ radar trigger signal
  → on trigger, buffer + live footage retained → local storage (SD card) →
  periodic purge of untriggered segments. No path to a classifier or a
  server in this mode.

## Power / Duration Budget (recalculated for always-on architecture)

**Component draws:**

| Component | Qty | Unit draw | Subtotal | Basis |
|---|---|---|---|---|
| Jetson Orin Nano (capture + software encode, no inference) | 1 | 7–10 W | 7–10 W | ~5 W idle + software-encode CPU load (no NVENC on this module) |
| Camera (720p, continuous) | 4 | ~0.7–1.1 W | ~3–4 W | typical CSI/USB camera draw at this resolution |
| RD-03D radar | 4 | 0.46 W | ~1.84 W | 92 mA avg × 5 V, per datasheet |
| microSD (continuous buffered write) | 1 | ~0.3–0.5 W | ~0.5 W | continuous write load |
| **Total Sentry active draw** | | | **~13–15 W (typical)** | range ~11–18 W |

**Duration table (car battery, 270 Wh usable, vs. failover power bank):**

| Mode | Draw | Source | Usable energy | Duration before drain |
|---|---|---|---|---|
| Sentry active, typical | ~14 W | Car battery | 270 Wh | ~19 h |
| Sentry active, optimized | ~11 W | Car battery | 270 Wh | ~24.5 h |
| Sentry active, conservative | ~18 W | Car battery | 270 Wh | ~15 h |
| Sentry active, typical | ~14 W | Power bank (below) | ~55 Wh usable | ~4 h |
| Sentry active, optimized | ~11 W | Power bank (below) | ~63 Wh usable | ~5.7 h |
| Sentry active, conservative | ~18 W | Power bank (below) | ~52 Wh usable | ~2.9 h |

This is why Sentry Mode's power profile has changed fundamentally: with
deep sleep removed, standby is no longer near-zero — the pipeline now
draws roughly **35× more power** than the original 0.4 W deep-sleep
design, and car-battery runtime drops from ~28 days to under a day.

## Power Bank (failover source)

- **Model:** Faster S20 PD-20W, 20,000 mAh
- **Price:** ~PKR 7,149 (PriceOye) / ~PKR 7,500 (Powerhouse Express) —
  within the PKR 5,000–10,000 budget
- **Outputs:** USB-A: 5V/3A, 9V/2A, 12V/1.5A; USB-C: 5V/2A, 9V/2.22A,
  12V/1.5A (max ~20 W)
- **Nominal energy:** 20,000 mAh × 3.7 V = 74 Wh; real-world usable after
  conversion losses ≈ **52–63 Wh** (use ~55 Wh for planning)
- **Note:** its ~20 W max output is close to the ~14 W typical system
  draw, so expect it to run warm and near its limit; use a PD trigger
  cable set to 9 V or 12 V, since the Jetson dev kit needs 9–19 V input
  (the bank's 5 V outputs cannot power it directly)

## KPIs (updated)

- Car-battery Sentry runtime: target ~19–25 h continuous (down from the
  original 28-day target under deep sleep)
- Power-bank failover runtime: target ~4–6 h continuous
- Pre-trigger buffer retained on every radar event: 100%
- False trigger rate (non-threat motion causing unnecessary buffer
  retention/storage use)
- Storage consumption rate during untriggered idle buffering (governs
  purge cadence and buffer length)

## Dependencies / Components

- 4× cameras, 8 fps / 720p
- 4× Ai-Thinker RD-03D mmWave radar sensor (5 V supply, ~92 mA / 0.46 W
  each)
- Jetson Orin Nano (no deep sleep; software-encode capable of 4 streams
  at 8 fps/720p — to be profiled with `tegrastats` for actual draw)
- 20,000 mAh power bank — Faster S20 PD-20W (or equivalent PD unit with
  9 V/12 V output ≥18 W)
- Local storage for rolling buffer + retained event footage (capacity to
  be sized against buffer length × resolution × 4 streams, and purge
  cadence)
- Turnover switch for switching between sentry mode and driver mode

## Open items for next iteration
- On-device power measurement (`tegrastats`, VDD_IN rail) to replace the
  7–10 W Jetson estimate with a real figure
- Decide buffer length (seconds) and storage sizing accordingly
- Consider whether radar-gated duty-cycling of the encode pipeline (only
  encode when a radar is active, otherwise hold cameras in a lower-power
  capture state) is worth the added complexity to recover runtime, versus
  accepting the ~19 h car-battery ceiling as sufficient for the use case