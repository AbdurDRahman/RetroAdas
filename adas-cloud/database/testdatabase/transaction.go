// Package testdatabase provides the shared setup used by every test package in
// this project that needs a real database (as opposed to a pure-logic unit
// test with no database involvement at all).
//
// It is named testdatabase, not testing, so it never collides with Go's
// standard "testing" package and can be imported without an alias.
//
// Every test gets its own transaction against the real Postgres+PostGIS
// instance pointed to by TEST_DATABASE_URL, which is rolled back at the end
// of the test. This means:
//   - Tests never leave data behind for the next test to trip over.
//   - No mocking of PostGIS functions (ST_DWithin, ST_Project) is needed —
//     tests run against the genuine geography behavior.
//   - Today TEST_DATABASE_URL points at the local Docker container; later it
//     can point anywhere without any test code changing.
package testdatabase

import (
	"database/sql"
	"os"
	"sync"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database"
)

// Go builds every package's tests into a separate process, so "once" here
// means once per package per `go test` run. Several packages migrating at the
// same moment is safe: the migration lock makes them take turns (C-005).
var (
	migrateTestDatabaseOnce sync.Once
	migrationError          error
)

// NewTransaction opens a transaction on the shared test database and
// registers a cleanup hook that rolls it back once the test finishes,
// regardless of pass/fail.
func NewTransaction(test *testing.T) *sql.Tx {
	test.Helper()

	testDatabaseURL := os.Getenv("TEST_DATABASE_URL")
	require.NotEmpty(test, testDatabaseURL, "TEST_DATABASE_URL must be set — see .env.example")

	connectionPool, err := sql.Open("postgres", testDatabaseURL)
	require.NoError(test, err)

	// The first test in each test process brings adas_test up to date, so tests
	// always run against the current schema with no manual step. Every later
	// test in that process skips straight to the stored result.
	migrateTestDatabaseOnce.Do(func() {
		_, migrationError = database.Migrate(test.Context(), connectionPool)
	})
	require.NoError(test, migrationError, "migrating adas_test failed")

	transaction, err := connectionPool.Begin()
	require.NoError(test, err)

	test.Cleanup(func() {
		_ = transaction.Rollback()
		_ = connectionPool.Close()
	})

	return transaction
}
