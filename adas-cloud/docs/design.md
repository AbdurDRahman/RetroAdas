# adas-cloud — Backend Design

**Status:** agreed (task C-001). This document replaces the two early drafts
(`../api_design.md`, `../database_schema.md`), which are kept only for history. Changing a
decision here is a new task, recorded in `DONE.md` and in the decision log (section 13).

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

## 4. Event-tag registry — Agreed

The API is generic, but the rules for each kind of hazard are not: a pothole lasts weeks, a
pedestrian on the road lasts minutes; a pothole is a pinpoint, an accident covers more road.
So each `event_tag` has a server-side policy entry:

| Field | Meaning |
|---|---|
| `event_tag` | Name of the hazard type, e.g. `pothole`. |
| `time_to_live` | How long a hazard stays live after its last confirmation. |
| `deduplication_radius_meters` | How close a new report must be to count as the same hazard. |
| `allowed_device_types` | Which `device_type`s may post this tag (e.g. only `mobile_app` for `checkpost`). |

**Starting values:**

| `event_tag` | Time to live | Deduplication radius | Allowed device types |
|---|---|---|---|
| `accident` | 90 minutes | 150 meters | `jetson_unit`, `mobile_app` |
| `pothole` | 14 days | 25 meters | `jetson_unit`, `mobile_app` |
| `checkpost` | 4 hours | 100 meters | `mobile_app` |
| `debris` | 1 day | 50 meters | `mobile_app` |

- **Time to live** counts from the last confirmation, so a hazard that keeps being reported
  stays live, and disappears one time to live after the last report.
- **Deduplication radius** follows how much road the hazard covers, plus GPS error (a phone is
  off by about 5–10 meters). Too large merges two separate hazards; too small shows one hazard
  twice.
- **Allowed device types:** only a person can recognize a checkpost or debris today. When the
  camera pipeline arrives, `jetson_unit` is added to those rows — no other change.

**Where the registry lives: in Go code, not in the database.** A single file
(e.g. `internal/hazards/event_tags.go`) holds the table above. The API is the only writer of
hazards, so the handler is the gatekeeper:
- unknown `event_tag` → `400 Bad Request`;
- `event_tag` not allowed for the device's `device_type` → `403 Forbidden`.

The deduplication query receives the tag's radius and time to live as query parameters, so it
is still a single statement. Trade-off accepted: changing a value or adding a tag means a
rebuild and redeploy instead of an SQL update, and there is no foreign key protecting the
`event_tag` column against hand-written SQL. During a Stage 3 rolling update, old and new
replicas may briefly disagree on a value — harmless.

**Source and confidence.** Every report records its **source** — `auto` (a detector) or
`manual` (a person):
- `auto`: `confidence` is the detector's own score, between 0 and 1, sent by the device.
- `manual`: `confidence` is **null** — no detector measured it, and a made-up default would
  look like a real score. Any value the client sends is ignored.
- A check constraint enforces this pairing in the database (`manual` ⇔ `confidence` is null;
  `auto` ⇒ `confidence` between 0 and 1).
- Clients must handle `"confidence": null` — the app shows "reported by a person" instead of a
  percentage. Anything that sorts or filters by confidence has to decide what null means.
- A hazard keeps the confidence of its first report; confirmations never change it (section 6).

The mobile app needs the tag list for its picker — how it gets it is decided in section 8.

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

**Checked during C-103:** whether a GPS heading is trustworthy at very low speed. The minimum
lookahead distance is expected to cover it.

---

## 6. Hazard lifecycle — Agreed

- **Deduplication on write.** A new report of the same `event_tag` within that tag's
  deduplication radius of a live (not yet expired) hazard confirms it instead of creating a new
  hazard. If several match, the **nearest** one is confirmed.
- **A confirmation is from an independent device.** Each device counts at most once per hazard.
  A device re-reporting a hazard it already reported or confirmed changes nothing: no extra
  confirmation and no expiry reset. (Bug found in the drafts; fixed in C-102.)
- **`confirmations` counts the devices that have reported a hazard, the first reporter
  included** — 1 when the hazard is created. A confirmation increments it and resets the
  expiry to now + the tag's time to live. The original location stays authoritative (no averaging, to avoid drift).
- **`confidence` and `confirmations` are separate measures** and are never merged: one is "how
  sure was the detector", the other is "how many independent devices agree".
- **How a repeat is recognized.** A report carries no hazard ID. The server first finds the
  hazard *by location* (same tag, within the deduplication radius, still live, nearest), and
  only then checks whether this device already appears in `hazard_confirmations` for that
  hazard. So a second report from the same device resolves to the same hazard ID and gets
  `already_reported`.
- **Client responsibility: report each hazard once.** A detector sees the same pothole in many
  consecutive frames; without a client-side check the Jetson would send a request per frame.
  Clients should report once per tracked object (for example a tracker ID such as ByteTrack's),
  or, as a simpler fallback, skip a detection when the same tag was reported within its
  deduplication radius in the last few minutes. Counting frames is fragile, because frame rate
  and speed vary. The server check stays as the safety net: clients can be buggy or malicious,
  and the same car passes the same pothole again on another drive.
- **Expiry is passive.** Expired hazards simply stop being returned by searches.
- **Expired hazards are kept, not deleted.** For now they stay in `hazards`; searches skip them
  through the `expires_at` filter and index. At the end of the project they move to a separate
  archive store (task C-307), which keeps the history the contribution score (section 8) and
  the evaluation results need.
- **Revoking a device does not touch its hazards.** Hazards it already reported stay until they
  expire, even if fake: every tag's time to live is bounded (14 days at most), other devices
  may have confirmed them, and revoking already stops any new reports.

---

## 7. Identity and authentication — Agreed

### The model

Every device — Jetson unit or phone — has its own `device_id` and API key, sent on **every**
request (reads and writes) in the `X-Device-Id` and `X-Api-Key` headers. Only `/healthz` and
`/readyz` and the phone registration endpoint are open. The handshake at boot confirms the
device is valid and returns its configuration.

**Checking a request** (C-101), in this order:
1. Look up `device_id`. If it doesn't exist, **still compute a dummy hash**, then answer `401` —
   so an unknown device takes as long as a wrong key and nobody can discover which IDs exist.
2. Hash the presented key and compare it in constant time (`hmac.Equal`). Wrong key → `401`
   (the same response as an unknown device).
3. Revoked device → `403`. Only reachable with the correct key, so it reveals nothing to an
   outsider. Revocation takes effect on the next request.

**Formats** (C-107):
- `device_id`: a type prefix plus 8 random hexadecimal characters — `jetson-4f9c2a1b`,
  `phone-91d0e7c3`. Not secret; the prefix makes logs readable.
- API key: 32 random bytes from `crypto/rand`, written as 43 characters of URL-safe base64.
  Shown **once** (by the provisioning tool or the registration response) and never stored or
  logged raw.

**Hashing** (C-101): HMAC-SHA256 with a server-side secret ("pepper") held in the environment
variable `API_KEY_PEPPER` — never in the database or in git; a Kubernetes Secret in Stage 3.
Changing the pepper invalidates every key.

**Jetson units** are provisioned with a command-line tool (C-107). **Phones** register
themselves: `POST /v1/devices/register` needs no key and returns a new `device_id` + API key
once (C-114); the app keeps them in the phone's secure storage (Android Keystore).
Registration is rate limited (section 10). Phones get the same trust as Jetson units — manual
reports are already marked by `source` and a null `confidence` (section 4).

### Security analysis

What we protect: the integrity of the hazard map, the availability of the server, the API
keys, and drivers' location privacy. The system only *warns* — it never controls the car — so
a fake hazard is a false warning, not a dangerous action. There is no delete or edit endpoint:
an attacker can add false data but cannot remove real hazards.

| Threat | Status | Why |
|---|---|---|
| Guessing a key | Protected | 256 random bits — impossible to guess at any request rate. |
| Discovering which device IDs exist | Protected | Same `401` for unknown device and wrong key, plus the dummy hash. |
| Someone listening on the network | Protected **with HTTPS only** | The key travels on every request. **HTTPS is mandatory beyond the laptop** (C-204). |
| Database leak (stolen backup, SQL injection reading tables) | Protected | Only HMAC hashes are stored and the pepper isn't in the database. SQL injection itself is blocked by parameterized queries. |
| Key extracted from one device | Limited | The attacker can do only what that device's type is allowed to; revocation is immediate. |
| Flooding the server (denial of service) | Partly | A fast hash costs microseconds per bad request (bcrypt would cost 50–100 ms of CPU each — a cheap way to overload us); timeouts, body size limits (C-106) and rate limits (section 10). |
| **Fake identities through phone registration (Sybil attack)** | **Weakest point — damage limited, not prevented** | Someone can register many "phones" and confirm their own fake hazard many times. Limits: registration rate limit, manual reports marked as such, every fake expires (14 days at most), suspicious clusters in `hazard_confirmations` can be found and revoked. |
| Full server compromise | Out of reach of any design | Operational defence: secrets out of git, patching, limited access. |

**Why HMAC and not bcrypt.** Slow hashes exist to protect weak, human-chosen passwords whose
hashes can be brute-forced after a leak. A 256-bit random key can't be brute-forced at any
speed, so bcrypt would add no protection — only latency on every request (visible in the k6
results) and a denial-of-service lever. Hashed random API tokens checked with a fast hash is
the standard pattern (GitHub and Stripe tokens work this way). The pepper is defence in depth:
a leaked database alone is worthless.

**Alternatives rejected for now:**
- One shared key built into the app — extractable, one leak exposes everyone, no per-phone
  revocation.
- User accounts (email and password, or Google sign-in) — slightly harder to fake, but adds
  login screens, password storage and personal data to protect; per-install keys are anonymous.
- Short-lived signed tokens (JSON Web Tokens) — save the database lookup, but a revoked device
  keeps working until its token expires. Our lookup is one primary-key read and revocation is
  immediate; it also keeps the API stateless (any replica can check any key).
- Client certificates (mutual TLS) for Jetson units — strongest, since the secret never crosses
  the network, but needs a certificate authority, rotation and ingress configuration.

**Location privacy.** Because reads are authenticated, every poll links a device to a position
— a movement trail every time between polls. `GET` writes nothing to the database, so the
trail could only exist in logs. **Rule for Stages 2 and 3:** never log coordinates together
with a device ID, and never store poll locations. (Statement logging, `log_statement=all`, is
development only.) Anonymous reads would avoid the link but allow scraping and remove
per-device rate limiting; the rule keeps most of both.

**Future work** (out of scope for this project): Google Play Integrity attestation or verified
accounts to stop fake identities; hardware-protected keys (TPM) or client certificates for
Jetson units.

---

## 8. Endpoints — Agreed

| # | Endpoint | Purpose | Key needed |
|---|---|---|---|
| 1 | `POST /v1/devices/register` | A phone gets its own `device_id` + API key (section 7) | No (rate limited) |
| 2 | `POST /v1/devices/handshake` | Boot-time identity check | Yes |
| 3 | `POST /v1/hazards` | Report a hazard (any producer) | Yes |
| 4 | `GET /v1/hazards` | Hazards ahead of a moving vehicle (polling) | Yes |
| 5 | `GET /v1/hazards/map` | Hazards inside a map rectangle, for the app's map screen | Yes |
| 6 | `GET /v1/devices/me/contributions` | The calling device's contribution counts | Yes |
| — | `GET /healthz`, `GET /readyz` | Liveness and readiness for containers / Kubernetes | No |

### Shared conventions

- JSON bodies, `snake_case` names. Coordinates in decimal degrees (WGS 84). Times in UTC,
  ISO 8601 (`"2026-10-15T09:30:00Z"`). Hazard `id` is a JSON number.
- Authentication: `X-Device-Id` + `X-Api-Key` headers (section 7).
- **One error shape** for every error, so clients branch on `code` and can show `message`:

  ```json
  { "error": { "code": "unknown_event_tag", "message": "event_tag \"flood\" is not a known hazard type" } }
  ```

| HTTP status | When | `code` values |
|---|---|---|
| `400` | Invalid input | `invalid_request`, `unknown_event_tag`, `area_too_large` |
| `401` | Unknown device or wrong key (identical on purpose) | `unauthorized` |
| `403` | Revoked device, or tag not allowed for this device type | `device_revoked`, `event_tag_not_allowed` |
| `413` | Request body too large | `request_too_large` |
| `429` | Rate limited; a `Retry-After` header says when to try again | `rate_limited` |
| `500` | Server error | `internal_error` |

**Event-tag lists live in the clients.** Each client hardcodes the tags it can report (the
app needs an icon and a label per tag anyway; the Jetson posts only what its detectors
produce). The server stays the gatekeeper with `400 unknown_event_tag` and
`403 event_tag_not_allowed`, so a mismatch shows up as a clear error.

### 1. `POST /v1/devices/register`

Request: empty body. Response `201 Created`:

```json
{
  "device_id": "phone-91d0e7c3",
  "device_type": "mobile_app",
  "api_key": "q3Zr8v...43 characters...Xw"
}
```

The only time `api_key` is ever shown. The app stores it in secure storage, then calls the
handshake. Errors: `429`.

### 2. `POST /v1/devices/handshake`

Request: empty body (the headers identify the device). Response `200 OK`:

```json
{ "device_id": "jetson-4f9c2a1b", "device_type": "jetson_unit" }
```

Only an identity check at boot — "is my key valid, am I revoked?" — so a misconfigured device
finds out immediately (the Jetson can show "device not registered", the app "registration
lost") instead of failing quietly on its first poll. It returns **no polling values**: the
poll distance depends on the current speed, so it comes back in every `GET /v1/hazards`
response instead (section 5). Errors: `401`, `403`.

### 3. `POST /v1/hazards`

Request:

```json
{
  "event_tag": "pothole",
  "latitude": 33.6850,
  "longitude": 73.0490,
  "heading_degrees": 140.0,
  "confidence": 0.91
}
```

- `latitude` between −90 and 90, `longitude` between −180 and 180 — required.
- `heading_degrees` — **optional**, from 0 up to (not including) 360. Someone on foot
  reporting debris may have no meaningful heading.
- `confidence` — required and between 0 and 1 for `auto` reports; ignored for `manual`
  reports, which store null (section 4).
- **`source` is not sent.** The server derives it from the device type: `jetson_unit` → `auto`,
  `mobile_app` → `manual`. A phone can't pose as a detector with a high confidence.
- **No timestamp.** The server's clock is the truth: a report counts as "now", so devices
  should send reports straight away rather than saving them up.

Response — the same shape every time; `status` says what happened:

```json
{ "id": 9123, "status": "created", "confirmations": 1, "expires_at": "2026-10-29T09:30:00Z" }
```

| `status` | HTTP status | Meaning |
|---|---|---|
| `created` | `201` | A new hazard. |
| `confirmed` | `200` | Matched an existing hazard: `confirmations` went up and the expiry was reset. |
| `already_reported` | `200` | This device already reported or confirmed this hazard; nothing changed (section 6). |

Errors: `400` (`invalid_request`, `unknown_event_tag`), `401`, `403` (`device_revoked`,
`event_tag_not_allowed`), `413`, `429`.

### 4. `GET /v1/hazards`

```
GET /v1/hazards?latitude=33.6844&longitude=73.0479&heading_degrees=142.0&speed_kilometers_per_hour=38.0
```

All four parameters are required. **The client does not send a radius**: the server computes
the lookahead distance and search radius from the speed (section 5). Letting the client choose
would break the rule and let anyone ask for a huge circle.

Response `200 OK`:

```json
{
  "hazards": [
    {
      "id": 9123,
      "event_tag": "pothole",
      "latitude": 33.6850,
      "longitude": 73.0490,
      "distance_meters": 95,
      "confidence": 0.91,
      "confirmations": 4
    }
  ],
  "next_poll": {
    "poll_distance_meters": 158,
    "fallback_seconds": 60
  }
}
```

- `distance_meters` — whole meters from the vehicle's **current position** (not the projected
  point), so the device can say "pothole in 95 meters".
- Nearest first, **at most 100 hazards**.
- `confidence` is null for manual reports.
- `next_poll` — poll again after driving `poll_distance_meters`, or after `fallback_seconds`,
  whichever comes first. These are the only polling values a device ever receives; the
  lookahead distance and search radius stay inside the server.

Errors: `400`, `401`, `403`, `429`.

### 5. `GET /v1/hazards/map`

For the app's map screen: every live hazard inside the rectangle the user is looking at. It is
not tied to the phone's position and has no lookahead, which is why it can't reuse
`GET /v1/hazards`.

```
GET /v1/hazards/map?minimum_latitude=33.68&minimum_longitude=73.04&maximum_latitude=33.70&maximum_longitude=73.06
```

Response `200 OK`:

```json
{
  "hazards": [
    {
      "id": 9123,
      "event_tag": "pothole",
      "latitude": 33.6850,
      "longitude": 73.0490,
      "confidence": 0.91,
      "confirmations": 4,
      "created_at": "2026-10-15T08:10:00Z",
      "expires_at": "2026-10-29T09:30:00Z"
    }
  ]
}
```

- The rectangle may be at most about **20 × 20 kilometers** (`400 area_too_large` beyond
  that), and at most **500 hazards** come back — so one request can't download the whole map.
- `created_at` lets the app show "reported 2 hours ago".

Errors: `400`, `401`, `403`, `429`.

### 6. `GET /v1/devices/me/contributions`

Response `200 OK`:

```json
{
  "device_id": "phone-91d0e7c3",
  "hazards_reported": 12,
  "hazards_confirmed": 30,
  "reports_confirmed_by_others": 8
}
```

- `hazards_reported` — hazards this device created.
- `hazards_confirmed` — other devices' hazards this device confirmed.
- `reports_confirmed_by_others` — this device's hazards that at least one other device
  confirmed (a sign its reports are real).

Raw counts only, from `hazards` and `hazard_confirmations`. The score formula is decided in
C-111; a `score` field can be added later without breaking clients. Errors: `401`, `403`.

### Health

- `GET /healthz` → `200 {"status": "ok"}` while the process is up.
- `GET /readyz` → `200 {"status": "ok"}`, or `503 {"status": "database_unreachable"}`.

---

## 9. Data entities — Agreed

Which tables exist and why; exact columns and SQL are designed in each task.

| Table | Holds | Changes from today's migrations |
|---|---|---|
| `devices` | `device_id` (e.g. `jetson-4f9c2a1b`), the HMAC hash of the API key, `device_type`, `created_at`, `revoked` | Exists (migration 001). Add a check: `device_type` is `jetson_unit` or `mobile_app`. |
| `hazards` | One row per real-world hazard: `event_tag`, `location`, optional `heading_degrees`, `confidence` (null for manual), `source`, `reported_by`, `confirmations`, `created_at`, `expires_at` | Exists (migration 002). Add `source` and the checks (`source`/`confidence` pairing from section 4, heading from 0 up to 360). `confidence` becomes nullable; `confirmations` starts at 1 (section 6). `id` switches from `BIGSERIAL` to `GENERATED ALWAYS AS IDENTITY`. Unknown tags are rejected by the handler, not the database. |
| `hazard_confirmations` | One row per (hazard, device) pair, with the time | New (C-102). |
| Report rate-limit counter | Reports per device per time window | New (C-110, section 10). |

**`hazards.id`** is generated by Postgres from an identity sequence: the insert never sets it
and gets it back with `RETURNING id`. Taking the next number is atomic inside Postgres, so
replicas inserting at the same instant never collide. Numbers taken by rolled-back
transactions are not reused, so IDs are unique and increasing but have gaps. They are
sequential and therefore guessable, which doesn't matter here: no endpoint takes a hazard ID
as input, and any device can see hazards anyway. Random IDs (UUIDs) would cost twice the space
and slower index inserts for no security gain. `GENERATED ALWAYS AS IDENTITY` is preferred over
`BIGSERIAL` because it is the SQL-standard form and rejects an insert that sets `id` by hand
(which could later collide with the sequence).

**`hazard_confirmations`:**
- Its primary key is `(hazard_id, device_id)`, so the database itself makes counting a device
  twice impossible. `hazard_id` is a foreign key to `hazards.id`.
- **The original reporter gets a row too**, inserted together with the hazard. One lookup then
  answers "has this device already reported or confirmed this hazard?" (`already_reported`).
- `hazards.confirmations` is kept as a column even though it equals the number of rows here:
  `GET /v1/hazards` returns it on every poll, and reading a column is far cheaper than counting
  rows. Both are updated in the same transaction (C-102).
- It also feeds the contribution counts (section 8).

**Deliberately not stored:** an `event_tags` table (the registry is Go code, section 4); poll
locations (privacy, section 7); the archive of expired hazards (later, C-307).

---

## 10. Abuse controls — Agreed

In-memory limits break with several replicas (each would count separately), so limits live in
two places that every replica shares:

| Layer | Where | Applies to | Starting value |
|---|---|---|---|
| **Per IP address** | Reverse proxy (Stage 2) / ingress (Stage 3), before Go | Every endpoint; strictest on `POST /v1/devices/register` | Registration: about 5 per IP address per hour |
| **Per device** | The database (a small counter table, one upsert per report) | `POST /v1/hazards` | About 60 reports per device per hour |

- The per-IP layer stops floods before they cost the Go server anything. Carriers put many
  phones behind one IP address, so a registration limit per IP will occasionally block a real
  user — accepted.
- The per-device layer is what stops one stolen or fake key from spamming reports. Reports are
  rare compared with polls, so one extra query per report is cheap.
- A rejected request gets `429 rate_limited` with a `Retry-After` header (section 8).
- On the laptop (Stage 1) there is no proxy, so only the per-device layer runs — enough for
  testing.
- One device can't inflate confirmations: `hazard_confirmations` (section 9).
- All values are configuration and can be tuned.

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

Section 8 then removed `radius_meters`, `lookahead_distance_meters` and `search_radius_meters`
from the API — the server computes them and never sends or accepts them — and added
`next_poll`, `fallback_seconds`, `api_key`, `created_at`, `minimum_latitude`,
`minimum_longitude`, `maximum_latitude`, `maximum_longitude`, `hazards_reported`,
`hazards_confirmed`, `reports_confirmed_by_others` and the error object (`error`, `code`,
`message`).

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
| One generic API for every producer; per-tag policy lives on the server | Sections 2–4 |
| Event-tag registry lives in Go code; the handler rejects unknown tags (`400`) and disallowed device types (`403`); no `event_tags` table | Section 4 |
| Starting tags: `accident`, `pothole`, `checkpost`, `debris`, with their time to live, deduplication radius and allowed device types | Section 4 |
| `confidence` is the detector score for `auto` reports and null for `manual` ones, enforced by a check constraint; no default confidence | Section 4 |
| Speed-based polling: `search_radius = lookahead_distance = poll_distance = speed × time_between_polls`, with a minimum, a maximum and a fallback timer | Section 5 |
| Polling numbers are configuration, not architecture | Section 5 |
| Each device confirms a hazard at most once; nearest match wins | Section 6 |
| Expired hazards are kept; moved to a separate archive store at the end of the project, not deleted | Section 6 |
| A revoked device's existing hazards stay until they expire | Section 6 |
| `confirmations` counts reporting devices, the first reporter included; a repeat is recognized by location first, then by `hazard_confirmations` | Section 6 |
| Clients report each hazard once (tracker ID, or a short distance-and-time memory); the server check is the safety net | Section 6 |
| Per-device keys on every request, reads included; HMAC-SHA256 with a pepper, not bcrypt; same `401` for unknown device and wrong key, plus a dummy hash | Section 7 |
| `device_id` = type prefix + 8 random hexadecimal characters; key = 32 random bytes, URL-safe base64, shown once | Section 7 |
| Phones self-register through `POST /v1/devices/register`; same trust as Jetson units | Section 7 |
| HTTPS mandatory beyond the laptop; never log coordinates with a device ID in Stages 2 and 3 | Section 7 |
| Fake identities through registration accepted as a known, limited risk; attestation / accounts are future work | Section 7 |
| Handshake is only an identity check; event-tag lists are hardcoded in the clients | Section 8 |
| Polling values (`poll_distance_meters`, `fallback_seconds`) come only in `GET /v1/hazards` responses; the client never sends a radius | Section 8 |
| `source` is derived from the device type, not sent by the client | Section 8 |
| `POST /v1/hazards` statuses `created` / `confirmed` / `already_reported`; `heading_degrees` optional; no client timestamp | Section 8 |
| Nearby search: distance from the current position, nearest first, at most 100 | Section 8 |
| App map endpoint takes a bounding box, at most about 20 × 20 kilometers and 500 results | Section 8 |
| One error shape with machine-readable `code` values | Section 8 |
| Tables: `devices`, `hazards`, `hazard_confirmations` (reporter included, primary key `(hazard_id, device_id)`), a report rate-limit counter | Section 9 |
| `hazards.id` generated by Postgres (`GENERATED ALWAYS AS IDENTITY`); `confirmations` kept as a column alongside `hazard_confirmations` | Section 9 |
| Rate limits in two shared layers: per IP address at the proxy / ingress, per device in the database for reports | Section 10 |
| Sharing-time indicator split into server latency + polling staleness | Section 11 |
| Full-word names everywhere, including JSON fields and database columns; only `ID`, `URL`, `API`, `HTTP`, `JSON`, `SQL` stay short | Section 12 |
