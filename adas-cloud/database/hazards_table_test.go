package database_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
)

// The device every test hazard is reported by. A hazard must name a real
// device, so each test inserts this one first.
const hazardReporterDeviceID = "jetson-4f9c2a1b"

// hazardRow holds the columns these tests vary. `any` lets a test pass nil,
// which the database stores as NULL (no value).
type hazardRow struct {
	Source         string
	Confidence     any
	HeadingDegrees any
	Confirmations  int
	ReportedBy     string
}

// validHazardRow is a row every rule accepts. Each test case starts from it and
// changes exactly one thing, so a rejection can only be caused by that change.
func validHazardRow() hazardRow {
	return hazardRow{
		Source:         "auto",
		Confidence:     0.9,
		HeadingDegrees: 140.0,
		Confirmations:  1,
		ReportedBy:     hazardReporterDeviceID,
	}
}

func insertHazardReporter(test *testing.T, transaction *sql.Tx) {
	test.Helper()
	_, err := transaction.Exec(
		`INSERT INTO devices (device_id, api_key_hash, device_type) VALUES ($1, 'not-a-real-hash', 'jetson_unit')`,
		hazardReporterDeviceID,
	)
	require.NoError(test, err)
}

func insertHazardRow(transaction *sql.Tx, row hazardRow) error {
	_, err := transaction.Exec(
		`INSERT INTO hazards (event_tag, location, heading_degrees, confidence, source, reported_by, confirmations, expires_at)
		 VALUES ('pothole', ST_MakePoint(73.0490, 33.6850)::geography, $1, $2, $3, $4, $5, now() + interval '1 day')`,
		row.HeadingDegrees, row.Confidence, row.Source, row.ReportedBy, row.Confirmations,
	)
	return err
}

// The hazards table enforces the agreed shape of a hazard by itself
// (docs/design.md sections 4 and 9), so no bug in the Go code, and no
// hand-written SQL, can store a row that breaks these rules.
func TestHazardsTableRules(test *testing.T) {
	testCases := []struct {
		name   string
		change func(row *hazardRow)
		// The rule that must refuse the row; empty means the row is accepted.
		expectedConstraint string
	}{
		{name: "a valid auto report is accepted", change: func(row *hazardRow) {}},

		// Source and confidence (section 4).
		{name: "auto with confidence 0 is accepted", change: func(row *hazardRow) { row.Confidence = 0.0 }},
		{name: "auto with confidence 1 is accepted", change: func(row *hazardRow) { row.Confidence = 1.0 }},
		{name: "manual with no confidence is accepted", change: func(row *hazardRow) { row.Source = "manual"; row.Confidence = nil }},
		{name: "auto with no confidence is rejected", change: func(row *hazardRow) { row.Confidence = nil }, expectedConstraint: "hazards_source_confidence_check"},
		{name: "auto with confidence above 1 is rejected", change: func(row *hazardRow) { row.Confidence = 1.5 }, expectedConstraint: "hazards_source_confidence_check"},
		{name: "auto with negative confidence is rejected", change: func(row *hazardRow) { row.Confidence = -0.1 }, expectedConstraint: "hazards_source_confidence_check"},
		{name: "manual with a confidence is rejected", change: func(row *hazardRow) { row.Source = "manual" }, expectedConstraint: "hazards_source_confidence_check"},
		{name: "an unknown source is rejected", change: func(row *hazardRow) { row.Source = "spaceship" }, expectedConstraint: "hazards_source_confidence_check"},

		// Heading: optional, from 0 up to (not including) 360 (section 8).
		{name: "no heading is accepted", change: func(row *hazardRow) { row.HeadingDegrees = nil }},
		{name: "heading 0 is accepted", change: func(row *hazardRow) { row.HeadingDegrees = 0.0 }},
		{name: "heading 359.9 is accepted", change: func(row *hazardRow) { row.HeadingDegrees = 359.9 }},
		{name: "heading 360 is rejected", change: func(row *hazardRow) { row.HeadingDegrees = 360.0 }, expectedConstraint: "hazards_heading_degrees_check"},
		{name: "a negative heading is rejected", change: func(row *hazardRow) { row.HeadingDegrees = -1.0 }, expectedConstraint: "hazards_heading_degrees_check"},

		// Confirmations count the first reporter, so they can never be below 1 (section 6).
		{name: "0 confirmations is rejected", change: func(row *hazardRow) { row.Confirmations = 0 }, expectedConstraint: "hazards_confirmations_check"},

		// A hazard must be reported by a device that exists (section 9).
		{name: "an unknown reporter is rejected", change: func(row *hazardRow) { row.ReportedBy = "jetson-00000000" }, expectedConstraint: "hazards_reported_by_fkey"},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			insertHazardReporter(test, transaction)

			row := validHazardRow()
			testCase.change(&row)
			err := insertHazardRow(transaction, row)

			if testCase.expectedConstraint == "" {
				require.NoError(test, err)
				return
			}

			var databaseError *pq.Error
			require.True(test, errors.As(err, &databaseError), "expected a database error, got: %v", err)
			assert.Equal(test, testCase.expectedConstraint, databaseError.Constraint)
		})
	}
}

func TestHazardsTableStartsConfirmationsAtOne(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	insertHazardReporter(test, transaction)

	// No confirmations column in this insert: the table's own default applies.
	var actualConfirmations int
	err := transaction.QueryRow(
		`INSERT INTO hazards (event_tag, location, confidence, source, reported_by, expires_at)
		 VALUES ('pothole', ST_MakePoint(73.0490, 33.6850)::geography, 0.9, 'auto', $1, now() + interval '1 day')
		 RETURNING confirmations`,
		hazardReporterDeviceID,
	).Scan(&actualConfirmations)

	require.NoError(test, err)
	assert.Equal(test, 1, actualConfirmations)
}

func TestHazardsTableGeneratesItsOwnIDs(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	insertHazardReporter(test, transaction)

	// An insert that sets id by hand could later collide with the numbers
	// Postgres hands out, so the table must refuse it.
	_, err := transaction.Exec(
		`INSERT INTO hazards (id, event_tag, location, confidence, source, reported_by, expires_at)
		 VALUES (12345, 'pothole', ST_MakePoint(73.0490, 33.6850)::geography, 0.9, 'auto', $1, now() + interval '1 day')`,
		hazardReporterDeviceID,
	)

	var databaseError *pq.Error
	require.True(test, errors.As(err, &databaseError), "expected a database error, got: %v", err)
	// 428C9 is Postgres's code for "this column is generated; you can't set it".
	assert.Equal(test, pq.ErrorCode("428C9"), databaseError.Code)
}
