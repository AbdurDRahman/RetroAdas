package hazards_test

import (
	"testing"

	testdb "github.com/YOUR-ORG/adas-cloud/database/testing"
)

// TestFindNearby drives implementation of the projected lookahead search.
// Build this after TestProcessReport is green — it depends on the same
// seedHazard fixture.
func TestFindNearby(t *testing.T) {
	t.Run("hazard sitting at the projected lookahead point is returned", func(t *testing.T) {
		tx := testdb.NewTransaction(t)
		_ = tx

		// TODO: seed a hazard at the point ~200m ahead of a known
		// (lat, lng, heading_deg), then query FindNearby with that same
		// (lat, lng, heading_deg) and assert it comes back with a small
		// distance_m.
		t.Skip("depends on seedHazard + ProcessReport being implemented first")
	})

	t.Run("hazard well outside radius_m is not returned", func(t *testing.T) {
		tx := testdb.NewTransaction(t)
		_ = tx
		t.Skip("depends on seedHazard + ProcessReport being implemented first")
	})
}
