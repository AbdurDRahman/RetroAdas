-- Core crowdsourced hazard data: one row per real-world hazard. See
-- docs/design.md, sections 4, 6 and 9; column and index names follow section 12.
-- The rules below are checked by database/hazards_table_test.go.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS hazards (
    -- Postgres hands out the numbers. ALWAYS means an insert that sets id by
    -- hand is refused, so it can never collide with a number handed out later.
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_tag        TEXT NOT NULL,                    -- checked against the registry in Go, not here
    location         GEOGRAPHY(Point, 4326) NOT NULL,  -- longitude/latitude point; never changes after creation
    heading_degrees  REAL,                             -- first reporter's direction of travel; optional
    confidence       REAL,                             -- the detector's score; empty for a person's report
    source           TEXT NOT NULL,                    -- 'auto' (a detector) | 'manual' (a person)
    reported_by      TEXT NOT NULL,                    -- device_id of the first reporter
    confirmations    INT NOT NULL DEFAULT 1,           -- devices that reported it, the first one included
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,             -- last confirmation + the tag's time to live

    -- A person's report has no detector score; a detector's report must have
    -- one between 0 and 1. "confidence IS NOT NULL" is written out on purpose:
    -- comparing an empty value gives "unknown", and a CHECK lets "unknown"
    -- through, so without it an auto report with no score would be accepted.
    CONSTRAINT hazards_source_confidence_check CHECK (
        (source = 'manual' AND confidence IS NULL)
        OR (source = 'auto' AND confidence IS NOT NULL AND confidence >= 0 AND confidence <= 1)
    ),

    -- A compass direction: 0 is north, and 360 would be the same as 0.
    CONSTRAINT hazards_heading_degrees_check CHECK (
        heading_degrees IS NULL OR (heading_degrees >= 0 AND heading_degrees < 360)
    ),

    -- The first reporter counts, so a hazard never has fewer than 1.
    CONSTRAINT hazards_confirmations_check CHECK (confirmations >= 1),

    -- A hazard can only name a device that exists. Devices are revoked, never
    -- deleted, so this never blocks anything.
    CONSTRAINT hazards_reported_by_fkey FOREIGN KEY (reported_by) REFERENCES devices (device_id)
);

-- GiST (Generalized Search Tree) is the index type PostGIS uses for
-- "what is near this point" queries (ST_DWithin); a normal B-tree index
-- can't answer distance questions.
CREATE INDEX IF NOT EXISTS hazards_location_index   ON hazards USING GIST (location);
CREATE INDEX IF NOT EXISTS hazards_expires_at_index ON hazards (expires_at);
CREATE INDEX IF NOT EXISTS hazards_event_tag_index  ON hazards (event_tag);
