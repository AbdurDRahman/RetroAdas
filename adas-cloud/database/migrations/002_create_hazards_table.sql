-- Core crowdsourced hazard data. See docs/design.md, sections 6 and 9;
-- column and index names follow section 12.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS hazards (
    id               BIGSERIAL PRIMARY KEY,
    event_tag        TEXT NOT NULL,                    -- 'pothole', 'checkpost', 'accident', 'debris', etc.
    location         GEOGRAPHY(Point, 4326) NOT NULL,  -- longitude/latitude point, GiST-indexed
    heading_degrees  REAL,                             -- reporting vehicle's bearing at time of report (nullable)
    confidence       REAL NOT NULL DEFAULT 1.0,        -- 0.0-1.0, from the reporting device's detector
    reported_by      TEXT NOT NULL,                    -- device_id of the original reporter
    confirmations    INT NOT NULL DEFAULT 0,           -- incremented when other devices report the same hazard
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL              -- created_at + time to live for that event_tag
);

-- GiST (Generalized Search Tree) is the index type PostGIS uses for
-- "what is near this point" queries (ST_DWithin); a normal B-tree index
-- can't answer distance questions.
CREATE INDEX IF NOT EXISTS hazards_location_index   ON hazards USING GIST (location);
CREATE INDEX IF NOT EXISTS hazards_expires_at_index ON hazards (expires_at);
CREATE INDEX IF NOT EXISTS hazards_event_tag_index  ON hazards (event_tag);
