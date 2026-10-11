# ADAS Cloud — Feature 2 backend

Cloud backend for RetroADAS's crowdsourced hazard awareness (Feature 2): a
stateless Go API over Postgres + PostGIS. Devices post the hazards they
detect, the server deduplicates them, and devices pull the hazards ahead of
where they're driving. The design is in `docs/design.md`; work is tracked in
`TASKS.md` / `DONE.md` (see `CLAUDE.md` for the workflow).

## Why the folders are named the way they are

Every directory name says what's inside it in plain words, so opening the
project after a break doesn't require remembering a convention:

```
adas-cloud/
├── cmd/server/main.go        Program entrypoint. Wires configuration + database + routes together.
├── config/                   Reads all environment variables, once, in one place.
├── database/
│   ├── connection.go         How the running SERVER connects to Postgres.
│   ├── migrations/           Plain numbered .sql files, one per table.
│   ├── init/                 Runs once on a fresh Docker database: creates
│   │                         adas_test and applies the migrations to both.
│   └── testdatabase/         How TESTS connect to Postgres (transaction-
│                             per-test, always rolled back). Deliberately
│                             separate from connection.go so production code
│                             can never accidentally import test-only helpers.
├── internal/
│   ├── devices/              Everything about authenticating a device_id +
│   │                         API key against the devices table.
│   ├── hazards/
│   │   ├── deduplication.go  POST /v1/hazards logic: match-or-insert.
│   │   └── nearby_search.go  GET /v1/hazards logic: projected lookahead search.
│   └── handshake/            POST /v1/devices/handshake logic.
├── load-tests/k6/            k6 scripts that stand in for real hardware
│                             until Jetson units exist — "virtual devices"
│                             that poll and report exactly like the real
│                             thing will.
├── docs/design.md            The backend design (authoritative).
├── docker-compose.yml        Local Postgres + PostGIS.
└── docker-entrypoint.sh      Container entry point (used from C-112 on).
```

`internal/` holds one sub-package per concern rather than one big `api/`
package, so "where does the deduplication logic live" always has one obvious
answer.

## Two layers of testing, two different jobs

**Go tests (`*_test.go` next to the code they test)** — the actual TDD loop.
Run constantly while developing:

```bash
go test ./...
```

These test internal correctness (did `confirmations` increment? did the
right row get matched?) against a real Postgres+PostGIS instance via
`database/testdatabase`, not mocks — PostGIS geography math is exactly the kind
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

Needs Docker (on WSL: Docker Desktop with WSL integration enabled) and Go 1.24.

```bash
# 1. Start Postgres 17 + PostGIS 3.5. The first start creates adas_dev and
#    adas_test and applies database/migrations/*.sql to both.
docker compose up -d --wait database

# 2. Local credentials. .env is gitignored; the defaults already match the
#    container, so no edits are needed for local work.
cp .env.example .env

# 3. Load .env into THIS terminal (repeat in every new terminal).
#    set -a exports every variable that follows; set +a stops that.
set -a; source .env; set +a

# 4. Run the tests against adas_test.
go test ./...
```

Useful database commands:

```bash
docker compose logs -f database                               # every SQL statement (log_statement=all)
docker compose exec database psql -U adas_user -d adas_dev    # SQL shell
docker compose down                                     # stop, keep data
docker compose down -v                                  # stop AND delete all data
```

**New migration files are not applied automatically yet.** The init script
(`database/init/`) only runs when the data volume is empty. After adding a
migration, either reset with `docker compose down -v` (deletes all data) or
apply it by hand with `psql -f`. Task C-005 replaces this with a proper
migration runner.

## Current state

Work is tracked task by task: `TASKS.md` (remaining), `DONE.md` (finished,
with the decisions made), `docs/design.md` (the backend design). Every test
currently either fails with "not implemented" or is `t.Skip`'d — that's the
intended starting point for the TDD loop.
