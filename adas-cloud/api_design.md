> **Superseded.** This early draft is kept only for history. The agreed backend design is
> [`docs/design.md`](docs/design.md) (task C-001); names, endpoints and tables here are outdated.

# Digital Driving Assistant — API Design
**Subsystem 3 (Cloud Infrastructure) — MVP scope**

This document is a rough first pass at the endpoint contracts — naming and shapes are expected to change as you get further into Go and start actually building this. Open questions are called out per-endpoint and again at the end.

---

## 1. Design principles (agreed so far)

- **No WebSocket / push channel.** Everything is stateless request/response. No connection hub, no per-device server-side state, no live-position tracking on the server between requests.
- **Distance-triggered polling, not time-triggered.** The client decides when to poll based on distance traveled since its last poll (e.g. every ~300m), not on a fixed timer. This naturally goes idle in stopped traffic without extra logic.
- **Projected lookahead, not current position.** To avoid feeling laggy, the client queries hazards around a point *ahead* of its actual position (e.g. current location + ~200m along heading), not its literal current coordinates.
- **Per-device authentication** on any write, via `device_id` + API key, checked against the `devices` table (see `database_schema.md`).

---

## 2. Endpoint overview

| # | Endpoint | Purpose | Auth required |
|---|---|---|---|
| 1 | `POST /v1/devices/handshake` | Startup check-in when a device boots | Yes |
| 2 | `POST /v1/hazards` | Submit a newly detected hazard | Yes |
| 3 | `GET /v1/hazards` | Fetch nearby hazards | Yes (or lighter, TBD — see open items) |

All endpoints versioned under `/v1/` for consistency, including the hazard-fetch one (unifying naming — your example used `/api/hazards`, worth picking one prefix scheme and sticking to it across the whole backend).

---

## 3. `POST /v1/devices/handshake`

Called once when a device boots up (Jetson unit or mobile app), before it starts polling or submitting anything. Purpose: confirm the device is provisioned and not revoked, and let the server hand back any config the device should use for this session.

**Headers:**
```
X-Device-Id: veh-0427
X-Api-Key: <device's provisioned secret>
```

*Design note: your draft put `device_id` and `x_api_key` in the JSON body. Recommend moving both to headers instead — matches the pattern already used for `POST /v1/hazards`, so authentication is handled the same way everywhere rather than differently per endpoint.*

**Request body:** empty, or optionally device metadata (firmware version, device_type confirmation) if useful for debugging later. Not required for MVP.

**Response — 200 OK:**
```json
{
  "device_id": "veh-0427",
  "device_type": "jetson_unit",
  "poll_interval_distance_m": 300,
  "lookahead_m": 200,
  "hazard_radius_m": 1000
}
```

The config values here are just a first idea — the server can hand back tunable numbers (poll distance, lookahead distance, radius) rather than hardcoding them on the device, so you can adjust behavior later without re-flashing firmware. Optional for MVP; a bare `200 OK` with just `device_id` echoed back is enough to start.

**Error responses:**

| Status | Meaning |
|---|---|
| `401` | Unknown `device_id` or API key doesn't match |
| `403` | Device exists but `revoked = true` |

---

## 4. `POST /v1/hazards`

Called when a device's onboard CV pipeline detects a hazard.

**Headers:**
```
X-Device-Id: veh-0427
X-Api-Key: <device's provisioned secret>
```

**Request body:**
```json
{
  "event_tag": "pothole",
  "lat": 33.6850,
  "lng": 73.0490,
  "heading_deg": 140.0,
  "confidence": 0.91
}
```

**Server-side behavior (per the dedup logic in `database_schema.md`):**
1. Validate device credentials (401/403 as above).
2. Check for an existing non-expired hazard, same `event_tag`, within ~100m.
3. If found → increment `confirmations`, reset `expires_at`, return the existing row.
4. If not found → insert new row, compute `expires_at` from the TTL table for that `event_tag`.

**Response — 201 Created (new hazard):**
```json
{
  "id": 9123,
  "status": "created",
  "expires_at": "2026-07-10T03:10:00Z"
}
```

**Response — 200 OK (matched existing hazard, confirmation incremented):**
```json
{
  "id": 9123,
  "status": "confirmed",
  "confirmations": 4,
  "expires_at": "2026-07-10T05:40:00Z"
}
```

Distinguishing `created` vs `confirmed` in the response is useful for debugging/logging on the device side even though the device doesn't need to act differently either way.

---

## 5. `GET /v1/hazards`

Called repeatedly while driving — the main "discovery" endpoint. This is the one with a real open design question (Section 6.1) about where the lookahead projection math should live.

**Option A — client sends raw state, server projects (recommended to start with):**

```
GET /v1/hazards?lat=33.6844&lng=73.0479&heading_deg=142.0&speed_kmh=38.0&radius_m=1000
```

Server computes the projected point itself using PostGIS:

```sql
SELECT * FROM hazards
WHERE expires_at > now()
  AND ST_DWithin(
        location,
        ST_Project(
          ST_MakePoint(:lng, :lat)::geography,
          :lookahead_m,
          radians(:heading_deg)
        ),
        :radius_m
      );
```

`:lookahead_m` can be a fixed default (e.g. 200m) or derived server-side from `speed_kmh` if you want it dynamic later.

**Option B — client precomputes the projected lat/lng itself and just sends that as the query point.** Simpler server logic (one less PostGIS call), but pushes projection math (and any future improvements to it, like the line/corridor idea discussed earlier) onto every device instead of one place. Flagging as the alternative, not recommending it as the starting point — centralizing the geo math server-side means you only have to get `ST_Project` right once, rather than reimplementing projection logic correctly on every embedded device.

**Response — 200 OK:**
```json
{
  "hazards": [
    {
      "id": 9123,
      "event_tag": "pothole",
      "lat": 33.6850,
      "lng": 73.0490,
      "distance_m": 95,
      "confidence": 0.91,
      "confirmations": 4
    }
  ]
}
```

No `poll_interval_s` in the response — since polling is distance-triggered client-side, the server doesn't need to tell the client when to come back.

---

## 6. Open items for discussion

1. **Where projection math lives** (Section 5, Option A vs B) — leaning server-side, but worth deciding before you write any client code, since it affects what the Jetson unit needs to send.
2. **Whether `GET /v1/hazards` needs full device auth or something lighter.** It's read-only, so the "spam/attack" risk is lower than for `POST /v1/hazards` — but an unauthenticated read endpoint does let anyone scrape your entire hazard map. Worth deciding whether the same `X-Device-Id`/`X-Api-Key` headers apply here too, or a lighter shared token.
3. **Single-point projection vs. a projected line/corridor** for curved roads — flagged earlier as a possible refinement, not needed for MVP.
4. **Dedup radius per `event_tag`** — same 100m for every hazard type, or should `accident` (larger real-world footprint) differ from `pothole` (pinpoint)?
5. **Handshake response config values** — worth having, or overkill for MVP (just return device_id + 200 OK and hardcode everything else on-device for now)?
