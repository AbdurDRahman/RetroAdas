-- Trusted device registry. Every write request (POST /v1/hazards,
-- POST /v1/devices/handshake) is authenticated against this table.
-- See database_schema.md, Section 3.

CREATE TABLE IF NOT EXISTS devices (
    device_id     TEXT PRIMARY KEY,
    api_key_hash  TEXT NOT NULL,          -- hash only, never the raw key
    device_type   TEXT NOT NULL,          -- 'jetson_unit' | 'mobile_app'
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked       BOOLEAN NOT NULL DEFAULT false
);
