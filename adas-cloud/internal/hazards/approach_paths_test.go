package hazards_test

import (
	"database/sql"
	"math"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// --- test fixtures ---

// trailToReport builds the trail of a vehicle that drove in a straight line to
// the report's position, arriving from the given compass direction. Each
// number is how far before the report a point lies, oldest first:
// (500, 250, 25) is three points 500, 250 and 25 meters before it. PostGIS
// computes the positions.
func trailToReport(test *testing.T, transaction *sql.Tx, report hazards.Report, arrivingFromDegrees float64, distancesBeforeReportMeters ...float64) []hazards.Position {
	test.Helper()
	var trail []hazards.Position
	for _, distanceMeters := range distancesBeforeReportMeters {
		var position hazards.Position
		err := transaction.QueryRow(
			`SELECT ST_Y(position::geometry), ST_X(position::geometry)
			 FROM (SELECT ST_Project(ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3::double precision, radians($4::double precision)) AS position) AS projected`,
			report.Longitude, report.Latitude, distanceMeters, arrivingFromDegrees,
		).Scan(&position.Latitude, &position.Longitude)
		require.NoError(test, err)
		trail = append(trail, position)
	}
	return trail
}

// trailBehindReport is the common case: the trail of the vehicle in
// validJetsonReport. It drives at 140 degrees, so it arrives from the opposite
// direction, 320.
func trailBehindReport(test *testing.T, transaction *sql.Tx, distancesBeforeReportMeters ...float64) []hazards.Position {
	test.Helper()
	return trailToReport(test, transaction, validJetsonReport(), 320, distancesBeforeReportMeters...)
}

// readApproachPaths returns the paths stored for a hazard, oldest first, each
// as its list of points.
func readApproachPaths(test *testing.T, transaction *sql.Tx, hazardID int64) [][]hazards.Position {
	test.Helper()
	// ST_DumpPoints turns a line back into its points; point.path[1] is the
	// point's number inside its line.
	rows, err := transaction.Query(
		`SELECT paths.id, ST_Y(point.geom), ST_X(point.geom)
		 FROM hazard_approach_paths AS paths, ST_DumpPoints(paths.path::geometry) AS point
		 WHERE paths.hazard_id = $1
		 ORDER BY paths.id, point.path[1]`,
		hazardID,
	)
	require.NoError(test, err)
	defer rows.Close()

	var approachPaths [][]hazards.Position
	var previousPathID int64
	for rows.Next() {
		var pathID int64
		var position hazards.Position
		require.NoError(test, rows.Scan(&pathID, &position.Latitude, &position.Longitude))
		if pathID != previousPathID {
			approachPaths = append(approachPaths, nil)
			previousPathID = pathID
		}
		lastPath := len(approachPaths) - 1
		approachPaths[lastPath] = append(approachPaths[lastPath], position)
	}
	require.NoError(test, rows.Err())
	return approachPaths
}

// requireSamePosition compares two positions with a tolerance of about one
// centimeter, because a coordinate loses its last digits on the way through
// the database.
func requireSamePosition(test *testing.T, expectedPosition hazards.Position, actualPosition hazards.Position) {
	test.Helper()
	require.InDelta(test, expectedPosition.Latitude, actualPosition.Latitude, 0.0000001)
	require.InDelta(test, expectedPosition.Longitude, actualPosition.Longitude, 0.0000001)
}

// evenlySpaced lists distances from farthest down to nearest, one every
// spacing: evenlySpaced(500, 25, 25) is 500, 475, ... 50, 25.
func evenlySpaced(farthestMeters float64, nearestMeters float64, spacingMeters float64) []float64 {
	var distancesMeters []float64
	for distanceMeters := farthestMeters; distanceMeters >= nearestMeters; distanceMeters -= spacingMeters {
		distancesMeters = append(distancesMeters, distanceMeters)
	}
	return distancesMeters
}

// --- tests ---

// A trail must stay inside the limits of docs/design.md section 5. Each
// accepted case sits just inside a limit and each refused case just outside
// it, so a limit that is off by a little shows up.
func TestProcessReportChecksTheApproachPath(test *testing.T) {
	testCases := []struct {
		name                        string
		distancesBeforeReportMeters []float64
		// change alters the finished trail, for cases that distances can't describe.
		change          func(trail []hazards.Position)
		expectedRefused bool
	}{
		{name: "a normal trail: 500 meters, a point every 25", distancesBeforeReportMeters: evenlySpaced(500, 25, 25)},

		// The number of points: at least 2, at most 80.
		{name: "one point", distancesBeforeReportMeters: []float64{100}, expectedRefused: true},
		{name: "two points", distancesBeforeReportMeters: []float64{100, 25}},
		{name: "80 points", distancesBeforeReportMeters: evenlySpaced(400, 5, 5)},
		{name: "81 points", distancesBeforeReportMeters: evenlySpaced(405, 5, 5), expectedRefused: true},

		// Coordinates that are not on Earth.
		{name: "a latitude of 91", distancesBeforeReportMeters: evenlySpaced(500, 25, 25), change: func(trail []hazards.Position) { trail[3].Latitude = 91 }, expectedRefused: true},
		{name: "a longitude of -181", distancesBeforeReportMeters: evenlySpaced(500, 25, 25), change: func(trail []hazards.Position) { trail[3].Longitude = -181 }, expectedRefused: true},
		{name: "a latitude that is not a number", distancesBeforeReportMeters: evenlySpaced(500, 25, 25), change: func(trail []hazards.Position) { trail[3].Latitude = math.NaN() }, expectedRefused: true},

		// The trail must end at the report: at most 50 meters before it.
		{name: "ends 45 meters before the report", distancesBeforeReportMeters: evenlySpaced(500, 45, 35)},
		{name: "ends 55 meters before the report", distancesBeforeReportMeters: evenlySpaced(510, 55, 35), expectedRefused: true},

		// No jumps: neighbouring points at most 200 meters apart.
		{name: "a gap of 195 meters", distancesBeforeReportMeters: []float64{420, 225, 30}},
		{name: "a gap of 205 meters", distancesBeforeReportMeters: []float64{440, 235, 30}, expectedRefused: true},

		// The whole path, to the report: at least 50 and at most 1,200 meters.
		{name: "45 meters long", distancesBeforeReportMeters: []float64{45, 20}, expectedRefused: true},
		{name: "55 meters long", distancesBeforeReportMeters: []float64{55, 20}},
		{name: "1,190 meters long", distancesBeforeReportMeters: evenlySpaced(1190, 40, 50)},
		{name: "1,210 meters long", distancesBeforeReportMeters: evenlySpaced(1210, 10, 50), expectedRefused: true},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
			hazardCountBefore := countHazards(test, transaction)

			report := validJetsonReport()
			report.ApproachPath = trailBehindReport(test, transaction, testCase.distancesBeforeReportMeters...)
			if testCase.change != nil {
				testCase.change(report.ApproachPath)
			}

			outcome, err := hazards.ProcessReport(transaction, report)

			if !testCase.expectedRefused {
				require.NoError(test, err)
				assert.Equal(test, hazards.StatusCreated, outcome.Status)
				return
			}
			// The whole report is refused, not only its trail (design
			// section 5), so no hazard may have been stored.
			require.ErrorIs(test, err, hazards.ErrInvalidApproachPath)
			assert.Equal(test, hazardCountBefore, countHazards(test, transaction))
		})
	}
}

// A valid trail is stored as an approach path of the hazard: the trail's
// points in order, then the reported position as the final point, so a path
// always ends at the report (docs/design.md section 5).
func TestProcessReportStoresTheApproachPathOfANewHazard(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	report := validJetsonReport()
	report.ApproachPath = trailBehindReport(test, transaction, 300, 200, 100, 25)

	outcome, err := hazards.ProcessReport(transaction, report)

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusCreated, outcome.Status)
	assert.True(test, outcome.ApproachPathStored)

	approachPaths := readApproachPaths(test, transaction, outcome.ID)
	require.Equal(test, 1, len(approachPaths), "number of paths stored")
	storedPath := approachPaths[0]
	require.Equal(test, 5, len(storedPath), "the 4 trail points plus the reported position")
	for pointNumber, expectedPosition := range report.ApproachPath {
		requireSamePosition(test, expectedPosition, storedPath[pointNumber])
	}
	requireSamePosition(test, hazards.Position{Latitude: report.Latitude, Longitude: report.Longitude}, storedPath[4])
}

// A confirmation teaches the server one more way of reaching a hazard it
// already knows, so its trail is stored with that hazard.
func TestProcessReportStoresTheApproachPathOfAConfirmation(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	hazardID := seedJetsonPothole(test, transaction)
	report := phonePotholeReportNearby()
	// The phone arrives from the same direction as the Jetson did (it drives
	// at 140 degrees, so it comes from 320).
	report.ApproachPath = trailToReport(test, transaction, report, 320, 300, 200, 100, 25)

	outcome, err := hazards.ProcessReport(transaction, report)

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusConfirmed, outcome.Status)
	assert.Equal(test, hazardID, outcome.ID)
	assert.True(test, outcome.ApproachPathStored)

	approachPaths := readApproachPaths(test, transaction, hazardID)
	require.Equal(test, 1, len(approachPaths), "number of paths stored")
	// The path ends where the phone reported from, not at the hazard's own
	// stored position: the phone's trail is what was really driven.
	lastPoint := approachPaths[0][len(approachPaths[0])-1]
	requireSamePosition(test, hazards.Position{Latitude: report.Latitude, Longitude: report.Longitude}, lastPoint)
}

// A repeat changes nothing about the hazard, but the same device arriving
// again can still show a way of reaching it (design section 5).
func TestProcessReportStoresTheApproachPathOfARepeat(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	hazardID := seedJetsonPothole(test, transaction)
	hazardBefore := readHazard(test, transaction, hazardID)
	// The Jetson that first reported this pothole (without a trail) reports
	// it again, this time with one.
	report := validJetsonReport()
	report.ApproachPath = trailBehindReport(test, transaction, 300, 200, 100, 25)

	outcome, err := hazards.ProcessReport(transaction, report)

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusAlreadyReported, outcome.Status)
	assert.True(test, outcome.ApproachPathStored)
	assert.Equal(test, 1, len(readApproachPaths(test, transaction, hazardID)), "number of paths stored")

	// Still a repeat: no extra confirmation and no new expiry.
	hazardAfter := readHazard(test, transaction, hazardID)
	assert.Equal(test, hazardBefore.Confirmations, hazardAfter.Confirmations)
	assert.Equal(test, hazardBefore.ExpiresAt, hazardAfter.ExpiresAt)
}

func TestProcessReportWithoutATrailStoresNoApproachPath(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)

	outcome, err := hazards.ProcessReport(transaction, validJetsonReport())

	require.NoError(test, err)
	assert.False(test, outcome.ApproachPathStored)
	assert.Empty(test, readApproachPaths(test, transaction, outcome.ID))
}

// --- fixtures for the limits on stored paths ---

// positionFrom returns the position a distance away from another one, in a
// compass direction.
func positionFrom(test *testing.T, transaction *sql.Tx, origin hazards.Position, distanceMeters float64, directionDegrees float64) hazards.Position {
	test.Helper()
	var position hazards.Position
	err := transaction.QueryRow(
		`SELECT ST_Y(position::geometry), ST_X(position::geometry)
		 FROM (SELECT ST_Project(ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3::double precision, radians($4::double precision)) AS position) AS projected`,
		origin.Longitude, origin.Latitude, distanceMeters, directionDegrees,
	).Scan(&position.Latitude, &position.Longitude)
	require.NoError(test, err)
	return position
}

// straightRouteTo is a 300 meter trail arriving at the report in a straight
// line from the given compass direction. Two of these from clearly different
// directions are two different routes.
func straightRouteTo(test *testing.T, transaction *sql.Tx, report hazards.Report, arrivingFromDegrees float64) []hazards.Position {
	test.Helper()
	return trailToReport(test, transaction, report, arrivingFromDegrees, 300, 200, 100, 25)
}

// repeatWithRoute is the Jetson reporting its own pothole again, arriving from
// the given direction. It is always a repeat, so the only thing it can change
// is the hazard's paths.
func repeatWithRoute(test *testing.T, transaction *sql.Tx, arrivingFromDegrees float64) hazards.Outcome {
	test.Helper()
	report := validJetsonReport()
	report.ApproachPath = straightRouteTo(test, transaction, report, arrivingFromDegrees)
	outcome, err := hazards.ProcessReport(transaction, report)
	require.NoError(test, err)
	require.Equal(test, hazards.StatusAlreadyReported, outcome.Status)
	return outcome
}

// seedApproachPath stores a path directly, without going through
// ProcessReport and without counting it for any device.
func seedApproachPath(test *testing.T, transaction *sql.Tx, hazardID int64, trail []hazards.Position) {
	test.Helper()
	var longitudes, latitudes []float64
	for _, position := range trail {
		longitudes = append(longitudes, position.Longitude)
		latitudes = append(latitudes, position.Latitude)
	}
	_, err := transaction.Exec(
		`INSERT INTO hazard_approach_paths (hazard_id, path)
		 SELECT $1, ST_MakeLine(ST_SetSRID(ST_MakePoint(longitude, latitude), 4326) ORDER BY point_number)::geography
		 FROM unnest($2::double precision[], $3::double precision[]) WITH ORDINALITY AS trail (longitude, latitude, point_number)`,
		hazardID, pq.Array(longitudes), pq.Array(latitudes),
	)
	require.NoError(test, err)
}

// readPathsAdded returns how many paths the count table says a device has
// added to a hazard; 0 when it has no row.
func readPathsAdded(test *testing.T, transaction *sql.Tx, hazardID int64, deviceID string) int {
	test.Helper()
	var pathsAdded int
	err := transaction.QueryRow(
		`SELECT coalesce((SELECT paths_added FROM hazard_approach_path_counts WHERE hazard_id = $1 AND device_id = $2), 0)`,
		hazardID, deviceID,
	).Scan(&pathsAdded)
	require.NoError(test, err)
	return pathsAdded
}

// --- tests for the limits on stored paths ---

// A trail on a route the hazard already has stores nothing (docs/design.md
// section 5). Without this, the traffic on a main road would fill the hazard's
// places and push out the one trail from a side road.
func TestProcessReportSkipsARouteTheHazardAlreadyHas(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	hazardID := seedJetsonPothole(test, transaction)
	repeatWithRoute(test, transaction, 320)

	// The phone drives the same road. Its points are not the Jetson's points:
	// it reports from about 7 meters away, and its trail is spaced differently.
	// The two lines still lie on top of each other.
	report := phonePotholeReportNearby()
	report.ApproachPath = trailToReport(test, transaction, report, 320, 280, 190, 110, 40)

	outcome, err := hazards.ProcessReport(transaction, report)

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusConfirmed, outcome.Status)
	assert.False(test, outcome.ApproachPathStored)
	assert.Equal(test, 1, len(readApproachPaths(test, transaction, hazardID)), "number of paths stored")
	// A skipped trail took no place, so it does not count against the device.
	assert.Equal(test, 0, readPathsAdded(test, transaction, hazardID, phoneDeviceID))
}

func TestProcessReportStoresADifferentRoute(test *testing.T) {
	test.Run("arriving from another direction", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := seedJetsonPothole(test, transaction)
		repeatWithRoute(test, transaction, 320)

		report := phonePotholeReportNearby()
		report.ApproachPath = straightRouteTo(test, transaction, report, 230)

		outcome, err := hazards.ProcessReport(transaction, report)

		require.NoError(test, err)
		assert.True(test, outcome.ApproachPathStored)
		assert.Equal(test, 2, len(readApproachPaths(test, transaction, hazardID)), "number of paths stored")
		assert.Equal(test, 1, readPathsAdded(test, transaction, hazardID, phoneDeviceID))
	})

	// The case the rule exists for: a car comes down a side road and joins the
	// main road 100 meters before the hazard. Its last 100 meters lie on the
	// stored path, but its first 200 do not, so it is a different route.
	test.Run("joining the same road from a side road", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		hazardID := seedJetsonPothole(test, transaction)
		repeatWithRoute(test, transaction, 320)

		report := phonePotholeReportNearby()
		reportPosition := hazards.Position{Latitude: report.Latitude, Longitude: report.Longitude}
		junction := positionFrom(test, transaction, reportPosition, 100, 320)
		report.ApproachPath = []hazards.Position{
			positionFrom(test, transaction, junction, 200, 230),
			positionFrom(test, transaction, junction, 100, 230),
			junction,
			positionFrom(test, transaction, reportPosition, 25, 320),
		}

		outcome, err := hazards.ProcessReport(transaction, report)

		require.NoError(test, err)
		assert.True(test, outcome.ApproachPathStored)
		assert.Equal(test, 2, len(readApproachPaths(test, transaction, hazardID)), "number of paths stored")
	})
}

// A hazard keeps at most 8 paths. When a new route arrives at a full hazard,
// the oldest path makes room for it.
func TestProcessReportDropsTheOldestPathWhenAHazardIsFull(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	hazardID := seedJetsonPothole(test, transaction)
	report := validJetsonReport()
	// Eight routes, arriving from 0, 40, 80 ... 280 degrees. The one from 0
	// degrees is stored first, so it is the oldest.
	for arrivingFromDegrees := 0.0; arrivingFromDegrees <= 280; arrivingFromDegrees += 40 {
		seedApproachPath(test, transaction, hazardID, straightRouteTo(test, transaction, report, arrivingFromDegrees))
	}
	secondOldestRoute := straightRouteTo(test, transaction, report, 40)

	outcome := repeatWithRoute(test, transaction, 320)

	assert.True(test, outcome.ApproachPathStored)
	approachPaths := readApproachPaths(test, transaction, hazardID)
	require.Equal(test, 8, len(approachPaths), "number of paths stored")
	// The route from 0 degrees is gone, so the oldest one left is the route
	// from 40 degrees, and the newest is the one just reported.
	requireSamePosition(test, secondOldestRoute[0], approachPaths[0][0])
	newestRoute := straightRouteTo(test, transaction, report, 320)
	requireSamePosition(test, newestRoute[0], approachPaths[7][0])
}

// One device adds at most 2 paths to one hazard, so a single device can't fill
// all 8 places and honest vehicles keep theirs (design section 5).
func TestProcessReportLetsOneDeviceAddTwoPathsToAHazard(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	hazardID := seedJetsonPothole(test, transaction)

	firstOutcome := repeatWithRoute(test, transaction, 320)
	// The same route again: skipped, and it must not use up the second place.
	sameRouteOutcome := repeatWithRoute(test, transaction, 320)
	secondOutcome := repeatWithRoute(test, transaction, 230)
	thirdOutcome := repeatWithRoute(test, transaction, 50)

	assert.True(test, firstOutcome.ApproachPathStored)
	assert.False(test, sameRouteOutcome.ApproachPathStored)
	assert.True(test, secondOutcome.ApproachPathStored)
	assert.False(test, thirdOutcome.ApproachPathStored, "a third route from the same device")
	assert.Equal(test, 2, len(readApproachPaths(test, transaction, hazardID)), "number of paths stored")
	assert.Equal(test, 2, readPathsAdded(test, transaction, hazardID, jetsonDeviceID))

	// Another device still has its own two places.
	report := phonePotholeReportNearby()
	report.ApproachPath = straightRouteTo(test, transaction, report, 50)
	phoneOutcome, err := hazards.ProcessReport(transaction, report)
	require.NoError(test, err)
	assert.True(test, phoneOutcome.ApproachPathStored)
	assert.Equal(test, 3, len(readApproachPaths(test, transaction, hazardID)), "number of paths stored")
}
