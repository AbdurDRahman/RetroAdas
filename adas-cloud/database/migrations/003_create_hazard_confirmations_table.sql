-- Which devices have reported each hazard: one row per (hazard, device) pair,
-- the first reporter included. See docs/design.md, sections 6 and 9.
-- The rules below are checked by database/hazard_confirmations_table_test.go.

-- +goose Up
CREATE TABLE hazard_confirmations (
    hazard_id     BIGINT NOT NULL,
    device_id     TEXT NOT NULL,
    confirmed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The pair is the key, so the database itself refuses to count one device
    -- twice for the same hazard. It also makes "has this device already
    -- reported this hazard?" a single indexed lookup.
    CONSTRAINT hazard_confirmations_pkey PRIMARY KEY (hazard_id, device_id),

    CONSTRAINT hazard_confirmations_hazard_id_fkey FOREIGN KEY (hazard_id) REFERENCES hazards (id),
    CONSTRAINT hazard_confirmations_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices (device_id)
);
