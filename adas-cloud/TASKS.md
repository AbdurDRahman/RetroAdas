# TASKS — remaining cloud work

Work happens in the loop described in `README.md` ("How work is organized"): **pick → discuss → code → verify → close**.
When a task is closed it is **removed from this file** and logged in `DONE.md`.

Pick top-down within the current stage unless **Depends on** says otherwise. Never widen a
task's scope mid-flight — add a new task at the right place instead.

Task template:

```
### C-xxx Title
- **Goal:** one or two sentences — the why.
- **Done when:** testable acceptance criteria.
- **Touches:** files/dirs expected to change.
- **Depends on:** task IDs (or —).
- **Discuss:** decisions to settle in the discuss step, before any code.
- **Who:** me | claude | tbd   (claude = written via /code-with-me)
```

ID ranges: `C-0xx` Stage 0, `C-1xx` Stage 1, `C-2xx` Stage 2, `C-3xx` Stage 3.

---

## Stage 0 — Foundations

All Stage 0 tasks are done (see `DONE.md`).

---

## Stage 1 — Laptop (core API, all features, docker compose)

### C-206 Checks on every push (continuous integration)
- **Goal:** every push to GitHub runs the real-database test suite. Moved up from Stage 2: it is cheap, and it catches "works only on my laptop" early.
- **Done when:** the first run of `../.github/workflows/adas-cloud-tests.yml` is green on GitHub. The workflow is written and its steps were rehearsed locally against a fresh PostGIS container (formatting, `go vet`, build, `go test ./...`); only the first real run is left.
- **Touches:** `../.github/workflows/adas-cloud-tests.yml`.
- **Depends on:** C-003, C-005
- **Discuss:** — (path filters: only pushes touching `adas-cloud/` or the workflow file trigger it)
- **Who:** claude

### C-101 Device authentication
- **Goal:** every write is authenticated as `device_id` + API key against `devices`, with only a hash stored.
- **Done when:** `devices.Authenticate` and `seedDevice` are implemented as in `docs/design.md` section 7 — HMAC-SHA256 with the `API_KEY_PEPPER` pepper (read in `config/`, added to `.env.example`), constant-time comparison, a dummy hash for unknown devices, `401` for unknown device and wrong key alike, `403` for revoked; the `test.Skip` line at the top of `TestAuthenticate` is removed and all 4 cases pass; raw key never logged.
- **Touches:** `internal/devices/*`, `config/config.go`, `.env.example`.
- **Depends on:** C-003, C-004
- **Discuss:** how to test the dummy-hash timing rule (or accept it as a code-review item); `cmd/migrate` uses the same `config.Load()` as the server, so a required `API_KEY_PEPPER` would force the migrate program (and the Stage 3 Job) to carry a secret it never uses — keep the pepper out of what `migrate` needs. Hashing method decided in C-001: HMAC-SHA256 with a pepper, not bcrypt.
- **Who:** tbd

### C-102 Deduplication on write (`ProcessReport`)
- **Goal:** the core business logic — a re-report within the tag's deduplication radius of a live hazard with the same tag confirms it instead of inserting a duplicate.
- **Done when:** the event-tag registry from `docs/design.md` section 4 exists in Go (`internal/hazards/event_tags.go`, replacing the hard-coded `timeToLiveFor` and `deduplicationRadiusMeters`); `hazards.ProcessReport` and `seedHazard` are implemented; the `test.Skip` line at the top of `TestProcessReport` is removed and all its cases pass; new tests cover:
  - expired hazards are never matched;
  - time to live (TTL) resets on confirmation;
  - **the same device re-reporting a hazard does not add a confirmation or reset its expiry** (bug found reviewing the drafts — each device counts once per hazard, e.g. a `hazard_confirmations(hazard_id, device_id)` table);
  - when two live hazards of the same tag are inside the deduplication radius, the **nearest** one is matched (the drafts' `LIMIT 1` has no `ORDER BY`);
  - two simultaneous reports of the same hazard give 1 row + 1 confirmation, not 2 rows;
  - the `source` column and the `source`/`confidence` check constraint (`manual` ⇔ `confidence` is null) are in place;
  - schema matches `docs/design.md` section 9: `hazards.id` is `GENERATED ALWAYS AS IDENTITY`, `confirmations` starts at 1, `confidence` is nullable, and the original reporter gets a `hazard_confirmations` row together with the hazard.
- **Touches:** `internal/hazards/deduplication*.go`, `internal/hazards/event_tags.go`, new migrations for confirmations and for `source` + the check constraint.
- **Depends on:** C-001, C-101 (fixture style), C-004
- **Discuss:** whether to split this task in three (registry + schema changes; deduplication with one confirmation per device; the simultaneous-reports race) — it is large for one task. The race between SELECT and INSERT — `pg_advisory_xact_lock` on (tag, grid cell), `SERIALIZABLE` + retry, or accept the rare duplicate? Time to live and deduplication radius come from the Go registry and are passed to the query as parameters.
- **Who:** tbd

### C-103 Projected lookahead search (`FindNearby`)
- **Goal:** return the live hazards ahead of the vehicle, using the agreed speed-based rule: `search_radius = lookahead_distance = poll_distance = speed × time_between_polls` (minimum lookahead distance, maximum search radius of 1 kilometre — see `docs/design.md`).
- **Done when:** `hazards.FindNearby` is implemented; the server computes lookahead distance and search radius from `speed_kilometers_per_hour` (`SearchQuery.RadiusMeters` and the fixed `LookaheadDistanceMeters` are removed — the client never sends a radius); the response matches `docs/design.md` section 8 (`distance_meters` from the vehicle's current position, nearest first, at most 100, plus `next_poll`); `TestFindNearby` has real cases (hazard inside the search circle returned; hazard behind the car not returned; hazard outside the radius not returned; expired hazard not returned; results ordered by distance; slow speed uses the minimum lookahead distance).
- **Touches:** `internal/hazards/nearby_search*.go`, `config/` (time between polls, minimum lookahead distance, maximum search radius).
- **Depends on:** C-001, C-102
- **Discuss:** how to test "hazard behind the car" reliably near the minimum lookahead distance. (Result limit and no client radius decided in C-001.)
- **Who:** tbd

### C-104 Handshake
- **Goal:** devices check in at boot so a misconfigured or revoked device finds out immediately (`docs/design.md` section 8).
- **Done when:** `handshake.Handle` authenticates and returns only `device_id` + `device_type` (the polling fields in `Response` are removed — polling values come from `GET /v1/hazards`); tests cover OK / unknown / revoked.
- **Touches:** `internal/handshake/*`.
- **Depends on:** C-101
- **Discuss:** —
- **Who:** tbd

### C-105 HTTP layer
- **Goal:** expose the three endpoints over HTTP with consistent authentication, validation and errors.
- **Done when:** `POST /v1/devices/handshake`, `POST /v1/hazards`, `GET /v1/hazards` are routed in `cmd/server`; authentication middleware reads `X-Device-Id` / `X-Api-Key` (same as the k6 scripts); status codes, bodies and the error shape follow `docs/design.md` section 8 (`created` → 201, `confirmed` / `already_reported` → 200; `source` derived from the device type); `event_tag` checked against the Go registry (unknown tag → 400, tag not allowed for the device type → 403); handler tests use `httptest`.
- **Touches:** `cmd/server/main.go`, new `internal/httpapi/` (or similar), core package signatures.
- **Depends on:** C-102, C-103, C-104
- **Discuss:** core functions currently take `*sql.Tx` — switch to a small `Querier` interface so handlers can pass `*sql.DB` or a transaction and tests keep using rollback transaction? Package name/layout for handlers. (Error shape decided in C-001.)
- **Who:** tbd

### C-106 Server hardening
- **Goal:** make the server behave well in a container and behind a load balancer.
- **Done when:** read/write/idle timeouts set; graceful shutdown on SIGTERM; `GET /healthz` (process up) and `GET /readyz` (database ping); structured logs via `log/slog` (request ID, status, latency, no secrets, and **never coordinates together with a device ID** — `docs/design.md` section 7); request body size limit; every response carries `X-Instance-Id` (hostname by default — needed for the Stage 3 demo); database connection pool limits come from `config`; **connections are reused** (HTTP keep-alive / HTTP/2 idle timeout long enough to span the time between polls, so a vehicle pays the TLS handshake once, not every poll); **responses are gzip-compressed** when the client sends `Accept-Encoding: gzip` (list responses repeat every field name per hazard).
- **Touches:** `cmd/server/main.go`, `config/config.go`, `.env.example`.
- **Depends on:** C-105
- **Discuss:** log format (JSON vs text); idle timeout value vs time between polls (a load balancer in front may impose its own); whether keep-alive / gzip are handled by the Go server or by the reverse proxy (Stage 2) / ingress (Stage 3) — likely both configured, proxy wins.
- **Who:** tbd

### C-107 Device provisioning CLI
- **Goal:** create and revoke devices without hand-written SQL — needed for real units, the mobile app, and k6.
- **Done when:** `go run ./cmd/provision create --type jetson_unit` prints a new `device_id` + raw key **once** and stores only the hash; `revoke <device_id>` works; it reuses the hash function from C-101.
- **Touches:** `cmd/provision/`, `internal/devices/`.
- **Depends on:** C-101
- **Discuss:** — (formats decided in C-001: `device_id` = type prefix + 8 random hexadecimal characters, e.g. `jetson-4f9c2a1b`; key = 32 random bytes as URL-safe base64, shown once).
- **Who:** tbd

### C-109 Crash auto-post (Feature 3 integration)
- **Goal:** a confirmed crash, sent by the Jetson as `event_tag: "accident"`, shows up for nearby cars.
- **Done when:** the contract in `../adas-automatic-crash-detection/CLAUDE.md` is confirmed with the team; `accident` is on the allow-list with its TTL; an end-to-end handler test posts an accident and finds it via `GET /v1/hazards`.
- **Touches:** `internal/hazards/`, handler tests, `docs/design.md`.
- **Depends on:** C-105
- **Discuss:** should crashes skip dedup or use a smaller radius? Any extra fields (severity)? Is the auto-post agreed by the team (it's flagged "needs confirmation" in the feature spec)?
- **Who:** tbd

### C-110 Abuse controls
- **Goal:** stop spam or false reports from undermining trust, without breaking statelessness.
- **Done when:** per-device rate limit on `POST /v1/hazards` kept in the database (`docs/design.md` section 10: a counter table, about 60 reports per device per hour, `429 rate_limited` with `Retry-After`); tests cover it. (Stopping one device from inflating `confirmations` is done by `hazard_confirmations` in C-102.)
- **Touches:** a new migration for the counter table, `internal/hazards/`, middleware.
- **Depends on:** C-102, C-105
- **Discuss:** fixed or sliding time window for the counter; how old counter rows are cleaned up. (Decided in C-001: per-device limit in the database, per-IP limits at the proxy / ingress in Stage 2/3; GET needs full authentication.)
- **Who:** tbd

### C-111 Mobile-app support
- **Goal:** the companion app can submit manual reports, show a hazard map, and show the user's contribution score.
- **Done when:** `GET /v1/hazards/map` (bounding box, at most about 20 × 20 kilometers, at most 500 results) and `GET /v1/devices/me/contributions` (raw counts) work as in `docs/design.md` section 8; manual reports are distinguishable (the `source` column from C-102); tests pass.
- **Touches:** `database/migrations/00x_*.sql`, `internal/hazards/`, new handlers, docs.
- **Depends on:** C-105, C-110 (confirmation tracking feeds the score)
- **Discuss:** score formula; per-user vs per-device identity for the app. (Decided in C-001: a manual report's `confidence` is null.)
- **Who:** tbd

### C-114 Mobile-app device registration
- **Goal:** `POST /v1/devices/register` (`docs/design.md` section 7) — phones can't be pre-provisioned like Jetson units, and a key built into the app can be extracted from it. Phones need a way to get their own `device_id` + API key.
- **Done when:** a registration endpoint issues a key to a new phone install (stores only the hash); it is rate limited per source; registered phones are `device_type = mobile_app` and can be revoked like any device.
- **Touches:** `internal/devices/`, new handler, `docs/design.md`.
- **Depends on:** C-105, C-107, C-110
- **Discuss:** abuse controls on registration (rate limit per IP? app attestation is out of scope); (trust level decided in C-001: same as Jetson units; manual reports are marked by `source`).
- **Who:** tbd

### C-112 Container image
- **Goal:** the server ships as one small image used unchanged in all three stages.
- **Done when:** multi-stage `Dockerfile` (Go build → small runtime image) builds both `cmd/server` and `cmd/migrate` into `/app/server` and `/app/migrate` (the path `docker-entrypoint.sh` calls); uses `docker-entrypoint.sh` as `ENTRYPOINT`; `api` service added to `docker-compose.yml`; `docker compose up` gives a working API on :8080 with migrations applied.
- **Touches:** `Dockerfile`, `.dockerignore`, `docker-compose.yml`, `docker-entrypoint.sh`.
- **Depends on:** C-005, C-106
- **Discuss:** runtime base (distroless vs alpine — the entrypoint needs `/bin/sh`, and `pg_isready` if `WAIT_FOR_DATABASE` is used).
- **Who:** tbd

### C-113 Local k6 baseline
- **Goal:** prove the full stack works under simulated fleet traffic before touching Azure.
- **Done when:** both k6 scripts run via the `grafana/k6` Docker image against the compose stack using keys from C-107; burst test shows deduplication (N devices give 1 row with `confirmations` = N, because the first reporter counts — `docs/design.md` section 6); each virtual device sends its own key (keys are per device, so the burst script's single shared `API_KEY` cannot work); p95 latency + error rate recorded in `docs/results.md`.
- **Touches:** `load-tests/k6/*`, `docs/results.md`.
- **Depends on:** C-107, C-112
- **Discuss:** how the scripts receive one `device_id` + key per virtual device (for example a file written by the C-107 tool); scenario sizes.
- **Who:** tbd

---

## Stage 2 — Single Azure VM

### C-201 Provision the VM
- **Goal:** one VM that runs the same compose stack as the laptop.
- **Done when:** VM up (size + region recorded), NSG allows only 22/80/443, Docker installed, SSH key access only; steps written in `docs/deploy-vm.md`.
- **Touches:** `docs/deploy-vm.md`, optional `deploy/vm/` scripts.
- **Depends on:** C-113
- **Discuss:** VM size vs credits; Postgres on the same VM for now (yes, per feature-2).
- **Who:** tbd

### C-202 Image registry + build/push
- **Goal:** the VM (and later the cluster) pulls versioned images rather than building on the server.
- **Done when:** image pushed to GHCR or ACR with a git-sha tag; script/Make target to build+push.
- **Touches:** `Makefile` or `scripts/`, docs.
- **Depends on:** C-112
- **Discuss:** GHCR (free) vs ACR (Azure-native, costs credits).
- **Who:** tbd

### C-203 Production compose on the VM
- **Goal:** a durable deploy: data survives restarts, secrets aren't in git.
- **Done when:** `deploy/vm/docker-compose.prod.yml` with api + postgis (named volume, restart policies, healthchecks); `.env` lives only on the VM; deploy = pull + `up -d`.
- **Touches:** `deploy/vm/`, `docs/deploy-vm.md`.
- **Depends on:** C-201, C-202
- **Discuss:** keep Postgres port closed to the internet (yes).
- **Who:** tbd

### C-204 HTTPS
- **Goal:** devices send API keys in headers, so plain HTTP isn't acceptable outside the laptop.
- **Done when:** reverse proxy in front of the API terminates TLS; HTTP redirects to HTTPS; `curl https://.../healthz` works.
- **Touches:** `deploy/vm/` (Caddyfile or nginx conf).
- **Depends on:** C-203
- **Discuss:** domain name available? Caddy auto-TLS needs one; otherwise a self-signed cert and pinned on devices.
- **Who:** tbd

### C-205 Backups + migrations on deploy
- **Goal:** don't lose data, and make schema changes routine.
- **Done when:** nightly `pg_dump` with retention; restore tested once; deploy applies pending migrations via the C-005 runner.
- **Touches:** `deploy/vm/`, docs.
- **Depends on:** C-203, C-005
- **Discuss:** where backups go (VM disk vs Azure Blob).
- **Who:** tbd

### C-207 k6 against the VM + sharing-time KPI
- **Goal:** measure the deployed system against the Feature 2 KPIs.
- **Done when:** both k6 scripts run from the laptop against the VM over HTTPS; results in `docs/results.md` for both halves of the redefined sharing key performance indicator:
  - **report → queryable on the server** (POST by device A, then an immediate GET finds it): target ≤ 3 seconds;
  - **worst-case staleness** for a polling car: bounded by time between polls (or the stationary fallback timer).
- **Touches:** `load-tests/k6/`, `docs/results.md`.
- **Depends on:** C-204
- **Discuss:** k6 polling model that follows the speed-based poll distance.
- **Who:** tbd

---

## Stage 3 — Multi-server Kubernetes

### C-301 Choose the Kubernetes flavor
- **Goal:** decide how the multi-server cluster is built.
- **Done when:** decision + credit-cost estimate recorded in `DONE.md`.
- **Touches:** `docs/deploy-k8s.md`.
- **Depends on:** C-207
- **Discuss:** k3s on Azure VMs (cheap, hands-on) vs AKS (managed, more credits) vs kubeadm (most realistic, heaviest); node count and size.
- **Who:** tbd

### C-302 Postgres on its own VM
- **Goal:** split the database from the API, as planned in feature-2 — only `DATABASE_URL` changes.
- **Done when:** database VM running the pinned PostGIS version, data migrated from the Stage 2 VM, reachable only from cluster nodes; the API pool sized so replicas × `max_open_conns` < `max_connections`.
- **Touches:** `deploy/`, `config/` (pool settings), docs.
- **Depends on:** C-301
- **Discuss:** Postgres in Docker vs native on the database VM.
- **Who:** tbd

### C-303 Provision the cluster
- **Goal:** control plane + workers running and reachable.
- **Done when:** nodes joined and `Ready`; `kubectl` works from the laptop; private networking to the database VM.
- **Touches:** `deploy/k8s/` scripts, docs.
- **Depends on:** C-301
- **Discuss:** depends on C-301.
- **Who:** tbd

### C-304 Kubernetes manifests
- **Goal:** run the same image as N identical replicas.
- **Done when:** Deployment (replicas, resources, liveness `/healthz`, readiness `/readyz`, `RUN_MIGRATIONS=false`), Service, ConfigMap, Secret, and a migration Job (runs `/app/migrate` from the same image, C-005); `kubectl apply` brings it up from scratch.
- **Touches:** `deploy/k8s/`.
- **Depends on:** C-302, C-303, C-106
- **Discuss:** plain YAML vs Kustomize; how the migration Job is run before rollout.
- **Who:** tbd

### C-305 Ingress + TLS
- **Goal:** one HTTPS entry point load-balancing across the pods, with no sticky sessions.
- **Done when:** Ingress (Traefik on k3s / nginx / Azure LB) with TLS; responses show different `X-Instance-Id` values.
- **Touches:** `deploy/k8s/`.
- **Depends on:** C-304
- **Discuss:** cert-manager vs manual cert; ingress-level rate limiting (ties into C-110).
- **Who:** tbd

### C-306 Horizontal Pod Autoscaler
- **Goal:** scale replicas with load automatically.
- **Done when:** metrics-server installed; HPA on CPU; scaling observed under k6 load.
- **Touches:** `deploy/k8s/`.
- **Depends on:** C-305
- **Discuss:** min/max replicas; CPU target.
- **Who:** tbd

### C-307 Archive expired hazards
- **Goal:** expired hazards are kept in `hazards` until now (decided in C-001, `docs/design.md` section 6); at the end of the project, move them to a separate archive store so the live table stays small while the history is kept.
- **Done when:** an archive command moves hazards past expiry (plus their confirmations) into the archive store in one transaction, is safe to run on several replicas at once, and runs on a schedule (Kubernetes CronJob); the contribution score still counts archived hazards; logs show how many rows moved.
- **Touches:** `cmd/archive/` (or a flag), `internal/hazards/`, a migration or second database, `deploy/k8s/`.
- **Depends on:** C-102, C-111 (contribution score), C-304
- **Discuss:** separate database vs separate table or schema in the same database; how long after expiry a hazard is moved; schedule.
- **Who:** tbd

### C-308 Scalability demonstration
- **Goal:** visibly show request distribution and flat latency as load increases — the Feature 2 demo.
- **Done when:** k6 ramp scenario; results show `X-Instance-Id` distribution and p95 latency vs replica count (1 → 2 → 3+); written up in `docs/results.md`; teardown script destroys the cluster to save credits.
- **Touches:** `load-tests/k6/`, `docs/results.md`, `deploy/k8s/teardown.*`.
- **Depends on:** C-306
- **Discuss:** what charts/tables the demo needs for the FYP report.
- **Who:** tbd

### C-309 Future-work notes
- **Goal:** record what we deliberately left out.
- **Done when:** `docs/future-work.md` covers PgBouncer, read replicas, and anything else found along the way, each with a "when it would be needed" trigger based on our measured numbers.
- **Touches:** `docs/future-work.md`.
- **Depends on:** C-308
- **Discuss:** —
- **Who:** tbd
