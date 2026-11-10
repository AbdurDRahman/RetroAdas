package hazards_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// TestProcessReport is the primary TDD loop for this project — this is the
// core business logic (deduplication-on-write) described in docs/design.md
// section 6. Run it now, watch it fail, then implement
// hazards.ProcessReport until every case here passes.
func TestProcessReport(test *testing.T) {
	// Skipped until task C-102 writes hazards.ProcessReport, so the checks that
	// run on every push stay meaningful. Removing this line is the first step
	// of C-102: the test then fails, and the task makes it pass.
	test.Skip("C-102: hazards.ProcessReport is not written yet")

	testCases := []struct {
		name                  string
		hazardToSeed          *seededHazard // nil = table starts empty
		incomingReport        hazards.Report
		expectedStatus        string
		expectedConfirmations int
	}{
		{
			name:         "no existing hazard nearby -> new row created",
			hazardToSeed: nil,
			incomingReport: hazards.Report{
				EventTag: "pothole", Latitude: 33.6850, Longitude: 73.0490, ReportedBy: "vehicle-0427",
			},
			expectedStatus: "created",
		},
		{
			name: "same tag about 6 meters away -> confirms existing hazard",
			hazardToSeed: &seededHazard{
				EventTag: "pothole", Latitude: 33.6850, Longitude: 73.0490, Confirmations: 3,
			},
			incomingReport: hazards.Report{
				EventTag: "pothole", Latitude: 33.68505, Longitude: 73.04905, ReportedBy: "vehicle-0311",
			},
			expectedStatus:        "confirmed",
			expectedConfirmations: 4,
		},
		{
			name: "same coordinates, different event_tag -> treated as a new hazard",
			hazardToSeed: &seededHazard{
				EventTag: "checkpost", Latitude: 33.6850, Longitude: 73.0490, Confirmations: 2,
			},
			incomingReport: hazards.Report{
				EventTag: "pothole", Latitude: 33.6850, Longitude: 73.0490, ReportedBy: "vehicle-0311",
			},
			expectedStatus: "created",
		},
		{
			name: "same tag but about 145 meters away (outside the 100 meter deduplication radius) -> new row",
			hazardToSeed: &seededHazard{
				EventTag: "pothole", Latitude: 33.6850, Longitude: 73.0490, Confirmations: 1,
			},
			incomingReport: hazards.Report{
				EventTag: "pothole", Latitude: 33.6863, Longitude: 73.0490, ReportedBy: "vehicle-0311",
			},
			expectedStatus: "created",
		},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)

			if testCase.hazardToSeed != nil {
				seedHazard(test, transaction, *testCase.hazardToSeed)
			}

			outcome, err := hazards.ProcessReport(transaction, testCase.incomingReport)
			require.NoError(test, err)
			require.Equal(test, testCase.expectedStatus, outcome.Status)

			if testCase.expectedStatus == "confirmed" {
				require.Equal(test, testCase.expectedConfirmations, outcome.Confirmations)
			}
		})
	}
}

// --- test fixtures ---

type seededHazard struct {
	EventTag            string
	Latitude, Longitude float64
	Confirmations       int
}

// seedHazard inserts a hazard row directly (bypassing ProcessReport) so each
// test case can set up its "existing state" independently of the code under
// test.
//
// Like seedDevice, it must use the same *sql.Tx that ProcessReport receives:
// the seeded hazard is uncommitted, so only that transaction can see it.
func seedHazard(test *testing.T, transaction *sql.Tx, hazard seededHazard) {
	test.Helper()
	// TODO(C-102): implement:
	//   INSERT INTO hazards (event_tag, location, reported_by, confirmations, expires_at)
	//   VALUES ($1, ST_MakePoint($2, $3)::geography, 'seed-fixture', $4, now() + interval '1 day')
	//   with $2 = Longitude, $3 = Latitude (PostGIS points are longitude first).
	test.Skip("seedHazard not implemented yet — fill in alongside hazards.ProcessReport")
}
