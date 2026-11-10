-- Trusted device registry. Every request is authenticated against this table,
-- reads included. See docs/design.md, sections 7 and 9.

-- +goose Up
CREATE TABLE IF NOT EXISTS devices (
    device_id     TEXT PRIMARY KEY,
    api_key_hash  TEXT NOT NULL,          -- hash only, never the raw key
    device_type   TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked       BOOLEAN NOT NULL DEFAULT false,

    -- The type decides what a device may report, so only the two types the
    -- design knows are allowed. A misspelled type fails here, at the insert,
    -- instead of creating a device that silently matches no rule.
    CONSTRAINT devices_device_type_check
        CHECK (device_type IN ('jetson_unit', 'mobile_app'))
);
