# Feature 2: Crowdsourced Hazard Awareness

## Overview
Share hazard information detected by one vehicle with other RetroADAS
vehicles (and the mobile app) via a central server, so drivers get advance
warning of hazards before their own cameras can see them. Combines
auto-detected hazards (from Feature 1's pipeline) with manually reported
hazards from users.

## Scope
- **Auto-detected hazards** (from onboard CV, no user action required):
  pedestrians on road, stationary vehicles, traffic congestion
- **Manually reported hazards** (user-submitted via mobile app or in-car
  interface): accidents, road closures, potholes, etc.
- Server-side storage, deduplication, and expiry of hazard reports
- Distribution of relevant hazards to nearby vehicles and mobile app users
- Basic gamification: user contribution score

## Functional Requirements
1. Edge device posts auto-detected hazards to the server in real time (`POST /v1/hazards`).
2. Mobile app allows users to manually submit a hazard report (type + location).
3. Server deduplicates reports of the same real-world hazard (same location + tag within a radius/time window) by incrementing a `confirmations` counter rather than creating duplicate rows.
4. Each hazard has a **confidence score** (from the detecting device's model output) and a separate **confirmation count** (how many independent devices/users reported it) — these are tracked separately, not merged.
5. Each hazard type has a **time-to-live (TTL)**; hazards expire automatically unless re-confirmed, which resets the TTL.
6. Vehicles periodically query the server for hazards within a lookahead radius of their current position and heading (`GET /v1/hazards`).
7. Mobile app displays a hazard map and the user's contribution score.
8. Confirmed/expired hazards are removed from active alerts.

## Technical Approach
- **Server:** Cloud server with PostGIS-enabled database for geospatial queries
- **Schema (hazards table):**
  - `id`, `event_tag` (pothole/checkpost/accident/debris/etc.)
  - `location` (GEOGRAPHY Point, GiST-indexed)
  - `heading_deg` (reporting vehicle's bearing, nullable)
  - `confidence` (0.0–1.0, from reporting device's detection)
  - `reported_by` (device_id of original reporter)
  - `confirmations` (incremented on independent re-reports)
  - `created_at`, `expires_at` (TTL, reset on confirmation)
- **API:**
  - `POST /v1/hazards` — device posts a detected/reported hazard (auth via `X-Device-Id` + `X-Api-Key`)
  - `GET /v1/hazards?lat=&lng=&heading_deg=&speed_kmh=&radius_m=` — server computes a projected lookahead point using PostGIS `ST_Project` + `ST_DWithin` and returns hazards within range
- **Distribution:** server pushes relevant hazards to in-range vehicles and to the mobile app; vehicle in-car display renders them via Feature 1's HUD
- **Mobile app:** hazard map view, manual hazard upload form, user score display

## KPIs (from project deliverables)
- Hazard sharing time (detection → distributed to other vehicles): ≤ 3 s
- Hazard broadcast radius: 1 km

## Dependencies / Components
- Cloud server (available)
- SQL/PostGIS database
- Mobile application (to be built)
- Networking/connectivity on edge device (hotspot/cellular)

## Open Questions
- Where should the lookahead-projection math live — client-side (device sends raw GPS/heading, does its own projection) or server-side (current recommended approach, per appendix: client sends raw state, server projects via PostGIS)? Appendix currently recommends **server-side** as the starting point.
- What's the actual per-`event_tag` TTL table (potholes probably persist longer than "pedestrian on road")? Not yet defined.
- How do we prevent spam/false manual reports from tanking trust in the system — rate limiting? reputation-weighted confidence?
- Should a Feature 3 crash detection event also get posted into this same `hazards` table (e.g. `event_tag: "accident"`) so other vehicles see it automatically? Currently not connected in the deck — worth deciding.
