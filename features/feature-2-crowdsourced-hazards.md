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
- Server-side infrastructure designed to scale horizontally as the device
  fleet grows, without changes to the client-facing API

## Design Principles
- **No WebSockets — fully stateless request/response.** The server holds
  no live/session state per device; every request carries everything
  needed to answer it. This is what allows the backend to scale out
  horizontally later (any replica can serve any device, no in-memory per-device state).
- **Distance-triggered polling, not timer-based.** Client polls roughly
  every ~300 m traveled rather than on a fixed interval. Naturally goes
  idle in stopped/slow traffic, avoiding wasted requests and load.
- **Projected lookahead.** The client sends its raw position, heading,
  and speed; the server projects a point ~200 m ahead along the heading
  (PostGIS `ST_Project`) and searches around *that* point, not the
  vehicle's literal current position — so warnings don't feel laggy.
- **Per-device authentication on writes**, via `device_id` + API key,
  checked against a `devices` table (hashed key, never stored raw).

## Functional Requirements
1. Edge device posts auto-detected hazards to the server (`POST /v1/hazards`),
   sent opportunistically as detections occur.
2. Mobile app allows users to manually submit a hazard report.
3. Server deduplicates reports of the same real-world hazard: a new report
   within a radius (~100 m, tunable per `event_tag`) of an existing
   non-expired hazard of the same tag increments `confirmations` and
   resets `expires_at`, rather than creating a duplicate row. Original
   coordinates remain authoritative (not averaged). No match → new row
   inserted.
4. Each hazard has a **confidence score** (from the detecting device's
   model output, set once at insert) and a separate **confirmation
   count** (independent re-reports) — tracked separately, never merged.
5. Each hazard type has a **time-to-live (TTL)**; hazards expire
   automatically unless re-confirmed, which resets the TTL. TTLs are
   purely passive expiry.
   Illustrative placeholders: checkpost — hours; accident — 1–2 hrs;
   pothole/debris — days–weeks.
6. Vehicles periodically query the server for hazards within a lookahead
   radius of their projected position (`GET /v1/hazards`), using
   server-side `ST_Project` + `ST_DWithin`.
7. Mobile app displays a hazard map and the user's contribution score.
8. Confirmed/expired hazards are removed from active alerts — no
   background push; hazards simply stop being returned by `GET /v1/hazards`
   once expired.
9. Devices check in at boot via a handshake, so the server can confirm
   they're not revoked and hand back tunable config.

## Technical Approach

### Server
Go backend, stateless, containerized. Currently being developed and tested
against a Postgres + PostGIS instance on a single Azure VM (chosen over
managed Postgres for cost/simplicity/control over compute specs during
load testing); DB moves to its own VM once real throughput testing begins.

### Schema (MVP — 2 tables)
**`hazards`**
- `event_tag` (pothole/checkpost/accident/debris/etc.)
- `location` (GEOGRAPHY Point, GiST-indexed)
- `heading_deg` (reporting vehicle's bearing, nullable)
- `confidence` (0.0–1.0, from reporting device's detection, set once at insert)
- `reported_by` (device_id of original reporter)
- `confirmations` (incremented on independent re-reports)
- `created_at`, `expires_at` (TTL, reset on confirmation)

**`devices`**
- `device_id`
- `api_key_hash` (hash only, never raw)
- `device_type`
- `revoked`

### API
- `POST /v1/devices/handshake` — boot-time check-in; confirms device
  isn't revoked, optionally returns tunable config (poll distance,
  lookahead distance, search radius).
- `POST /v1/hazards` — device posts a detected/reported hazard (auth via
  `device_id` + API key against the `devices` table). Server performs
  dedup-on-write as described above.
- `GET /v1/hazards?lat=&lng=&heading_deg=&speed_kmh=&radius_m=` — server
  computes a projected lookahead point via PostGIS `ST_Project`, then
  `ST_DWithin` radius search, and returns hazards within range.

### Distribution model
No server-push, no persistent connections. Vehicles and the mobile app
*pull* relevant hazards on their own polling cadence via `GET /v1/hazards`.
This keeps the server stateless and avoids the complexity/cost of
maintaining live connections to a growing device fleet.

### Scalability / infrastructure architecture
Because the API is fully stateless (no WebSockets, no server-side
per-device tracking, auth resolved per-request against the DB), the
backend can scale horizontally by simply running multiple identical
server replicas behind a load balancer .

```
Edge devices / mobile app
        │  HTTPS, short-lived requests
        ▼
   Load Balancer   (round-robin / least-connections — no sticky sessions needed)
        ▼
┌────────────────────────────────┐
│   Stateless Go API replicas    │  ← scales out horizontally
│   (identical, no shared state) │
└────────────────────────────────┘
        ▼
   Postgres + PostGIS
   (current scaling bottleneck — read replica / connection pooling
    are the future-work path once write load grows)
```

- **Target production pattern:** Kubernetes Deployment (replica pool) +
  Service (load balancing) + Horizontal Pod Autoscaler (scale on
  CPU/latency).
- **Demonstration approach for this project:** a smaller proof-of-concept
  — 2–3 containerized replicas of the Go server behind a load balancer
  (Azure Load Balancer or nginx), with k6 driving simulated fleet traffic
  and each response tagged with the serving instance ID, to visibly show
  request distribution and flat latency as load increases. Spun up only
  for demonstration and torn down afterward to conserve Azure credits.
- **Day-to-day development** continues against a single VM, matching the
  MVP scope; the multi-replica setup is an infrastructure demonstration
  layered on top of the same stateless API, not a separate system.
- **Database is explicitly not part of the horizontal-scaling story yet**
  — Postgres remains a single instance for now; read replicas / PgBouncer
  connection pooling are noted as future work once real throughput
  testing (via k6) identifies it as the bottleneck.

### Development approach
TDD, two testing layers:
- **Go tests** (`testify` + real Postgres/PostGIS, transaction-per-test
  with rollback) — drives development of dedup logic, auth, and
  projection search.
- **k6 scripts** — simulate a fleet of virtual devices (polling every
  ~300 m, bursts of multiple devices reporting the same hazard) against
  the deployed API, standing in for hardware until Feature 1's CV
  pipeline output is ready. Same scripts double as the load-generation
  tool for the scalability demonstration. Swapped for real device traffic
  later with zero API changes, since the client contract (raw
  position/heading/speed in, hazards out) doesn't change based on what's
  behind the load balancer.

## KPIs (from project deliverables)
- Hazard sharing time (detection → distributed to other vehicles): ≤ 3 s
- Hazard broadcast radius: 1 km

## Dependencies / Components
- Cloud server (Azure VM for MVP; load-balanced multi-replica setup for
  scalability demonstration)
- SQL/PostGIS database
- Mobile application (to be built)
- Networking/connectivity on edge device (hotspot/cellular)

## Open Questions
- Where should the lookahead-projection math live — client-side (device
  sends raw GPS/heading, does its own projection) or server-side
  (**decided: server-side**, per appendix — client sends raw state,
  server projects via PostGIS `ST_Project`).
- What's the actual per-`event_tag` TTL table (potholes probably persist
  longer than "pedestrian on road")? Not yet defined.
- Whether the dedup radius (~100 m baseline) should vary by `event_tag`
  (e.g. accident vs pothole) — not yet finalized.
- How do we prevent spam/false manual reports from tanking trust in the
  system — rate limiting? reputation-weighted confidence?
- Should a Feature 3 crash detection event also get posted into this same
  `hazards` table (e.g. `event_tag: "accident"`) so other vehicles see it
  automatically? Currently not connected in the deck — worth deciding.
- Whether `GET /v1/hazards` needs full device auth or something lighter
  (e.g. rate-limited, unauthenticated reads) — not yet finalized.
- Whether `heading_deg` gets used for anything beyond the current
  projected-lookahead use case (e.g. future route-corridor filtering).