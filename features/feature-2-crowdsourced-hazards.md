# Feature 2: Crowdsourced Hazard Awareness

> **Backend details:** the agreed backend design is
> [`adas-cloud/docs/design.md`](../adas-cloud/docs/design.md) (task C-001). Where this spec and
> that document differ, the design document wins; the points below were updated to match it.

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
- **Speed-based polling.** The poll distance grows with speed:
  `search_radius = lookahead_distance = poll_distance = speed × time_between_polls`
  (starting value 12 seconds), with a minimum lookahead distance (150 m), a maximum search
  radius (1 km) and a fallback timer (60 s) so a stopped car still polls. The server returns
  the next poll distance in every response. (Replaces the fixed "every ~300 m";
  design section 5.)
- **Projected lookahead.** The client sends its raw position, heading,
  and speed; the server projects a point ahead along the heading
  (PostGIS `ST_Project`) and searches around *that* point, not the
  vehicle's literal current position — so warnings don't feel laggy.
- **Per-device authentication on every request** (reads included), via
  `device_id` + API key, checked against a `devices` table (HMAC hash of
  the key, never stored raw; design section 7).

## Functional Requirements
1. Edge device posts auto-detected hazards to the server (`POST /v1/hazards`),
   sent opportunistically as detections occur.
2. Mobile app allows users to manually submit a hazard report.
3. Server deduplicates reports of the same real-world hazard: a new report
   within the tag's deduplication radius (25–150 m depending on
   `event_tag`) of an existing non-expired hazard of the same tag
   increments `confirmations` and resets `expires_at`, rather than
   creating a duplicate row. Original coordinates remain authoritative
   (not averaged). No match → new row inserted. Each device counts at
   most once per hazard.
4. Each hazard has a **confidence score** (from the detecting device's
   model output, set once at insert; null for manual reports) and a
   separate **confirmation count** (independent devices that reported
   it) — tracked separately, never merged.
5. Each hazard type has a **time-to-live (TTL)**; hazards expire
   automatically unless re-confirmed, which resets the TTL. TTLs are
   purely passive expiry.
   Starting values: accident — 90 minutes; checkpost — 4 hours;
   debris — 1 day; pothole — 14 days (design section 4).
6. Vehicles periodically query the server for hazards within a lookahead
   radius of their projected position (`GET /v1/hazards`), using
   server-side `ST_Project` + `ST_DWithin`.
7. Mobile app displays a hazard map and the user's contribution score.
8. Confirmed/expired hazards are removed from active alerts — no
   background push; hazards simply stop being returned by `GET /v1/hazards`
   once expired.
9. Devices check in at boot via a handshake, so the server can confirm
   they're not revoked. (Polling values come with every `GET /v1/hazards`
   response instead of the handshake.)

## Technical Approach

### Server
Go backend, stateless, containerized, against Postgres + PostGIS (self-run
rather than managed Postgres, for cost, simplicity and control over compute
specs during load testing). Built in three stages: (1) the developer's
laptop with docker compose, (2) a single Azure VM, (3) multi-server
Kubernetes, where the database moves to its own VM.

### Schema (MVP — 3 tables, design section 9)
**`hazards`**
- `event_tag` (pothole/checkpost/accident/debris/etc.)
- `location` (GEOGRAPHY Point, GiST-indexed)
- `heading_degrees` (reporting vehicle's bearing, nullable)
- `confidence` (0.0–1.0, from reporting device's detection, set once at insert; null for manual reports)
- `source` (`auto` or `manual`, derived from the device type)
- `reported_by` (device_id of original reporter)
- `confirmations` (number of independent devices that reported it, the first included)
- `created_at`, `expires_at` (TTL, reset on confirmation)

**`hazard_confirmations`**
- one row per (hazard, device) pair, so each device counts once

**`devices`**
- `device_id`
- `api_key_hash` (hash only, never raw)
- `device_type`
- `revoked`

### API
Full request and response bodies: design section 8.
- `POST /v1/devices/register` — a phone gets its own `device_id` + API key.
- `POST /v1/devices/handshake` — boot-time check-in; confirms the device
  is valid and isn't revoked.
- `POST /v1/hazards` — device posts a detected/reported hazard (auth via
  `device_id` + API key against the `devices` table). Server performs
  deduplication on write as described above.
- `GET /v1/hazards?latitude=&longitude=&heading_degrees=&speed_kilometers_per_hour=`
  — server computes the lookahead distance and search radius from the
  speed, projects a point via PostGIS `ST_Project`, runs an `ST_DWithin`
  search, and returns hazards within range plus the next poll distance.
- `GET /v1/hazards/map` — hazards inside a map rectangle, for the app.
- `GET /v1/devices/me/contributions` — the calling device's contribution counts.

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
- **k6 scripts** — simulate a fleet of virtual devices (speed-based
  polling, bursts of multiple devices reporting the same hazard) against
  the deployed API, standing in for hardware until Feature 1's CV
  pipeline output is ready. Same scripts double as the load-generation
  tool for the scalability demonstration. Swapped for real device traffic
  later with zero API changes, since the client contract (raw
  position/heading/speed in, hazards out) doesn't change based on what's
  behind the load balancer.

## KPIs (from project deliverables)
- Hazard sharing time (detection → distributed to other vehicles): ≤ 3 s.
  Because vehicles pull, this is measured as two indicators (design
  section 11): a report is queryable on the server within 3 s, and a
  vehicle sees it within one time between polls at worst.
- Hazard broadcast radius: 1 km — now the *maximum* search radius; the
  actual radius grows with speed (design section 5).

## Dependencies / Components
- Cloud server (Azure VM for MVP; load-balanced multi-replica setup for
  scalability demonstration)
- SQL/PostGIS database
- Mobile application (to be built)
- Networking/connectivity on edge device (hotspot/cellular)

## Open Questions
Most of these were settled in the backend design (`adas-cloud/docs/design.md`);
the answers are noted inline.
- Where should the lookahead-projection math live — client-side (device
  sends raw GPS/heading, does its own projection) or server-side
  (**decided: server-side**, per appendix — client sends raw state,
  server projects via PostGIS `ST_Project`).
- What's the actual per-`event_tag` TTL table? **Decided:** design
  section 4 (starting values above).
- Whether the deduplication radius should vary by `event_tag`?
  **Decided: yes** — accident 150 m, checkpost 100 m, debris 50 m,
  pothole 25 m (design section 4).
- How do we prevent spam/false manual reports from tanking trust in the
  system? **Decided:** per-device and per-IP rate limits, each device
  counts once per hazard, manual reports marked by `source` (design
  sections 6, 7, 10). Fake identities through phone registration remain
  a known, limited risk.
- Should a Feature 3 crash detection event also get posted into this same
  `hazards` table (e.g. `event_tag: "accident"`) so other vehicles see it
  automatically? Currently not connected in the deck — worth deciding.
  (The backend supports it: `accident` is allowed for Jetson units; task
  C-109.)
- Whether `GET /v1/hazards` needs full device auth or something lighter?
  **Decided: full authentication** (design section 7).
- Whether `heading_degrees` gets used for anything beyond the current
  projected-lookahead use case (e.g. future route-corridor filtering).