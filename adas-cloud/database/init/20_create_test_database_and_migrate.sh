#!/bin/bash
# Runs once inside the database container, the first time its data volume is empty
# (see docker-compose.yml). By then Postgres is up and POSTGRES_DB (adas_dev)
# already exists.
#
# 1. Create adas_test, so tests never share rows with manual development work.
# 2. Apply every migration to both databases, in file-name order (001, 002, ...).
#
# Known limitation until task C-005: this never runs again on an existing
# volume, so a NEW migration file needs `docker compose down -v` (deletes all
# data) or a manual `psql -f`.
set -euo pipefail

# ON_ERROR_STOP makes psql exit non-zero on the first failed statement instead
# of carrying on, so a broken migration stops the whole init loudly.
run_sql() {
	local database_name="$1"
	shift
	psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$database_name" "$@"
}

run_sql "$POSTGRES_DB" -c "CREATE DATABASE adas_test;"

for database_name in "$POSTGRES_DB" adas_test; do
	for migration_file in /migrations/*.sql; do
		echo "init: applying $(basename "$migration_file") to $database_name"
		run_sql "$database_name" -f "$migration_file"
	done
done
