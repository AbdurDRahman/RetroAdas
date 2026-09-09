# Feature 1: Real-Time Multi-Camera Hazard Detection

## Overview
Detect vehicles, pedestrians, and other road hazards in real time using a
multi-camera edge-AI pipeline, robust across normal and adverse weather
(rain, fog, haze, low light), and present the results to the driver as a
simple 2D situational-awareness display.

## Scope
- Object detection: vehicles, pedestrians (extendable to cyclists, animals, debris)
- Distance / proximity estimation to detected objects
- Multi-camera coverage: front, rear, left, right
- Robustness across weather/lighting conditions
- Real-time driver-facing visualization (HUD)

## Functional Requirements
1. Ingest synchronized video streams from 4 cameras (front/rear/left/right).
2. Run object detection on each stream at a minimum of 8 FPS.
3. Classify detected objects (vehicle, pedestrian, at minimum).
4. Estimate distance from ego-vehicle to each detected object.
5. Fuse detections across cameras into a single "scene state" (what's around the car right now).
6. Render scene state on an in-car display as a top-down/lane-relative 2D view.
7. Trigger audio alert (buzzer) when a hazard crosses a distance/severity threshold.
8. Maintain acceptable accuracy in rain, fog, haze, and low-visibility conditions.

## Technical Approach
- **Detection model:** YOLOv8n (nano, for edge inference speed)
- **Depth/distance:** DepthAnything (or comparable monocular depth model) fused with detection boxes to approximate real-world distance
- **Inference runtime:** TensorRT (optimized/quantized engine on Jetson)
- **Video pipeline:** GStreamer with async queues per camera, NVMM zero-copy GPU memory to avoid CPU round-trips, NVStreamMux for batching multiple camera feeds into one inference pass
- **Compute:** Jetson Orin Nano
- **Output consumers:** On-screen HUD display, Hazard-Alert Controller (buzzer/audio), and the Feature 2 crowdsourcing pipeline (auto-detected hazards get pushed to server from here)

## Datasets (for training/fine-tuning + weather robustness)
| Dataset | Purpose |
|---|---|
| BDD100K | Clear-weather vehicle/pedestrian detection baseline |
| DAWN | Rain, snow, fog detection |
| RTTS | Smog/haze detection |

Consider a weather-conditioned evaluation split (test accuracy separately per
condition, not just aggregate mAP) since aggregate mAP can hide poor
performance in adverse conditions.

## Display / UX Requirements
- Simple 2-D lane-relative view (as shown in mockup): ego vehicle centered, other vehicles/hazards shown at approximate relative position and distance.
- Severity indicator for the most urgent hazard (e.g. "Severity 1–3").
- Status bar: speed, lane status, GPS lock, network status, active hazard count, cloud alert status, time.
- Must be glanceable — legible in under 1 second of driver attention.

## KPIs (from project deliverables)
- Detection accuracy: ≥ 70% mAP
- Inference speed: ≥ 8 FPS
- Alert latency (detection → driver alert): ≤ 1 s

## Dependencies / Components
- Jetson Orin Nano (available)
- 4x camera modules (available)
- HUD/screen (available)
- Trained YOLOv8n weights (to be trained on BDD100K + DAWN + RTTS)
- DepthAnything model or equivalent, optimized for edge inference

## Open Questions
- What's the minimum object size / distance at which detection must reliably fire?
- Do we fuse camera detections at the bounding-box level or only after independent per-camera inference (simpler, less accurate)?
- What's the fallback behavior if a camera feed drops mid-drive?
- Exact severity-scoring formula (distance × object type × relative speed?) — not yet defined in the deck.
