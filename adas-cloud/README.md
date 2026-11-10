# ADAS Cloud — Feature 2 backend

Cloud backend for RetroADAS's crowdsourced hazard awareness (Feature 2): a
stateless Go API over Postgres + PostGIS. Devices post the hazards they
detect, the server deduplicates them, and devices pull the hazards ahead of
where they're driving. The design is in `docs/design.md`; work is tracked in
`TASKS.md` / `DONE.md` (see "How work is organized" below).

## Why the folders are named the way they are

Every directory name says what's inside it in plain words, so opening the
project after a break doesn't require remembering a convention:

```
adas-cloud/
├── cmd/server/main.go        Program entrypoint. Wires configuration + database + routes together.
├── cmd/migrate/main.go       Applies pending migrations, then exits. Runs before
│                             the server (entrypoint script / Kubernetes Job).
├── config/                   Reads all environment variables, once, in one place.
├── database/
│   ├── connection.go         How the running SERVER connects to Postgres.
│   ├── migrate.go            The migration runner (goose), with the .sql files
│   │                         built into the binary.
│   ├── migrations/           Numbered .sql files, applied in order, each once.
│   ├── init/                 Runs once on a fresh Docker database: creates
│   │                         the empty adas_test database (no tables).
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
# 1. Start Postgres 17 + PostGIS 3.5. The first start creates the empty
#    adas_dev and adas_test databases.
docker compose up -d --wait database

# 2. Local credentials. .env is gitignored; the defaults already match the
#    container, so no edits are needed for local work.
cp .env.example .env

# 3. Load .env into THIS terminal (repeat in every new terminal).
#    set -a exports every variable that follows; set +a stops that.
set -a; source .env; set +a

# 4. Create the tables in adas_dev (safe to repeat: it only applies new files).
go run ./cmd/migrate

# 5. Run the tests against adas_test (they migrate adas_test themselves).
go test ./...
```

Useful database commands:

```bash
docker compose logs -f database                               # every SQL statement (log_statement=all)
docker compose exec database psql -U adas_user -d adas_dev    # SQL shell
docker compose down                                     # stop, keep data
docker compose down -v                                  # stop AND delete all data
```

### Migrations

`go run ./cmd/migrate` applies the `.sql` files in `database/migrations/` that
a database hasn't applied yet. Tests migrate `adas_test` themselves. How it
works and why is in `docs/design.md` section 9.

To add a migration:

1. Create the next file, e.g. `003_create_hazard_confirmations_table.sql`,
   starting with the line `-- +goose Up`. Forward only: no `-- +goose Down`
   section; to undo a change, write another migration.
2. Add its name to `expectedMigrationFileNames` in `database/migrate_test.go`.
3. Run `go run ./cmd/migrate` and `go test ./...`.

Until the first deploy to the Azure VM (Stage 2), an applied file may still be
edited in place, followed by `docker compose down -v`. From Stage 2 on, never
edit an applied file: it would be skipped, and the databases would quietly
differ from the file. Add a new one instead.

## How work is organized

Every piece of work is one task from `TASKS.md`, taken through five steps:

1. **Pick** — choose the next unblocked task in the current stage (check its
   **Depends on**).
2. **Discuss** — settle the approach and the task's **Discuss** items before
   writing any code, starting from the section of `docs/design.md` it touches.
3. **Code** — test first, then the implementation.
4. **Verify** — `go test ./...`, plus curl or k6 when the task touches HTTP or
   load.
5. **Close** — remove the task from `TASKS.md`, add an entry to the top of
   `DONE.md` with the decisions made, add any newly discovered work to
   `TASKS.md` as new tasks, and commit.

The work runs in stages: 0 Foundations → 1 Laptop (docker compose) →
2 Single Azure VM → 3 Multi-server Kubernetes. `docs/design.md` is the agreed
backend design and the one place where design facts are written; changing one
of its decisions is a task of its own.

Every push that touches `adas-cloud/` runs the checks in
`../.github/workflows/adas-cloud-tests.yml` on GitHub: formatting, `go vet`,
build, and the tests against a real PostGIS database.

## Current state

Stage 0 (foundations) is done: the design is agreed, the database runs in
Docker, and migrations work. No endpoint is implemented yet. The tests for
unwritten features are skipped, each naming the task that will write it;
`TASKS.md` lists what remains and `DONE.md` what is finished.
