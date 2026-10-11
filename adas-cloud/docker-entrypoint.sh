#!/bin/sh
# Container entry point for the adas-cloud API image (wired up in task C-112).
#
# Same image, three stages — behaviour is chosen purely by env vars:
#   Stage 1/2 (docker compose): WAIT_FOR_DATABASE=true  RUN_MIGRATIONS=true
#   Stage 3   (Kubernetes):     WAIT_FOR_DATABASE=false RUN_MIGRATIONS=false
#     -> migrations run once from a separate Job, never from every replica,
#        and readiness is handled by the /readyz probe instead of waiting here.
#
# Usage (as Dockerfile ENTRYPOINT, with the server as CMD):
#   ENTRYPOINT ["/docker-entrypoint.sh"]
#   CMD ["/app/server"]
set -eu

log() {
	echo "entrypoint: $*" >&2
}

# --- 1. Required env -------------------------------------------------------
# config.Load() fails on this too, but failing here gives a clearer message
# before any DB waiting or migration work starts.
if [ -z "${DATABASE_URL:-}" ]; then
	log "DATABASE_URL is not set (see .env.example)"
	exit 1
fi

WAIT_FOR_DATABASE="${WAIT_FOR_DATABASE:-false}"
DATABASE_WAIT_RETRIES="${DATABASE_WAIT_RETRIES:-30}"
RUN_MIGRATIONS="${RUN_MIGRATIONS:-false}"

# --- 2. Optionally wait for Postgres ---------------------------------------
# docker compose's depends_on doesn't wait for Postgres to accept connections,
# so on Stage 1/2 we poll for up to DATABASE_WAIT_RETRIES seconds.
wait_for_database() {
	if ! command -v pg_isready >/dev/null 2>&1; then
		log "WAIT_FOR_DATABASE=true but pg_isready is not installed in this image"
		exit 1
	fi

	attempt_number=0
	until pg_isready -q -d "$DATABASE_URL"; do
		attempt_number=$((attempt_number + 1))
		if [ "$attempt_number" -ge "$DATABASE_WAIT_RETRIES" ]; then
			log "database not ready after ${DATABASE_WAIT_RETRIES}s, giving up"
			exit 1
		fi
		sleep 1
	done
	log "database is ready"
}

# --- 3. Optionally run migrations ------------------------------------------
run_migrations() {
	# TODO(C-005): call the chosen migration runner here (psql loop over
	# database/migrations/*.sql, or a Go `migrate` binary). It must be
	# idempotent: running it twice is a no-op.
	log "RUN_MIGRATIONS=true but no migration runner exists yet (task C-005)"
	exit 1
}

if [ "$WAIT_FOR_DATABASE" = "true" ]; then
	wait_for_database
fi

if [ "$RUN_MIGRATIONS" = "true" ]; then
	run_migrations
fi

# --- 4. Hand over to the server --------------------------------------------
# exec replaces this shell, so the server becomes PID 1 and receives SIGTERM
# directly from docker/kubernetes for graceful shutdown (task C-106).
if [ "$#" -eq 0 ]; then
	log "no command given (expected the server binary as CMD)"
	exit 1
fi

exec "$@"
