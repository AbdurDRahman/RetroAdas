# adas-cloud — Backend Design

**Status:** draft, being finalized in task C-001. Sections marked **Agreed** are settled;
sections marked **To discuss** are next in line. When every section is agreed, this document
replaces the two early drafts (`../api_design.md`, `../database_schema.md`), which are partly
outdated.

This document describes *what* the backend does and *why*. Exact table definitions and SQL
queries are designed later, task by task (C-1xx), starting from this document.

---

## 1. Purpose and scope — Agreed

The backend lets one RetroADAS vehicle's knowledge of a road hazard reach every other nearby
vehicle and the mobile app. It stores hazard reports, merges duplicate reports of the same
real-world hazard, expires hazards that nobody confirms anymore, and answers "what hazards are
ahead of me?" for a moving vehicle.

In scope: hazard reporting, deduplication, expiry, nearby-hazard search, device identity and
authentication, mobile-app support (manual reports, hazard map, contribution score), abuse
controls, and the deployment path from laptop to a multi-server Kubernetes cluster.

Out of scope for now: trips, driver scoring, live vehicle tracking, server push notifications.

---

## 2. Design principles — Agreed

1. **Stateless request/response.** No WebSockets, no per-device session state, no in-memory
   state shared between server replicas. Any replica can answer any request, which is what
   lets the backend scale horizontally behind a load balancer.
2. **Pull, not push.** Vehicles and the app ask for hazards; the server never pushes them.
3. **One generic API for every client.** The endpoints don't know or care whether a report came
   from the crash detector, the mobile app, or (later) the camera pipeline. The caller is
   identified by its device and `device_type`; everything else is the same request shape.
4. **The server owns the policy, the client owns the observation.** Clients report *what* they
   saw and *where*. The server decides how long it lives (time to live), what counts as a
   duplicate, and how far ahead to search. Clients never choose these values themselves.
5. **Geographic math lives on the server, in one place.** The client sends its raw position,
   heading and speed; the server does the projection and search with PostGIS. Getting it right
   once beats re-implementing it on every embedded device.
6. **API keys are stored only as hashes**, never raw, never logged.

---

## 3. Clients and producers — Agreed

| Client | `device_type` | What it sends | How it authenticates |
|---|---|---|---|
| Jetson unit (in the car) | `jetson_unit` | Hazards it is told about by the crash detector (accidents, potholes); later, camera detections. Polls for hazards while driving. | Provisioned `device_id` + API key |
| Crash detector (ESP32) | — (never talks to the cloud) | Signals the Jetson, which posts on its behalf. See `../../adas-automatic-crash-detection/CLAUDE.md`. | Through the Jetson's identity |
| Mobile app | `mobile_app` | Manual hazard reports. Reads the hazard map and its own contribution score. | Self-registered `device_id` + API key (see section 7) |
| Camera pipeline (future) | `jetson_unit` | Camera detections (pedestrian, stationary vehicle, congestion) | Same as the Jetson unit |

Adding a new producer later should need only new rows in the event-tag registry (section 4),
not new endpoints.

---

## 4. Event-tag registry — Agreed in principle, values To discuss

The API is generic, but the rules for each kind of hazard are not: a pothole lasts weeks, a
pedestrian on the road lasts minutes; a pothole is a pinpoint, an accident covers more road.
So each `event_tag` has a server-side policy entry:

| Field | Meaning |
|---|---|
| `event_tag` | Name of the hazard type, e.g. `pothole`. |
| `time_to_live` | How long a hazard stays live after its last confirmation. |
| `dedup_radius_meters` | How close a new report must be to count as the same hazard. |
| `allowed_sources` | Which `device_type`s may post this tag (e.g. only `mobile_app` for `checkpost`). |
| `default_confidence` | Used when the producer has no detector score (e.g. a manual report). |

Every report also records its **source** — `auto` (a detector) or `manual` (a person) — so
trust can be weighted differently later.

**To discuss:**
- Initial list of tags and their values. Starting point from the drafts: `checkpost` (a few
  hours), `accident` (1–2 hours), `pothole` / `debris` (days to weeks).
- Registry stored as a database table or as server configuration.
- What `confidence` means for each source: detector score for the crash detector and camera,
  `default_confidence` for manual reports.

---

## 5. Polling, lookahead and search radius — Agreed

**The rule:**

```
search_radius = lookahead_distance = poll_distance = speed × time_between_polls
```

with three guard rails:

- a **minimum lookahead distance** (starting value 150 metres) so slow vehicles still look far
  enough ahead;
- a **maximum search radius** of 1 kilometre;
- a **fallback timer**: a vehicle polls after at most a fixed time (starting value 60 seconds)
  even when it isn't moving, so a car stuck in traffic still learns about the accident
  causing the jam.

**Why the three distances are equal.** Each search is a circle centred `lookahead_distance`
ahead of the vehicle with radius `search_radius`, so along the road it covers from
`lookahead_distance − search_radius` to `lookahead_distance + search_radius` ahead.

- *No gaps between searches:* the vehicle must poll again before it has driven more than twice
  the search radius.
- *Hazards arrive in time:* a hazard must be fetched while it is still farther ahead than the
  stopping distance plus the distance driven while the request is in flight.
- *Nothing behind the vehicle:* the search radius must not exceed the lookahead distance, or
  the circle reaches back to hazards already passed.

Setting all three equal satisfies every condition: each search covers from the vehicle's
position to twice the lookahead distance ahead, and every hazard is fetched at least one
lookahead distance before the vehicle reaches it.

**Example values** with `time_between_polls` = 12 seconds:

| Speed | Lookahead distance = search radius = poll distance | Time between polls |
|---|---|---|
| 30 km/h | 150 metres (minimum) | 18 seconds |
| 60 km/h | 200 metres | 12 seconds |
| 100 km/h | 333 metres | 12 seconds |
| 120 km/h | 400 metres | 12 seconds |

`time_between_polls`, the minimum lookahead distance, the maximum search radius and the
fallback timer are **server configuration, not architecture** — they can be tuned at any time.
At 12 seconds, 1,000 moving vehicles produce roughly 83 requests per second.

**Who computes what.** The vehicle sends its position, heading and speed. The server computes
the lookahead distance and search radius, and returns the **next poll distance and the fallback
time** in every response. The vehicle only has to follow those two numbers, so the polling
policy can change without updating any device. (This replaces the drafts' "no poll interval in
the response".)

**Changes to earlier principles, recorded on purpose:**
- "Distance-triggered, not timer-triggered" becomes: *while moving, the poll distance grows with
  speed, so the time between polls stays roughly constant; at low speed the minimum distance
  applies; when stopped, the fallback timer applies.*
- The "1 km broadcast radius" becomes a maximum, not a fixed radius.

**Known limitations (later refinement, not MVP):** a circle also catches parallel roads and the
opposite carriageway, and doesn't follow curves. A heading cone or a route corridor would fix
this.

**To discuss:** none for the model itself. Whether a GPS heading is trustworthy at very low
speed is handled by the minimum lookahead distance; confirm during C-103.

---

## 6. Hazard lifecycle — Partly agreed

**Agreed:**
- **Dedup on write.** A new report of the same `event_tag` within that tag's dedup radius of a
  live (not yet expired) hazard confirms it instead of creating a new hazard. If several match,
  the **nearest** one is confirmed.
- **A confirmation is from an independent device.** Each device counts at most once per hazard.
  A device re-reporting a hazard it already reported or confirmed changes nothing: no extra
  confirmation and no expiry reset. (Bug found in the drafts; fixed in C-102.)
- **A confirmation** increments `confirmations` and resets the expiry to now + the tag's time
  to live. The original location stays authoritative (no averaging, to avoid drift).
- **`confidence` and `confirmations` are separate measures** and are never merged: one is "how
  sure was the detector", the other is "how many independent devices agree".
- **Expiry is passive.** Expired hazards simply stop being returned by searches.

**To discuss:**
- **Cleanup of expired hazards:** delete them, or archive them? The contribution score
  (section 8) needs history, so deleting outright may lose it.
- **Revoked devices:** do their existing hazards stay, or are they hidden?

---

## 7. Identity and authentication — To discuss

**Agreed:** every write is authenticated with `X-Device-Id` + `X-Api-Key` headers against the
devices registry; the key is stored only as a hash. Unknown device or wrong key → 401; revoked
device → 403. The handshake at boot confirms the device is valid and returns its configuration.

**To discuss:**
- **Jetson provisioning:** a command-line tool creates a `device_id` + API key and prints the
  key once (C-107). Format of `device_id` and key.
- **Mobile-app registration:** phones can't be pre-provisioned, and a key built into the app
  can be extracted. Proposal: a registration endpoint that issues a key to each new install,
  rate limited (C-114).
- **Does `GET /v1/hazards` need full authentication?** Read-only, so lower risk — but an open
  endpoint lets anyone scrape the whole hazard map.
- **Hashing method:** a deliberately slow hash (bcrypt) on every request costs tens of
  milliseconds; a fast keyed hash is usually enough for long random keys (C-101).

---

## 8. Endpoints — To discuss

| # | Endpoint | Purpose | Status |
|---|---|---|---|
| 1 | `POST /v1/devices/handshake` | Boot-time check-in; returns configuration | Shape from drafts; config fields to confirm |
| 2 | `POST /v1/hazards` | Report a hazard (any producer) | Shape from drafts; add `source` |
| 3 | `GET /v1/hazards` | Hazards ahead of a moving vehicle | Shape from drafts; response gains next poll distance / fallback time |
| 4 | `GET /v1/hazards/map` (name to decide) | Hazards in a map area for the app (no lookahead) | New |
| 5 | `GET /v1/devices/me/stats` (name to decide) | The calling device's contribution score | New |
| 6 | `POST /v1/devices/register` (name to decide) | Mobile-app self-registration | New |
| — | `GET /healthz`, `GET /readyz` | Liveness and readiness for containers / Kubernetes | New, no authentication |

Request and response bodies are written out here once each endpoint is agreed.

---

## 9. Data entities — To discuss

Entities only; columns and SQL come later in each task.

| Entity | Holds | Notes |
|---|---|---|
| `devices` | Device identity, key hash, `device_type`, revoked flag | Exists (migration 001) |
| `hazards` | One row per real-world hazard: tag, location, confidence, source, reporter, confirmations, created / expires times | Exists (migration 002); needs `source` and database-level checks (confidence 0–1, heading 0–360, tag must exist in the registry) |
| `hazard_confirmations` | Which device confirmed which hazard, and when | New — enforces "one device counts once" and feeds the contribution score |
| `event_tags` | The registry from section 4 | New, if stored as a table |

---

## 10. Abuse controls — To discuss

- Rate limiting on hazard reports per device. Must work with several server replicas, so not
  in-memory: either counted in the database, or enforced at the load balancer / ingress.
- Rate limiting on mobile-app registration.
- One device can't inflate confirmations (already covered by section 6).

---

## 11. Key performance indicators — Agreed

The original "detection → distributed to other vehicles ≤ 3 seconds" can't be met by a pull
design, because another vehicle only hears about a hazard on its next poll. It is split into two
measurable numbers:

| Indicator | Target | What controls it |
|---|---|---|
| Report → queryable on the server | ≤ 3 seconds | Server and network; measured with k6 (C-207) |
| Worst-case staleness for a polling vehicle | ≤ time between polls (≤ fallback timer when stopped) | The polling model in section 5 |
| Search radius | up to 1 kilometre | Maximum search radius in section 5 |

---

## 12. Naming — Agreed

Every name is written in full words — in Go code, JSON fields, query parameters and database
columns alike. Only universal acronyms stay short: `ID`, `URL`, `API`, `HTTP`, `JSON`, `SQL`.
Units are part of the name and spelled out (`_meters`, `_degrees`, `_kilometers_per_hour`).
JSON fields, query parameters and database columns use `snake_case`; Go uses its usual
`CamelCase`.

Renamed while nothing is built on them yet (C-006). The early drafts (`../api_design.md`,
`../database_schema.md`) still show the old names.

**JSON fields and query parameters**

| Old | New |
|---|---|
| `lat` | `latitude` |
| `lng` | `longitude` |
| `heading_deg` | `heading_degrees` |
| `speed_kmh` | `speed_kilometers_per_hour` |
| `radius_m` | `radius_meters` |
| `distance_m` | `distance_meters` |
| `poll_interval_distance_m` | `poll_distance_meters` |
| `lookahead_m` | `lookahead_distance_meters` |
| `hazard_radius_m` | `search_radius_meters` |

Unchanged (already full words or allowed acronyms): `device_id`, `device_type`, `event_tag`,
`confidence`, `confirmations`, `status`, `id`, `expires_at`, and the headers `X-Device-Id`,
`X-Api-Key`.

**Database columns and indexes**

| Table | Old | New |
|---|---|---|
| `hazards` | `heading_deg` | `heading_degrees` |
| `hazards` | index `hazards_geo_idx` | `hazards_location_index` |
| `hazards` | index `hazards_expiry_idx` | `hazards_expires_at_index` |
| `hazards` | index `hazards_tag_idx` | `hazards_event_tag_index` |

All other columns are already full words (`device_id`, `api_key_hash`, `device_type`,
`created_at`, `revoked`, `event_tag`, `location`, `confidence`, `reported_by`,
`confirmations`, `expires_at`). The two existing migrations are edited in place — nothing has
been deployed, so no follow-up migration is needed; local databases are reset with
`docker compose down -v`.

---

## 13. Decision log

| Decision | Where |
|---|---|
| Speed-based polling: `search_radius = lookahead_distance = poll_distance = speed × time_between_polls`, with a minimum, a maximum and a fallback timer | Section 5 |
| Polling numbers are configuration, not architecture | Section 5 |
| One generic API for every producer; per-tag policy lives on the server | Sections 2–4 |
| Each device confirms a hazard at most once; nearest match wins | Section 6 |
| Sharing-time indicator split into server latency + polling staleness | Section 11 |
| Full-word names everywhere, including JSON fields and database columns; only `ID`, `URL`, `API`, `HTTP`, `JSON`, `SQL` stay short | Section 12 |
