package hazards_test

import (
	"testing"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
)

// TestFindNearby drives implementation of the projected lookahead search.
// Build this after TestProcessReport is green — it depends on the same
// seedHazard fixture. C-103 replaces these placeholders with the full case
// list (hazard behind the vehicle, expired hazard, ordering, minimum
// lookahead distance at slow speed).
func TestFindNearby(test *testing.T) {
	test.Run("hazard sitting at the projected lookahead point is returned", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		_ = transaction

		// TODO(C-103): seed a hazard at the point LookaheadDistanceMeters
		// ahead of a known (latitude, longitude, heading_degrees), then query
		// FindNearby with that same (latitude, longitude, heading_degrees)
		// and assert it comes back with a small distance_meters.
		test.Skip("depends on seedHazard + ProcessReport being implemented first")
	})

	test.Run("hazard well outside radius_meters is not returned", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		_ = transaction
		test.Skip("depends on seedHazard + ProcessReport being implemented first")
	})
}
