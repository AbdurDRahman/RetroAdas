// External test package (database_test, not database): it uses the database
// package only through its exported functions, exactly like cmd/migrate will,
// and it can import testdatabase without an import cycle.
package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
)

// Every file in database/migrations, in the order they must run. Adding a
// migration means adding its name here, which is deliberate: the test then
// notices a file that was added but not picked up (or picked up out of order).
var expectedMigrationFileNames = []string{
	"001_create_devices_table.sql",
	"002_create_hazards_table.sql",
}

func TestMigrateAppliesEveryFileToAnEmptyDatabaseThenNothing(test *testing.T) {
	connectionPool := testdatabase.NewEmptyDatabase(test)
	cntx := context.Background()

	actualFirstRunFileNames, err := database.Migrate(cntx, connectionPool)
	require.NoError(test, err)
	assert.Equal(test, expectedMigrationFileNames, actualFirstRunFileNames)

	// The SQL really ran, not just the bookkeeping: the tables now exist.
	// to_regclass returns NULL for a table name that doesn't exist.
	var hazardsTableExists bool
	err = connectionPool.QueryRow("SELECT to_regclass('hazards') IS NOT NULL").Scan(&hazardsTableExists)
	require.NoError(test, err)
	assert.True(test, hazardsTableExists)

	// The "running it twice is a no-op" rule from C-005.
	actualSecondRunFileNames, err := database.Migrate(cntx, connectionPool)
	require.NoError(test, err)
	assert.Empty(test, actualSecondRunFileNames)
}

// Two runs starting at the same moment (Stage 3: several replicas, or a Job
// that overlaps a manual run) must not both apply the same file. The lock makes
// one wait for the other, so between them every file is applied exactly once.
func TestMigrateAppliesEachFileOnceWhenTwoRunsStartTogether(test *testing.T) {
	connectionPool := testdatabase.NewEmptyDatabase(test)
	cntx := context.Background()

	type migrateOutcome struct {
		appliedFileNames []string
		err              error
	}
	outcomes := make(chan migrateOutcome, 2)
	for range 2 {
		go func() {
			appliedFileNames, err := database.Migrate(cntx, connectionPool)
			outcomes <- migrateOutcome{appliedFileNames, err}
		}()
	}

	var actualAppliedFileNames []string
	for range 2 {
		outcome := <-outcomes
		require.NoError(test, outcome.err)
		actualAppliedFileNames = append(actualAppliedFileNames, outcome.appliedFileNames...)
	}
	assert.ElementsMatch(test, expectedMigrationFileNames, actualAppliedFileNames)
}
