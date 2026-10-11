# DONE — completed cloud work

Newest first. A task moves here from `TASKS.md` when it's closed (step 5 of the loop in
`CLAUDE.md`). The **Decisions made** section is the durable record of *why* — read the last
few entries at the start of a session.

Entry template:

```
### C-xxx Title — YYYY-MM-DD
- **Written by:** me | claude
- **Commit(s):** <short sha> <message>
- **What changed:**
  - ...
- **Decisions made:**
  - ...
- **Follow-ups:** new task IDs added to TASKS.md (or —)
```

---

### C-006 Refactor the codebase to the naming rules — 2026-10-11
- **Written by:** claude (via /code-with-me)
- **Commit(s):** — (not committed yet)
- **What changed:**
  - Go: every abbreviated identifier spelled out (`tx` → `transaction`, `cfg` → `configuration`, `db` → `connectionPool`, `mux` → `router`, `AuthResult` → `AuthenticationResult`, `ttlFor` → `timeToLiveFor`, `Lat`/`Lng` → `Latitude`/`Longitude`, `RadiusM` → `RadiusMeters`, …); tests use `test`, `testCases`/`testCase`, `expected…`/`actual…`; fixture types `seededDevice` / `seededHazard`.
  - `database/testing` → `database/testdatabase` (file `transaction.go`), so tests import it without the `testdb` alias.
  - JSON fields and query parameters renamed (`latitude`, `longitude`, `heading_degrees`, `speed_kilometers_per_hour`, `radius_meters`, `distance_meters`, `poll_distance_meters`, `lookahead_distance_meters`, `search_radius_meters`).
  - Migration 002 edited in place: `heading_deg` → `heading_degrees`, indexes `*_idx` → `hazards_location_index` / `hazards_expires_at_index` / `hazards_event_tag_index`; local database rebuilt.
  - k6 scripts: new JSON / query names and full-word variables; `veh-` → `vehicle-` test IDs everywhere.
  - `docker-entrypoint.sh`: `i` → `attempt_number`, `WAIT_FOR_DB` → `WAIT_FOR_DATABASE`, `DB_WAIT_RETRIES` → `DATABASE_WAIT_RETRIES`.
  - Docker Compose service `db` → `database` (commands are now `docker compose … database`).
  - Comments point at `docs/design.md` instead of the old drafts; TODOs tagged with their task (`TODO(C-101)` …); `gofmt` clean for the whole project; README folder tree and `CLAUDE.md` brought up to date.
- **Decisions made:**
  - Only `ID`, `URL`, `API`, `HTTP`, `JSON`, `SQL` stay short; units are spelled out in names.
  - Go specifics: `err`, `test *testing.T`, `cntx`; a variable never shares a name with a package.
  - **JSON fields and database columns were renamed too** — a design change, made now because nothing is built on them yet. Recorded in `docs/design.md` section 12; the early drafts keep the old names.
  - Migrations edited in place instead of adding a rename migration, since nothing is deployed.
  - Full naming rules written into `CLAUDE.md`.
- **Also fixed (bug in C-003):** the database health check used the Unix socket, so it reported "healthy" while the image's *temporary* init server was running — before `adas_test` existed. It now checks over TCP (`pg_isready -h 127.0.0.1`), which only succeeds once initialisation is complete. Verified by querying immediately after `up --wait` on a fresh volume.
- **Verified:** `go build`, `go vet`, `gofmt -l` clean; `go test ./...` gives the same red bar as before (1 fail + 3 skips per table test, `TestFindNearby` skips); server starts and connects; entrypoint database-wait loop works against the real database; both k6 scripts parse (`k6 inspect`); renamed column and indexes present in both databases.
- **Follow-ups:** C-106 gained connection reuse (keep-alive / HTTP/2) and gzip responses.

### C-003 Local Postgres+PostGIS in Docker — 2026-10-11
- **Written by:** claude (via /code-with-me)
- **Commit(s):** — (not committed yet)
- **What changed:**
  - `docker-compose.yml`: `db` service on `postgis/postgis:17-3.5`, named volume `database_data`, `pg_isready` health check, every SQL statement logged (`log_statement=all`).
  - `database/init/20_create_test_database_and_migrate.sh`: on first start, creates `adas_test` and applies `database/migrations/*.sql` to `adas_dev` and `adas_test` (`ON_ERROR_STOP`, `set -euo pipefail`).
  - `adas-cloud/.gitignore`: `.env` and variants ignored (`.env.example` kept), top-level build binaries, coverage files.
  - `README.md` setup section and `CLAUDE.md` commands rewritten for the Docker flow.
- **Decisions made:**
  - Pinned Postgres 17.5 + PostGIS 3.5.2. Stages 2 and 3 must run the same version.
  - The init script is mounted as a **single file** named `20_...`, so the image's own `10_postgis.sh` stays visible and runs first (mounting the whole folder would have hidden it).
  - Statement logging on for development, to make debugging and following the tests easier. Not for Stage 2/3.
  - **`.env` is loaded by the shell** (`set -a; source .env; set +a`), not by the program (`godotenv` rejected: `go test` runs from each package's folder so it wouldn't find `.env`, and it adds laptop-only code). The program only ever reads plain environment variables, the same as on the VM and in Kubernetes. `direnv` is an optional convenience later.
  - `.env` was not ignored by git before this task, so nothing gets committed until `.gitignore` covers it.
- **Verified:** container healthy; both databases have `devices`, `hazards` and PostGIS 3.5.2; following the README in a clean shell, `go test ./...` reaches the database and fails only with "not implemented" / skips; the statement log shows each test's `BEGIN` … `ROLLBACK`.
- **Follow-ups:** known limitation (new migrations need `down -v` or manual `psql -f`) is resolved by C-005, which is now unblocked.

### C-004 Make the test suite compile (red, not broken) — 2026-10-11
- **Written by:** claude (via /code-with-me)
- **Commit(s):** — (not committed yet)
- **What changed:**
  - `seedDevice` (`internal/devices/authentication_test.go`) and `seedHazard` (`internal/hazards/deduplication_test.go`) now take `*sql.Tx` instead of an inline interface that `*sql.Tx` couldn't satisfy (its `Exec` returns `sql.Result`, not `any`).
  - Comments explain why the fixture must share the test's transaction: seeded rows are uncommitted, so only that transaction can see them.
- **Decisions made:**
  - Concrete `*sql.Tx` now. A shared interface (if any) is decided in C-105 together with the handler layer.
  - Fixture bodies stay `t.Skip` (filled in by C-101 / C-102); short names stay for C-006.
- **Verified:** `go vet ./...` clean. `go test ./...` compiles and fails only with `TEST_DATABASE_URL must be set` (no database until C-003).
- **Follow-ups:** —

### C-002 Real module path + dependency lockfile — 2026-10-11
- **Written by:** claude (via /code-with-me)
- **Commit(s):** — (not committed yet)
- **What changed:**
  - `go.mod`: module path `github.com/YOUR-ORG/adas-cloud` → `github.com/AbdurDRahman/RetroAdas/adas-cloud`; `go 1.22` → `go 1.24`.
  - Updated the 7 imports in `cmd/server/main.go` and the three `_test.go` files.
  - `go mod tidy` added the indirect dependencies (`go-spew`, `go-difflib`, `yaml.v3`) and created `go.sum`.
- **Decisions made:**
  - Module path follows the repository layout: repository `github.com/AbdurDRahman/RetroAdas`, folder `adas-cloud/`.
  - Go 1.24, matching the installed toolchain.
  - The `testdb` import alias breaks the naming rules but is left for C-006 rather than widening this task.
- **Verified:** `go build ./...` passes. `go vet ./...` fails only on the two known fixture type errors (`*sql.Tx` vs `interface{ Exec(...) (any, error) }`), which C-004 fixes.
- **Follow-ups:** — (C-004 is now unblocked)

### C-000 Workflow files + staged roadmap — 2026-10-06
- **Written by:** claude
- **Commit(s):** — (not committed yet)
- **What changed:**
  - Added `TASKS.md` (full roadmap, Stages 0–3), `DONE.md` (this log), `CLAUDE.md` (session context + workflow loop), `docker-entrypoint.sh` (container entry point skeleton).
- **Decisions made:**
  - Workflow: pick → discuss → code → verify → close. Code is written either by the user or by Claude via `/code-with-me`.
  - Roadmap in three stages: (1) laptop with docker compose, (2) single Azure VM, (3) multi-server Kubernetes. The Kubernetes flavor is deferred to C-301.
  - Dev/test Postgres+PostGIS runs in Docker.
  - In scope beyond the 3 core endpoints: provisioning CLI, crash auto-post, mobile-app support, rate limiting/abuse controls.
  - `api_design.md` / `database_schema.md` will be supplied by the user (C-001), not recreated.
  - The entrypoint runs migrations only when `RUN_MIGRATIONS=true`, so Stage 3 can run them once via a Job instead of in every replica.
- **Follow-ups:** —
