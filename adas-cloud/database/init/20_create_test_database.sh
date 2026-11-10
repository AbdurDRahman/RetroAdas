#!/bin/bash
# Runs once inside the database container, the first time its data volume is empty
# (see docker-compose.yml). By then Postgres is up and POSTGRES_DB (adas_dev)
# already exists.
#
# Its only job: create adas_test, so tests never share rows with manual
# development work. It does NOT create any tables. Migrations are applied by
# the migrate program (task C-005): `go run ./cmd/migrate` for adas_dev, and
# the tests migrate adas_test themselves.
set -euo pipefail

# ON_ERROR_STOP makes psql exit non-zero if the statement fails, so a problem
# stops the init loudly instead of being skipped.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
	-c "CREATE DATABASE adas_test;"
