-- Core crowdsourced hazard data. See database_schema.md, Section 2.

CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS hazards (
    id              BIGSERIAL PRIMARY KEY,
    event_tag       TEXT NOT NULL,                    -- 'pothole', 'checkpost', 'accident', 'debris', etc.
    location        GEOGRAPHY(Point, 4326) NOT NULL,   -- lat/lng, GiST-indexed
    heading_deg     REAL,                              -- reporting vehicle's bearing at time of report (nullable)
    confidence      REAL NOT NULL DEFAULT 1.0,         -- 0.0-1.0, from the reporting device's YOLO detection
    reported_by     TEXT NOT NULL,                     -- device_id of the original reporter
    confirmations   INT NOT NULL DEFAULT 0,            -- incremented when other devices report the same hazard
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL               -- created_at + TTL for that event_tag
);

CREATE INDEX IF NOT EXISTS hazards_geo_idx    ON hazards USING GIST (location);
CREATE INDEX IF NOT EXISTS hazards_expiry_idx ON hazards (expires_at);
CREATE INDEX IF NOT EXISTS hazards_tag_idx    ON hazards (event_tag);
