// Package database provides the shared Postgres+PostGIS connection used by
// the running server. Test-only connection/transaction helpers live in
// database/testdatabase so they're never accidentally imported by production code.
package database

import (
	"database/sql"

	_ "github.com/lib/pq"
)

// Connect opens a connection pool to the Postgres+PostGIS database at the
// given DSN. Callers (cmd/server/main.go) are responsible for closing it on
// shutdown.
func Connect(databaseURL string) (*sql.DB, error) {
	connectionPool, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}

	if err := connectionPool.Ping(); err != nil {
		return nil, err
	}

	return connectionPool, nil
}
