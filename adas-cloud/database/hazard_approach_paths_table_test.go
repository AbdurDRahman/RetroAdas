package database_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
)

// A short line of three points leading up to the hazard that
// insertHazardReturningID places at (73.0490, 33.6850). PostGIS writes a point
// as "longitude latitude".
const approachPathText = "LINESTRING(73.0480 33.6858, 73.0485 33.6854, 73.0490 33.6850)"

func insertApproachPath(transaction *sql.Tx, hazardID int64, pathText string) error {
	_, err := transaction.Exec(
		`INSERT INTO hazard_approach_paths (hazard_id, path) VALUES ($1, ST_GeogFromText($2))`,
		hazardID, pathText,
	)
	return err
}

func insertApproachPathCount(transaction *sql.Tx, hazardID int64, deviceID string, pathsAdded int) error {
	_, err := transaction.Exec(
		`INSERT INTO hazard_approach_path_counts (hazard_id, device_id, paths_added) VALUES ($1, $2, $3)`,
		hazardID, deviceID, pathsAdded,
	)
	return err
}

// hazard_approach_paths holds the ways vehicles reached each hazard
// (docs/design.md sections 5 and 9).
func TestHazardApproachPathsTableRules(test *testing.T) {
	test.Run("a hazard can have several paths", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		require.NoError(test, insertApproachPath(transaction, hazardID, approachPathText))
		require.NoError(test, insertApproachPath(transaction, hazardID, approachPathText))
	})

	test.Run("a path must point to a hazard that exists", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		err := insertApproachPath(transaction, hazardID+1000, approachPathText)

		requireRefusedBy(test, err, "hazard_approach_paths_hazard_id_fkey")
	})

	test.Run("a path must be a line, not a single point", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		err := insertApproachPath(transaction, hazardID, "POINT(73.0490 33.6850)")

		// The column's type refuses this, not a named rule, so the test
		// checks PostGIS's own message.
		assert.ErrorContains(test, err, "does not match column type")
	})

	// A path is location history, so it is stored without the device's
	// identity and without times (design section 7). This test fails the
	// moment someone adds such a column, which makes it a decision, not an
	// accident.
	test.Run("the table has no device and no time column", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)

		rows, err := transaction.Query(
			`SELECT column_name FROM information_schema.columns
			 WHERE table_name = 'hazard_approach_paths' ORDER BY ordinal_position`)
		require.NoError(test, err)
		defer rows.Close()

		var actualColumnNames []string
		for rows.Next() {
			var columnName string
			require.NoError(test, rows.Scan(&columnName))
			actualColumnNames = append(actualColumnNames, columnName)
		}
		require.NoError(test, rows.Err())
		assert.Equal(test, []string{"id", "hazard_id", "path"}, actualColumnNames)
	})

	// With no time column, "the oldest path" is the one with the lowest id.
	test.Run("a later path gets a higher id", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)
		require.NoError(test, insertApproachPath(transaction, hazardID, approachPathText))
		require.NoError(test, insertApproachPath(transaction, hazardID, approachPathText))

		var lowestID, highestID int64
		err := transaction.QueryRow(
			`SELECT min(id), max(id) FROM hazard_approach_paths WHERE hazard_id = $1`, hazardID,
		).Scan(&lowestID, &highestID)

		require.NoError(test, err)
		assert.Greater(test, highestID, lowestID)
	})
}

// hazard_approach_path_counts remembers how many paths each device has added
// to each hazard, for the limit per device (docs/design.md section 5). It
// never says which paths.
func TestHazardApproachPathCountsTableRules(test *testing.T) {
	test.Run("two devices each have their own count for one hazard", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		require.NoError(test, insertApproachPathCount(transaction, hazardID, hazardReporterDeviceID, 1))
		require.NoError(test, insertApproachPathCount(transaction, hazardID, secondDeviceID, 2))
	})

	test.Run("one device has only one count per hazard", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)
		require.NoError(test, insertApproachPathCount(transaction, hazardID, secondDeviceID, 1))

		err := insertApproachPathCount(transaction, hazardID, secondDeviceID, 1)

		requireRefusedBy(test, err, "hazard_approach_path_counts_pkey")
	})

	test.Run("a count must point to a hazard that exists", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		err := insertApproachPathCount(transaction, hazardID+1000, secondDeviceID, 1)

		requireRefusedBy(test, err, "hazard_approach_path_counts_hazard_id_fkey")
	})

	test.Run("a count must point to a device that exists", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		err := insertApproachPathCount(transaction, hazardID, "jetson-00000000", 1)

		requireRefusedBy(test, err, "hazard_approach_path_counts_device_id_fkey")
	})

	// A row only exists once a device has added a path, so it starts at 1.
	test.Run("a count is never below 1", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		err := insertApproachPathCount(transaction, hazardID, secondDeviceID, 0)

		requireRefusedBy(test, err, "hazard_approach_path_counts_paths_added_check")
	})
}
