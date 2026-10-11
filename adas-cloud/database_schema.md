# Digital Driving Assistant — Database Schema Design
**Subsystem 3 (Cloud Infrastructure) — MVP scope: Hazards & Devices only**

This document is a working design, not a final spec. It reflects decisions made so far; open items are called out explicitly at the end.

---

## 1. Scope

Two tables for this iteration:

- `hazards` — the core crowdsourced hazard data.
- `devices` — trusted device registry, used to keep the system from being spammed or attacked.

Explicitly **out of scope for now**: anything related to trips, driver score, or live vehicle tracking (companion mobile app features). Those will need their own tables later but aren't designed yet.

---

## 2. `hazards` table

This is the main table. Every hazard report — pothole, checkpost, accident, etc. — becomes a row.

```sql
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE hazards (
    id              BIGSERIAL PRIMARY KEY,
    event_tag       TEXT NOT NULL,                  -- 'pothole', 'checkpost', 'accident', 'debris', etc.
    location        GEOGRAPHY(Point, 4326) NOT NULL, -- lat/lng, GiST-indexed
    heading_deg     REAL,                            -- reporting vehicle's bearing at time of report (nullable)
    confidence      REAL NOT NULL DEFAULT 1.0,        -- 0.0–1.0, from the reporting device's YOLO detection
    reported_by     TEXT NOT NULL,                    -- device_id of the original reporter
    confirmations   INT NOT NULL DEFAULT 0,           -- incremented when other devices report the same hazard
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL              -- created_at + TTL for that event_tag
);

CREATE INDEX hazards_geo_idx    ON hazards USING GIST (location);
CREATE INDEX hazards_expiry_idx ON hazards (expires_at);
CREATE INDEX hazards_tag_idx    ON hazards (event_tag);
```

Note: this drops the `dismissals` column that appeared in the earlier architecture draft. There is no active "still there?" prompt in this design — see Section 4 (Lifecycle) for why it isn't needed.

### 2.1 Column notes

| Column | Meaning |
|---|---|
| `event_tag` | Type of hazard. Also used to look up the TTL to apply (Section 4). |
| `location` | `GEOGRAPHY` type (not plain `POINT`) so `ST_DWithin` / `ST_Project` distances are computed in real metres on the Earth's surface, not raw lat/lng degrees. |
| `heading_deg` | The *reporting* vehicle's bearing when it detected the hazard. Not required for MVP filtering logic but kept for potential future route-aware corridor work. |
| `confidence` | Set once, at insert, from the reporting device's YOLO output. Never updated afterward. Answers: *how sure was the detector about this specific detection?* |
| `reported_by` | `device_id` of whichever device created the row first. Not updated on later confirmations. |
| `confirmations` | Starts at 0. Incremented every time another device reports what looks like the same hazard (see dedup logic below). Answers: *how many independent devices agree this is real?* |
| `created_at` | Set once, server-side. |
| `expires_at` | Reset every time a confirmation comes in — see Section 4. |

`confidence` and `confirmations` measure two different things and are intentionally kept separate rather than merged into one "trust score" — a hazard can have a middling confidence (blurry frame) but high confirmations (many cars have flagged it since), which is a stronger real-world signal than one high-confidence, unconfirmed report.

---

## 3. `devices` table

Unchanged from the original architecture draft. This exists purely so that write requests can be authenticated — an unpublished endpoint path alone isn't protection, since anyone who intercepts one device's traffic can replay or script the exact request format from any computer.

```sql
CREATE TABLE devices (
    device_id     TEXT PRIMARY KEY,
    api_key_hash  TEXT NOT NULL,          -- hash only, never the raw key
    device_type   TEXT NOT NULL,          -- 'jetson_unit' | 'mobile_app'
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked       BOOLEAN NOT NULL DEFAULT false
);
```

At provisioning time each device gets a `device_id` and a randomly generated API key; the server only ever stores a hash of that key.

---

## 4. Hazard lifecycle & expiry (dedup-on-write)

No active dismissal mechanism, no user-facing "still there?" prompt — the system is meant to stay invisible to the driver. Instead, expiry works through two passive mechanisms only:

**(a) TTL by `event_tag`** — each hazard type has a baseline lifetime applied at insert time:

| `event_tag` | Approximate TTL |
|---|---|
| `checkpost` | A few hours |
| `accident` | 1–2 hours |
| `pothole` / `debris` | Days to weeks |

A background sweep process periodically deletes/archives rows past `expires_at`.

**(b) Confirmation-based reset** — this is the main mechanism that keeps a real, ongoing hazard alive without any user interaction. On every `POST` of a new hazard, before inserting, the server checks whether an existing, non-expired hazard of the **same `event_tag`** exists **within ~100m**:

```sql
SELECT id FROM hazards
WHERE event_tag = :event_tag
  AND expires_at > now()
  AND ST_DWithin(location, ST_MakePoint(:lng, :lat)::geography, 100)
LIMIT 1;
```

- **Match found →** treat as the same real-world hazard:
  - `confirmations = confirmations + 1`
  - `expires_at = now() + ttl_for(event_tag)` (clock resets)
  - `location` is **not** updated/averaged — the original report's coordinates remain authoritative, to avoid position drift across many reports.
- **No match →** insert a new row as normal.

This means a hazard that's still genuinely there gets naturally kept alive by the next car(s) that drive past and detect it again — no dismissal logic, no prompts, no separate "confirm/deny" API needed.

---

## 5. Explicitly not yet designed

- Any table for trips, driver score, or live tracking (mobile app features from the proposal).
- Anything for route-aware bearing filtering beyond the raw `heading_deg` column already present.

---

## 6. Open items for discussion

- Whether `heading_deg` on `hazards` ends up used for anything in MVP, or is just carried along for future route-corridor work.
- Whether a fixed 100m dedup radius is right for every `event_tag`, or whether e.g. `accident` (larger area) vs `pothole` (pinpoint) should have different dedup radii.
- Exact TTL values per `event_tag` — table above is illustrative; real values should come from field-testing data.
