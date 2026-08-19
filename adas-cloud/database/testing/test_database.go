// Package testing provides the shared setup used by every test package in
// this project that needs a real database (as opposed to a pure-logic unit
// test with no DB involvement at all).
//
// Every test gets its own transaction against the real Postgres+PostGIS
// instance pointed to by TEST_DATABASE_URL, which is rolled back at the end
// of the test. This means:
//   - Tests never leave data behind for the next test to trip over.
//   - No mocking of PostGIS functions (ST_DWithin, ST_Project) is needed —
//     tests run against the genuine geography behavior.
//   - Today TEST_DATABASE_URL points at the same VM as everything else;
//     later it can point anywhere without any test code changing.
package testing

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// NewTransaction opens a transaction on the shared test database and
// registers a cleanup hook that rolls it back once the test finishes,
// regardless of pass/fail.
func NewTransaction(t *testing.T) *sql.Tx {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	require.NotEmpty(t, dsn, "TEST_DATABASE_URL must be set — see .env.example")

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = tx.Rollback()
		_ = db.Close()
	})

	return tx
}
