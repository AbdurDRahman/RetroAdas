package testdatabase

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// NewEmptyDatabase creates a brand-new database with no tables in it and drops
// it again when the test finishes. It exists for the migration runner's tests,
// which must start from nothing to prove "an empty database gets every
// migration". Every other test uses NewTransaction on the shared adas_test.
func NewEmptyDatabase(test *testing.T) *sql.DB {
	test.Helper()

	testDatabaseURL := os.Getenv("TEST_DATABASE_URL")
	require.NotEmpty(test, testDatabaseURL, "TEST_DATABASE_URL must be set — see .env.example")

	// A random suffix, so two test runs at the same time never pick the same name.
	randomBytes := make([]byte, 4)
	_, err := rand.Read(randomBytes)
	require.NoError(test, err)
	emptyDatabaseName := "adas_test_empty_" + hex.EncodeToString(randomBytes)

	// CREATE DATABASE is not allowed inside a transaction, so this uses a plain
	// connection to adas_test instead of NewTransaction.
	administrationPool, err := sql.Open("postgres", testDatabaseURL)
	require.NoError(test, err)
	_, err = administrationPool.Exec("CREATE DATABASE " + pq.QuoteIdentifier(emptyDatabaseName))
	require.NoError(test, err)

	// Same server, user and password; only the database name (the URL path) changes.
	emptyDatabaseURL, err := url.Parse(testDatabaseURL)
	require.NoError(test, err)
	emptyDatabaseURL.Path = "/" + emptyDatabaseName
	emptyDatabasePool, err := sql.Open("postgres", emptyDatabaseURL.String())
	require.NoError(test, err)

	test.Cleanup(func() {
		_ = emptyDatabasePool.Close()
		// WITH (FORCE) disconnects anything still connected, so the drop never hangs.
		_, _ = administrationPool.Exec("DROP DATABASE IF EXISTS " + pq.QuoteIdentifier(emptyDatabaseName) + " WITH (FORCE)")
		_ = administrationPool.Close()
	})

	return emptyDatabasePool
}
