package hazards_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/YOUR-ORG/adas-cloud/internal/hazards"
	testdb "github.com/YOUR-ORG/adas-cloud/database/testing"
)

// TestProcessReport is the primary TDD loop for this project — this is the
// core business logic (dedup-on-write) described in database_schema.md
// Section 4. Run it now, watch it fail, then implement
// hazards.ProcessReport until every case here passes.
func TestProcessReport(t *testing.T) {
	tests := []struct {
		name             string
		existingHazard   *existingHazard // nil = table starts empty
		incoming         hazards.Report
		wantStatus       string
		wantConfirmCount int
	}{
		{
			name:           "no existing hazard nearby -> new row created",
			existingHazard: nil,
			incoming: hazards.Report{
				EventTag: "pothole", Lat: 33.6850, Lng: 73.0490, ReportedBy: "veh-0427",
			},
			wantStatus: "created",
		},
		{
			name: "same tag ~6m away -> confirms existing hazard",
			existingHazard: &existingHazard{
				EventTag: "pothole", Lat: 33.6850, Lng: 73.0490, Confirmations: 3,
			},
			incoming: hazards.Report{
				EventTag: "pothole", Lat: 33.68505, Lng: 73.04905, ReportedBy: "veh-0311",
			},
			wantStatus:       "confirmed",
			wantConfirmCount: 4,
		},
		{
			name: "same coordinates, different event_tag -> treated as a new hazard",
			existingHazard: &existingHazard{
				EventTag: "checkpost", Lat: 33.6850, Lng: 73.0490, Confirmations: 2,
			},
			incoming: hazards.Report{
				EventTag: "pothole", Lat: 33.6850, Lng: 73.0490, ReportedBy: "veh-0311",
			},
			wantStatus: "created",
		},
		{
			name: "same tag but ~145m away (outside 100m dedup radius) -> new row",
			existingHazard: &existingHazard{
				EventTag: "pothole", Lat: 33.6850, Lng: 73.0490, Confirmations: 1,
			},
			incoming: hazards.Report{
				EventTag: "pothole", Lat: 33.6863, Lng: 73.0490, ReportedBy: "veh-0311",
			},
			wantStatus: "created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := testdb.NewTransaction(t)

			if tt.existingHazard != nil {
				seedHazard(t, tx, *tt.existingHazard)
			}

			outcome, err := hazards.ProcessReport(tx, tt.incoming)
			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, outcome.Status)

			if tt.wantStatus == "confirmed" {
				require.Equal(t, tt.wantConfirmCount, outcome.Confirmations)
			}
		})
	}
}

// --- test fixtures ---

type existingHazard struct {
	EventTag      string
	Lat, Lng      float64
	Confirmations int
}

// seedHazard inserts a hazard row directly (bypassing ProcessReport) so each
// test case can set up its "existing state" independently of the code under
// test.
func seedHazard(t *testing.T, tx interface {
	Exec(query string, args ...any) (any, error)
}, h existingHazard) {
	t.Helper()
	// TODO: implement:
	//   INSERT INTO hazards (event_tag, location, reported_by, confirmations, expires_at)
	//   VALUES ($1, ST_MakePoint($2, $3)::geography, 'seed-fixture', $4, now() + interval '1 day')
	t.Skip("seedHazard not implemented yet — fill in alongside hazards.ProcessReport")
}
