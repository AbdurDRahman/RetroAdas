package database_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
)

// A second device, so tests can have two different devices confirm one hazard.
const secondDeviceID = "phone-91d0e7c3"

// insertTwoDevicesAndAHazard sets up what every test here needs: the reporter
// device, a second device, and one hazard reported by the first. It returns
// the hazard's id.
func insertTwoDevicesAndAHazard(test *testing.T, transaction *sql.Tx) int64 {
	test.Helper()
	insertHazardReporter(test, transaction)

	_, err := transaction.Exec(
		`INSERT INTO devices (device_id, api_key_hash, device_type) VALUES ($1, 'not-a-real-hash', 'mobile_app')`,
		secondDeviceID,
	)
	require.NoError(test, err)

	return insertHazardReturningID(test, transaction)
}

func insertHazardReturningID(test *testing.T, transaction *sql.Tx) int64 {
	test.Helper()
	var hazardID int64
	err := transaction.QueryRow(
		`INSERT INTO hazards (event_tag, location, confidence, source, reported_by, expires_at)
		 VALUES ('pothole', ST_MakePoint(73.0490, 33.6850)::geography, 0.9, 'auto', $1, now() + interval '1 day')
		 RETURNING id`,
		hazardReporterDeviceID,
	).Scan(&hazardID)
	require.NoError(test, err)
	return hazardID
}

func insertConfirmation(transaction *sql.Tx, hazardID int64, deviceID string) error {
	_, err := transaction.Exec(
		`INSERT INTO hazard_confirmations (hazard_id, device_id) VALUES ($1, $2)`,
		hazardID, deviceID,
	)
	return err
}

// requireRefusedBy checks that an insert failed, and failed because of the
// named rule and nothing else.
func requireRefusedBy(test *testing.T, err error, expectedConstraint string) {
	test.Helper()
	var databaseError *pq.Error
	require.True(test, errors.As(err, &databaseError), "expected a database error, got: %v", err)
	assert.Equal(test, expectedConstraint, databaseError.Constraint)
}

// hazard_confirmations records which devices have reported each hazard
// (docs/design.md sections 6 and 9). Its primary key is the rule "each device
// counts at most once per hazard", enforced by the database itself.
func TestHazardConfirmationsTableRules(test *testing.T) {
	test.Run("two different devices can confirm the same hazard", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		require.NoError(test, insertConfirmation(transaction, hazardID, hazardReporterDeviceID))
		require.NoError(test, insertConfirmation(transaction, hazardID, secondDeviceID))
	})

	test.Run("one device can confirm two different hazards", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		firstHazardID := insertTwoDevicesAndAHazard(test, transaction)
		secondHazardID := insertHazardReturningID(test, transaction)

		require.NoError(test, insertConfirmation(transaction, firstHazardID, secondDeviceID))
		require.NoError(test, insertConfirmation(transaction, secondHazardID, secondDeviceID))
	})

	test.Run("the same device cannot be counted twice for one hazard", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)
		require.NoError(test, insertConfirmation(transaction, hazardID, secondDeviceID))

		err := insertConfirmation(transaction, hazardID, secondDeviceID)

		requireRefusedBy(test, err, "hazard_confirmations_pkey")
	})

	test.Run("a confirmation must point to a hazard that exists", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		err := insertConfirmation(transaction, hazardID+1000, secondDeviceID)

		requireRefusedBy(test, err, "hazard_confirmations_hazard_id_fkey")
	})

	test.Run("a confirmation must point to a device that exists", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)

		err := insertConfirmation(transaction, hazardID, "jetson-00000000")

		requireRefusedBy(test, err, "hazard_confirmations_device_id_fkey")
	})

	test.Run("the time of the confirmation is filled in by the table", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := insertTwoDevicesAndAHazard(test, transaction)
		require.NoError(test, insertConfirmation(transaction, hazardID, secondDeviceID))

		var actualConfirmedAt time.Time
		err := transaction.QueryRow(
			`SELECT confirmed_at FROM hazard_confirmations WHERE hazard_id = $1 AND device_id = $2`,
			hazardID, secondDeviceID,
		).Scan(&actualConfirmedAt)

		require.NoError(test, err)
		assert.WithinDuration(test, time.Now(), actualConfirmedAt, time.Minute)
	})
}
