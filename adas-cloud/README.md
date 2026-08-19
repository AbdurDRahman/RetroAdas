# ADAS Cloud — Subsystem 3

Cloud backend for the retrofittable ADAS FYP. Implements the three endpoints
in `api_design.md` (handshake, submit hazard, fetch nearby hazards) against
the schema in `database_schema.md`.

## Why the folders are named the way they are

Every directory name says what's inside it in plain words, so opening the
project after a break doesn't require remembering a convention:

```
adas-cloud/
├── cmd/server/main.go        Program entrypoint. Wires config + DB + routes together.
├── config/                   Reads all environment variables, once, in one place.
├── database/
│   ├── connection.go         How the running SERVER connects to Postgres.
│   ├── migrations/           Plain numbered .sql files, one per table, taken
│   │                         straight from database_schema.md.
│   └── testing/               How TESTS connect to Postgres (transaction-
│                              per-test, always rolled back). Deliberately
│                              separate from connection.go so production code
│                              can never accidentally import test-only helpers.
├── internal/
│   ├── devices/               Everything about authenticating a device_id +
│   │                          API key against the devices table.
│   ├── hazards/
│   │   ├── deduplication.go   POST /v1/hazards logic: match-or-insert.
│   │   └── nearby_search.go   GET /v1/hazards logic: projected lookahead search.
│   └── handshake/             POST /v1/devices/handshake logic.
└── load-tests/k6/             k6 scripts that stand in for real hardware
                                until Jetson units exist — "virtual devices"
                                that poll and report exactly like the real
                                thing will.
```

`internal/` holds one sub-package per concern rather than one big `api/`
package — each maps directly to a section of `api_design.md`, so "where does
the dedup logic live" always has one obvious answer.

## Two layers of testing, two different jobs

**Go tests (`*_test.go` next to the code they test)** — the actual TDD loop.
Run constantly while developing:

```bash
go test ./...
```

These test internal correctness (did `confirmations` increment? did the
right row get matched?) against a real Postgres+PostGIS instance via
`database/testing`, not mocks — PostGIS geography math is exactly the kind
of thing you don't want to fake.

**k6 scripts (`load-tests/k6/`)** — black-box simulation of real device
traffic against the deployed API, standing in for hardware that doesn't
exist yet:

```bash
k6 run -e BASE_URL=http://localhost:8080 load-tests/k6/simulate_device_polling.js
k6 run -e BASE_URL=http://localhost:8080 load-tests/k6/simulate_hazard_burst.js
```

Later, when real Jetson units exist, these scripts get replaced by real
traffic with zero API changes — that's the point of having them shaped
exactly like the documented request format from day one.

## Setup

```bash
cp .env.example .env   # fill in real DB credentials
go mod tidy
psql "$DATABASE_URL" -f database/migrations/001_create_devices_table.sql
psql "$DATABASE_URL" -f database/migrations/002_create_hazards_table.sql
# repeat both migrations against $TEST_DATABASE_URL too
```

## Current state (TDD red bar)

Every test currently either fails or is explicitly `t.Skip`'d with a `TODO`
pointing at what to implement. That's intentional — the recommended order:

1. `internal/devices` — `TestAuthenticate` (smallest, most isolated)
2. `internal/hazards` — `TestProcessReport` (the core dedup logic)
3. `internal/hazards` — `TestFindNearby` (depends on fixtures from step 2)
4. `internal/handshake` — thin, mostly reuses step 1
5. Wire real HTTP handlers into `cmd/server/main.go`
6. Point `load-tests/k6/*.js` at the running server
