# DONE — completed cloud work

Newest first. A task moves here from `TASKS.md` when it's closed (step 5 of the loop in
"How work is organized" in `README.md`).

Entries stay short. Design decisions are written in `docs/design.md` (decision log,
section 13), so an entry only points there. **Decided** is for the things the design
doesn't hold: how a tool is set up, what was rejected, what to remember next time. The
full detail of a change is in its commit.

Entry template:

```
### C-xxx Title — YYYY-MM-DD — <short sha>
- **Changed:** one or two lines.
- **Decided:** what `docs/design.md` doesn't already record (or "design section N").
- **Follow-ups:** new task IDs added to TASKS.md (or —)
```

---

### C-005 Migration runner — 2026-11-10 — `ad4c921`
- **Changed:** `database.Migrate` (goose, `.sql` files built into the program) and `cmd/migrate`; the entry point calls `/app/migrate`; the database init script only creates `adas_test`; tests migrate `adas_test` themselves.
- **Decided:** design section 9. Also: goose v3.26.0 is the newest release that works with Go 1.24; a new migration's file name must be added to `expectedMigrationFileNames` in `database/migrate_test.go`; the migration tests need a database user that may create databases.
- **Follow-ups:** — (C-112 builds `/app/migrate`; C-304's Job runs it)

### C-001 Finalize the backend design — 2026-11-04 — `30be8c6`
- **Changed:** `docs/design.md` agreed section by section; the feature spec, `TASKS.md` (C-101 … C-114) and the k6 polling script brought in line; the two early drafts marked superseded; C-108 (deleting expired hazards) removed.
- **Decided:** everything is in the design's decision log (section 13).
- **Follow-ups:** the Jetson detection code needs the "report each hazard once" rule (outside `adas-cloud`; pass it to whoever owns it).

### C-006 Refactor the codebase to the naming rules — 2026-10-11 — `376fdb0`
- **Changed:** every abbreviated name spelled out across Go, JSON fields, query parameters, database columns and indexes, environment variables, scripts and test data; `database/testing` → `database/testdatabase`; compose service `db` → `database`.
- **Decided:** design section 12. Also fixed a C-003 bug: the database health check now uses TCP (`pg_isready -h 127.0.0.1`), because over the Unix socket it reported "healthy" while the image's temporary start-up server was still running.
- **Follow-ups:** C-106 gained connection reuse and gzip responses.

### C-003 Local Postgres+PostGIS in Docker — 2026-10-11 — `376fdb0`
- **Changed:** `docker-compose.yml` with `postgis/postgis:17-3.5`, a named volume, a health check and statement logging; an init script that creates `adas_test`; `.gitignore` covers `.env`.
- **Decided:** Postgres 17.5 + PostGIS 3.5.2 is pinned, and Stages 2 and 3 must run the same version. `.env` is loaded by the shell (`set -a; source .env; set +a`), never by the program (`godotenv` rejected), so the program reads plain environment variables in every stage. The init script is mounted as a single file so the image's own `10_postgis.sh` still runs first. Statement logging is for development only.
- **Follow-ups:** —

### C-004 Make the test suite compile (red, not broken) — 2026-10-11 — `376fdb0`
- **Changed:** the `seedDevice` and `seedHazard` fixtures take `*sql.Tx`, so the tests compile.
- **Decided:** fixtures share the test's transaction, because seeded rows are uncommitted and only that transaction can see them. A shared query interface, if any, is decided in C-105.
- **Follow-ups:** —

### C-002 Real module path + dependency lockfile — 2026-10-11 — `376fdb0`
- **Changed:** module path `github.com/AbdurDRahman/RetroAdas/adas-cloud`, Go 1.24, `go.sum` created.
- **Decided:** the module path follows the repository layout (repository + `adas-cloud/` folder).
- **Follow-ups:** —

### C-000 Workflow files + staged roadmap — 2026-10-06 — `376fdb0`
- **Changed:** added `TASKS.md`, `DONE.md`, `CLAUDE.md` and the `docker-entrypoint.sh` skeleton.
- **Decided:** the loop pick → discuss → code → verify → close; the stages laptop → single Azure VM → Kubernetes; the entry point runs migrations only when `RUN_MIGRATIONS=true`, so Stage 3 can run them once from a Job.
- **Follow-ups:** —
