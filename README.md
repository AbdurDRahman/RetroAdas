<p align="center">
  <img src="https://img.shields.io/badge/status-in--development-brightgreen" />
  <img src="https://img.shields.io/badge/platform-Jetson%20Orin%20Nano-76b900" />
  <img src="https://img.shields.io/badge/FYP-NUST%20CEME-8b1a3d" />
</p>

<h1 align="center">🚗 RetroADAS</h1>
<p align="center"><em>Retrofittable Edge-AI Multi-Camera Driving Assistant for Real-Time Hazard Detection & Crowdsourced Awareness</em></p>

---

## What is RetroADAS?

Most advanced driver-assistance features today live inside a small club of
new, high-end cars. If your car wasn't built with them in, you don't get
them — full stop.

**RetroADAS** is our answer to that: a single, self-contained box that
bolts onto *any* car and gives it modern driver-assistance capability. No
factory integration, no vehicle-specific wiring, no waiting for your next
car purchase — just cameras, a compute unit, and a screen.

Under the hood, it watches the road around your car in real time, spots
hazards — pedestrians stepping into traffic, stalled vehicles, sudden
congestion — and shows them to you at a glance. And because every RetroADAS
unit talks to every other one through a shared cloud backend, a hazard your
car sees becomes a hazard *every nearby RetroADAS car* knows about, seconds
later. It's less like a single dash cam, and more like a small, growing
network of cars looking out for each other.

## How it Works

The system is built in three layers — the car itself, the cloud that
connects cars together, and the interfaces people actually touch.

```
                    ┌────────────────────────────┐
   Front Camera ───▶│                           │
   Rear  Camera ───▶│     Jetson Orin Nano      │───▶ Screen Display
   Left  Camera ───▶│ YOLOv8n + Depth · TensorRT│───▶ Audio Buzzer
   Right Camera ───▶│                           │
                    └─────────────┬──────────────┘
                                  │  detected hazards
                                  ▼
                        ┌───────────────────┐
                        │  Server / Cloud   │◀──── Mobile App
                        │ (hazard database) │────▶ (map · manual report)
                        └───────────────────┘
                                  │
                                  ▼
                     shared with nearby RetroADAS cars
```

**On the edge**, four cameras (front, rear, left, right) stream continuously
into a Jetson Orin Nano. A lightweight object-detection model spots vehicles
and pedestrians, a depth-estimation model figures out roughly how far away
they are, and the two are fused into a simple 2D "what's around me right
now" view — rendered on a small in-car display, with an audio buzzer for
anything urgent enough to need it.

**In the cloud**, anything the edge device is confident about — a stalled
car, a pedestrian on the road, heavy congestion — gets pushed up to a
central server. Other RetroADAS-equipped cars nearby, and the companion
mobile app, pull from that same server to see hazards other drivers have
already encountered, before their own cameras ever catch sight of them.
Drivers can also manually report things the system can't detect on its own,
like an accident up ahead.

**On the client side**, this all comes together as an in-car screen showing
the live hazard view, and a mobile app showing a broader hazard map plus a
way to contribute reports of your own.

## Hardware

- **Compute:** NVIDIA Jetson Orin Nano
- **Vision:** 4x camera modules (front / rear / left / right)
- **Display:** Dash-mounted screen
- **Backend:** Cloud server with a geospatial database
- **Connectivity:** Network link between edge device, server, and mobile app

## Features

RetroADAS is built around four core features. Each one is documented in
detail in [`/features`](./features):

- 🚦 **[Real-Time Multi-Camera Hazard Detection](./features/feature-1-realtime-detection.md)** — the eyes of the system: spotting vehicles, pedestrians, and hazards around the car, in clear weather and bad.
- 🌐 **[Crowdsourced Hazard Awareness](./features/feature-2-crowdsourced-hazards.md)** — turning individual detections into a shared, real-time hazard map across every connected vehicle.
- 💥 **[Automatic Crash Detection](./features/feature-3-crash-detection.md)** — sensing the vehicle's own abrupt motion to recognize when *it* may have been in an accident, and calling for help.
- 🌙 **[Sentry Mode](./features/feature-4-sentry-mode.md)** — a low-power watchful eye that stays on guard while the car is parked and unattended.

## Datasets

Model training and evaluation draw on three complementary datasets to keep
detection reliable across conditions, not just on a sunny day:

| Dataset | What it's for |
|---|---|
| **BDD100K** | Clear-weather driving scenes |
| **DAWN** | Rain, snow, and fog |
| **RTTS** | Smog and haze |

## Project

A Final Year Project by **Sheheryar Shahid, M. Abdur Rahman, Rayan Shahid,
and M. Talha Imran**, at the College of Electrical and Mechanical
Engineering (CEME), National University of Sciences & Technology (NUST) —
supervised by **Dr. Asad Mansoor** and **Dr. Wasi Haider Butt**.
