package hazards_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// --- test fixtures ---

// The vehicle in every search test stands at this position and drives
// south-east. Hazards are placed relative to it, so a test reads as
// "100 meters ahead" instead of as a pair of coordinates.
const (
	vehicleLatitude       = 33.6850
	vehicleLongitude      = 73.0490
	vehicleHeadingDegrees = 140.0
	// Behind the vehicle is the opposite compass direction: 140 + 180.
	behindVehicleDegrees = 320.0
)

// searchTestPollingRules are the agreed starting values (docs/design.md
// section 5). At 60 kilometers per hour they give a circle centered 200 meters
// ahead with a radius of 200 meters: from the vehicle to 400 meters ahead.
var searchTestPollingRules = hazards.PollingRules{
	TimeBetweenPolls:               12 * time.Second,
	MinimumLookaheadDistanceMeters: 150,
	MaximumSearchRadiusMeters:      1000,
	FallbackTime:                   60 * time.Second,
}

// vehicleSearchQuery is what the vehicle sends when it drives at this speed.
func vehicleSearchQuery(speedKilometersPerHour float64) hazards.SearchQuery {
	return hazards.SearchQuery{
		Latitude:               vehicleLatitude,
		Longitude:              vehicleLongitude,
		HeadingDegrees:         vehicleHeadingDegrees,
		SpeedKilometersPerHour: speedKilometersPerHour,
	}
}

// seedHazardFromVehicle places a live hazard an exact distance from the
// vehicle in a compass direction, and returns its id. PostGIS computes the
// position (ST_Project), the same function the search itself uses, so the
// test never depends on hand-calculated coordinates. hazardHeadingDegrees is
// the direction its reporter was driving; nil means the report had none.
func seedHazardFromVehicle(test *testing.T, transaction *sql.Tx, eventTag string, distanceMeters float64, directionDegrees float64, hazardHeadingDegrees *float64) int64 {
	test.Helper()
	var latitude, longitude float64
	err := transaction.QueryRow(
		`SELECT ST_Y(position::geometry), ST_X(position::geometry)
		 FROM (SELECT ST_Project(ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3::double precision, radians($4::double precision)) AS position) AS projected`,
		vehicleLongitude, vehicleLatitude, distanceMeters, directionDegrees,
	).Scan(&latitude, &longitude)
	require.NoError(test, err)

	return seedHazard(test, transaction, seededHazard{
		EventTag:       eventTag,
		Latitude:       latitude,
		Longitude:      longitude,
		HeadingDegrees: hazardHeadingDegrees,
		ReportedBy:     jetsonDeviceID,
		ExpiresIn:      time.Hour,
	})
}

// seedPotholeFromVehicle is the common case: a pothole recorded by a vehicle
// driving the same way as the one searching.
func seedPotholeFromVehicle(test *testing.T, transaction *sql.Tx, distanceMeters float64, directionDegrees float64) int64 {
	test.Helper()
	return seedHazardFromVehicle(test, transaction, "pothole", distanceMeters, directionDegrees, pointerTo(vehicleHeadingDegrees))
}

// --- tests ---

// The heart of the search: it looks ahead of the vehicle, not around it. A
// hazard the vehicle has already passed is of no use to the driver.
func TestFindNearbyReturnsTheHazardAheadAndNotTheOneBehind(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	hazardAheadID := seedPotholeFromVehicle(test, transaction, 100, vehicleHeadingDegrees)
	seedPotholeFromVehicle(test, transaction, 50, behindVehicleDegrees)

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	require.Len(test, searchResult.Hazards, 1)
	assert.Equal(test, hazardAheadID, searchResult.Hazards[0].ID)
}

// The circle reaches from the vehicle to twice the lookahead distance ahead
// (docs/design.md section 5). These cases sit 10 meters either side of that
// far edge, so a wrong radius or a wrong center shows up immediately.
func TestFindNearbyReachesTwiceTheLookaheadDistance(test *testing.T) {
	testCases := []struct {
		name                   string
		speedKilometersPerHour float64
		hazardDistanceMeters   float64
		expectedReturned       bool
	}{
		// 60 kilometers per hour: lookahead distance 200 meters, far edge at 400.
		{name: "60 kilometers per hour, 390 meters ahead", speedKilometersPerHour: 60, hazardDistanceMeters: 390, expectedReturned: true},
		{name: "60 kilometers per hour, 410 meters ahead", speedKilometersPerHour: 60, hazardDistanceMeters: 410, expectedReturned: false},

		// 120 kilometers per hour: lookahead distance 400 meters, far edge at 800.
		{name: "120 kilometers per hour, 790 meters ahead", speedKilometersPerHour: 120, hazardDistanceMeters: 790, expectedReturned: true},
		{name: "120 kilometers per hour, 810 meters ahead", speedKilometersPerHour: 120, hazardDistanceMeters: 810, expectedReturned: false},

		// 30 kilometers per hour: the minimum of 150 meters applies, far edge at 300.
		{name: "30 kilometers per hour, 290 meters ahead", speedKilometersPerHour: 30, hazardDistanceMeters: 290, expectedReturned: true},
		{name: "30 kilometers per hour, 310 meters ahead", speedKilometersPerHour: 30, hazardDistanceMeters: 310, expectedReturned: false},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
			seedPotholeFromVehicle(test, transaction, testCase.hazardDistanceMeters, vehicleHeadingDegrees)

			searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(testCase.speedKilometersPerHour))

			require.NoError(test, err)
			if testCase.expectedReturned {
				assert.Len(test, searchResult.Hazards, 1)
			} else {
				assert.Empty(test, searchResult.Hazards)
			}
		})
	}
}

// The driver needs the closest hazard first, and each hazard's distance from
// the vehicle itself (docs/design.md section 8), so a device can say
// "pothole in 50 meters".
func TestFindNearbyReturnsTheNearestHazardFirstWithItsDistance(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	// Inserted out of order on purpose: a database without an ORDER BY often
	// returns rows in the order they were inserted, which would hide a
	// missing sort.
	hazardAt300MetersID := seedPotholeFromVehicle(test, transaction, 300, vehicleHeadingDegrees)
	hazardAt50MetersID := seedPotholeFromVehicle(test, transaction, 50, vehicleHeadingDegrees)
	hazardAt180MetersID := seedPotholeFromVehicle(test, transaction, 180, vehicleHeadingDegrees)

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	require.Len(test, searchResult.Hazards, 3)

	expectedHazardIDs := []int64{hazardAt50MetersID, hazardAt180MetersID, hazardAt300MetersID}
	expectedDistancesMeters := []float64{50, 180, 300}
	for position, hazard := range searchResult.Hazards {
		assert.Equal(test, expectedHazardIDs[position], hazard.ID)
		// Half a meter of tolerance: the position was stored as coordinates
		// and measured back, which loses a little precision.
		assert.InDelta(test, expectedDistancesMeters[position], hazard.DistanceMeters, 0.5)
	}
}

// A search returns at most 100 hazards (docs/design.md section 8), so one
// crowded stretch of road can't produce an enormous response. When there are
// more, the nearest ones are the ones kept.
func TestFindNearbyReturnsAtMostOneHundredHazards(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	// 105 potholes, one every meter from 105 meters ahead down to 1 meter
	// ahead. Farthest first, so the five that must be dropped are the first
	// ones inserted and not simply the last.
	for distanceMeters := 105; distanceMeters >= 1; distanceMeters-- {
		seedPotholeFromVehicle(test, transaction, float64(distanceMeters), vehicleHeadingDegrees)
	}

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	// The count is compared as a number: require.Len would print every hazard
	// in the list when it fails.
	require.Equal(test, 100, len(searchResult.Hazards), "number of hazards returned")
	// The farthest hazard kept is the one 100 meters ahead: the five beyond
	// it were dropped, not five nearer ones.
	farthestHazardReturned := searchResult.Hazards[len(searchResult.Hazards)-1]
	assert.InDelta(test, 100, farthestHazardReturned.DistanceMeters, 0.5)
}

// A hazard past its expiry is gone for drivers, even though its row is still
// in the table (docs/design.md section 6: nothing deletes rows on a timer).
func TestFindNearbyLeavesOutExpiredHazards(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	liveHazardID := seedPotholeFromVehicle(test, transaction, 100, vehicleHeadingDegrees)
	expiredHazardID := seedPotholeFromVehicle(test, transaction, 120, vehicleHeadingDegrees)
	_, err := transaction.Exec(`UPDATE hazards SET expires_at = now() - interval '1 second' WHERE id = $1`, expiredHazardID)
	require.NoError(test, err)

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	require.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
	assert.Equal(test, liveHazardID, searchResult.Hazards[0].ID)
}

// Every answer tells the vehicle when to ask again (docs/design.md section 8),
// also when there are no hazards at all: that is the normal case.
func TestFindNearbyReturnsTheNextPollValues(test *testing.T) {
	testCases := []struct {
		name                           string
		speedKilometersPerHour         float64
		expectedNextPollDistanceMeters float64
	}{
		{name: "60 kilometers per hour", speedKilometersPerHour: 60, expectedNextPollDistanceMeters: 200},
		{name: "30 kilometers per hour gets the minimum", speedKilometersPerHour: 30, expectedNextPollDistanceMeters: 150},
		{name: "stopped gets the minimum", speedKilometersPerHour: 0, expectedNextPollDistanceMeters: 150},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)

			searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(testCase.speedKilometersPerHour))

			require.NoError(test, err)
			assert.Empty(test, searchResult.Hazards)
			assert.InDelta(test, testCase.expectedNextPollDistanceMeters, searchResult.NextPollDistanceMeters, 0.01)
			assert.Equal(test, 60*time.Second, searchResult.FallbackTime)
		})
	}
}

// The direction rule (docs/design.md section 6). The vehicle drives at 140
// degrees. Every hazard here is 100 meters ahead, well inside the circle, so
// only its tag and its heading decide whether it is returned.
func TestFindNearbyLeavesOutHazardsOfTheOppositeDirection(test *testing.T) {
	testCases := []struct {
		name                 string
		eventTag             string
		hazardHeadingDegrees *float64
		expectedReturned     bool
	}{
		{name: "pothole recorded driving the same way", eventTag: "pothole", hazardHeadingDegrees: pointerTo(140), expectedReturned: true},
		// A compass in a phone can be off by 20 to 30 degrees.
		{name: "pothole 30 degrees off", eventTag: "pothole", hazardHeadingDegrees: pointerTo(170), expectedReturned: true},
		{name: "pothole recorded by oncoming traffic", eventTag: "pothole", hazardHeadingDegrees: pointerTo(320), expectedReturned: false},

		// The threshold is 135 degrees: 140 + 135 = 275.
		{name: "pothole exactly 135 degrees off", eventTag: "pothole", hazardHeadingDegrees: pointerTo(275), expectedReturned: true},
		{name: "pothole 136 degrees off", eventTag: "pothole", hazardHeadingDegrees: pointerTo(276), expectedReturned: false},

		// The same threshold on the other side passes through north:
		// 140 - 135 = 5, and one degree further is 4.
		{name: "pothole 135 degrees off the other way", eventTag: "pothole", hazardHeadingDegrees: pointerTo(5), expectedReturned: true},
		{name: "pothole 136 degrees off the other way", eventTag: "pothole", hazardHeadingDegrees: pointerTo(4), expectedReturned: false},
		// 350 is 150 degrees from 140 going through north, not 210.
		{name: "pothole at 350 degrees is 150 off", eventTag: "pothole", hazardHeadingDegrees: pointerTo(350), expectedReturned: false},

		// Nothing to compare with, so it goes to everyone.
		{name: "pothole without a heading", eventTag: "pothole", hazardHeadingDegrees: nil, expectedReturned: true},

		// An accident affects both directions, so its heading is ignored.
		{name: "accident recorded by oncoming traffic", eventTag: "accident", hazardHeadingDegrees: pointerTo(320), expectedReturned: true},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
			seedHazardFromVehicle(test, transaction, testCase.eventTag, 100, vehicleHeadingDegrees, testCase.hazardHeadingDegrees)

			searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

			require.NoError(test, err)
			if testCase.expectedReturned {
				assert.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
			} else {
				assert.Empty(test, searchResult.Hazards)
			}
		})
	}
}

// Below 5 kilometers per hour a positioning receiver's heading is unreliable:
// a car standing in a jam can report any direction. So the search stops
// trusting it (docs/design.md section 5): the circle is centered on the
// vehicle itself, with the minimum distance as its radius, and the direction
// rule is switched off.
func TestFindNearbySearchesAroundASlowVehicle(test *testing.T) {
	const besideVehicleDegrees = 230.0 // 90 degrees to the right of 140

	testCases := []struct {
		name                   string
		speedKilometersPerHour float64
		hazardDistanceMeters   float64
		hazardDirectionDegrees float64
		hazardHeadingDegrees   float64
		expectedReturned       bool
	}{
		// Stopped: everything within 150 meters, in every direction.
		{name: "stopped, 100 meters behind", speedKilometersPerHour: 0, hazardDistanceMeters: 100, hazardDirectionDegrees: behindVehicleDegrees, hazardHeadingDegrees: vehicleHeadingDegrees, expectedReturned: true},
		{name: "stopped, 140 meters to the side", speedKilometersPerHour: 0, hazardDistanceMeters: 140, hazardDirectionDegrees: besideVehicleDegrees, hazardHeadingDegrees: vehicleHeadingDegrees, expectedReturned: true},
		{name: "stopped, 140 meters ahead", speedKilometersPerHour: 0, hazardDistanceMeters: 140, hazardDirectionDegrees: vehicleHeadingDegrees, hazardHeadingDegrees: vehicleHeadingDegrees, expectedReturned: true},
		// With the circle ahead of the vehicle this would be returned (it
		// reaches 300 meters ahead). Centered on the vehicle it is outside.
		{name: "stopped, 160 meters ahead", speedKilometersPerHour: 0, hazardDistanceMeters: 160, hazardDirectionDegrees: vehicleHeadingDegrees, hazardHeadingDegrees: vehicleHeadingDegrees, expectedReturned: false},
		{name: "stopped, pothole recorded by oncoming traffic", speedKilometersPerHour: 0, hazardDistanceMeters: 100, hazardDirectionDegrees: vehicleHeadingDegrees, hazardHeadingDegrees: behindVehicleDegrees, expectedReturned: true},

		// Crawling at 4 kilometers per hour is still "slow".
		{name: "4 kilometers per hour, 100 meters behind", speedKilometersPerHour: 4, hazardDistanceMeters: 100, hazardDirectionDegrees: behindVehicleDegrees, hazardHeadingDegrees: vehicleHeadingDegrees, expectedReturned: true},

		// From 5 kilometers per hour the heading is trusted again.
		{name: "5 kilometers per hour, 100 meters behind", speedKilometersPerHour: 5, hazardDistanceMeters: 100, hazardDirectionDegrees: behindVehicleDegrees, hazardHeadingDegrees: vehicleHeadingDegrees, expectedReturned: false},
		{name: "5 kilometers per hour, pothole recorded by oncoming traffic", speedKilometersPerHour: 5, hazardDistanceMeters: 100, hazardDirectionDegrees: vehicleHeadingDegrees, hazardHeadingDegrees: behindVehicleDegrees, expectedReturned: false},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
			seedHazardFromVehicle(test, transaction, "pothole", testCase.hazardDistanceMeters,
				testCase.hazardDirectionDegrees, pointerTo(testCase.hazardHeadingDegrees))

			searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(testCase.speedKilometersPerHour))

			require.NoError(test, err)
			if testCase.expectedReturned {
				assert.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
			} else {
				assert.Empty(test, searchResult.Hazards)
			}
			// A slow vehicle is still told to poll after the minimum distance.
			assert.InDelta(test, 150, searchResult.NextPollDistanceMeters, 0.01)
		})
	}
}

// --- fixtures for the road-following search ---

// The road in these tests turns: from 100 meters behind the vehicle it runs
// straight on (140 degrees) to a corner 100 meters ahead, turns right (230
// degrees) and reaches the hazard 290 meters after the corner.
//
//	behind ---- vehicle ---- corner
//	                           |
//	                           |
//	                         hazard
//
// By road the hazard is 390 meters ahead, inside the 400 meter reach at 60
// kilometers per hour. In a straight line it is far off to the right, outside
// the circle. So only the road-following search can find it.
type roadWithACorner struct {
	path           []hazards.Position
	hazardPosition hazards.Position
}

func buildRoadWithACorner(test *testing.T, transaction *sql.Tx) roadWithACorner {
	test.Helper()
	vehiclePosition := hazards.Position{Latitude: vehicleLatitude, Longitude: vehicleLongitude}
	corner := positionFrom(test, transaction, vehiclePosition, 100, vehicleHeadingDegrees)
	hazardPosition := positionFrom(test, transaction, corner, 290, 230)
	return roadWithACorner{
		path: []hazards.Position{
			positionFrom(test, transaction, vehiclePosition, 100, behindVehicleDegrees),
			corner,
			positionFrom(test, transaction, corner, 100, 230),
			positionFrom(test, transaction, corner, 200, 230),
			hazardPosition,
		},
		hazardPosition: hazardPosition,
	}
}

// seedPotholeAt places a live pothole at a position. Its heading is the
// direction of the road's last stretch (230 degrees), as its reporter drove it.
func seedPotholeAt(test *testing.T, transaction *sql.Tx, position hazards.Position) int64 {
	test.Helper()
	return seedHazard(test, transaction, seededHazard{
		EventTag:       "pothole",
		Latitude:       position.Latitude,
		Longitude:      position.Longitude,
		HeadingDegrees: pointerTo(230),
		ReportedBy:     jetsonDeviceID,
		ExpiresIn:      time.Hour,
	})
}

// --- tests for the road-following search ---

// The reason approach paths exist (docs/design.md section 5): a hazard around
// a corner is found because a path leads from the vehicle to it, and its
// distance is the distance by road.
func TestFindNearbyFollowsTheRoadAroundACorner(test *testing.T) {
	test.Run("with an approach path the hazard is found, 390 meters by road", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
		road := buildRoadWithACorner(test, transaction)
		hazardID := seedPotholeAt(test, transaction, road.hazardPosition)
		seedApproachPath(test, transaction, hazardID, road.path)

		searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

		require.NoError(test, err)
		require.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
		hazard := searchResult.Hazards[0]
		assert.Equal(test, hazardID, hazard.ID)
		assert.Equal(test, hazards.MatchedByRoad, hazard.MatchedBy)
		// 100 meters to the corner plus 290 after it, not the straight line
		// (about 307 meters).
		assert.InDelta(test, 390, hazard.DistanceMeters, 1)
	})

	// The same hazard without a path: the circle looks straight ahead and
	// misses it. This is the limitation the paths remove.
	test.Run("without an approach path the hazard is not found", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
		road := buildRoadWithACorner(test, transaction)
		seedPotholeAt(test, transaction, road.hazardPosition)

		searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

		require.NoError(test, err)
		assert.Empty(test, searchResult.Hazards)
	})
}

// straightPath is a path that starts at a position and runs in a straight
// line in a compass direction, with a point every 100 meters. Its last point
// is where the hazard goes.
func straightPath(test *testing.T, transaction *sql.Tx, start hazards.Position, directionDegrees float64, lengthMeters float64) []hazards.Position {
	test.Helper()
	path := []hazards.Position{start}
	for distanceMeters := 100.0; distanceMeters <= lengthMeters; distanceMeters += 100 {
		path = append(path, positionFrom(test, transaction, start, distanceMeters, directionDegrees))
	}
	return path
}

// The limits of the road-following search (docs/design.md section 5). In
// every case the hazard is placed where the straight-ahead circle can't reach
// it, so the path alone decides whether it is returned.
func TestFindNearbyMatchesAPathOnlyWhenTheVehicleIsOnIt(test *testing.T) {
	vehiclePosition := hazards.Position{Latitude: vehicleLatitude, Longitude: vehicleLongitude}
	const rightOfVehicleDegrees = 230.0
	const leftOfVehicleDegrees = 50.0

	// The vehicle's own road: from 100 meters behind it, straight on to a
	// hazard 900 meters ahead. moveSidewaysMeters shifts the whole road to
	// the right, to test how close a path has to be.
	ownRoad := func(moveSidewaysMeters float64) func(*testing.T, *sql.Tx) []hazards.Position {
		return func(test *testing.T, transaction *sql.Tx) []hazards.Position {
			onRoad := positionFrom(test, transaction, vehiclePosition, moveSidewaysMeters, rightOfVehicleDegrees)
			start := positionFrom(test, transaction, onRoad, 100, behindVehicleDegrees)
			return straightPath(test, transaction, start, vehicleHeadingDegrees, 1000)
		}
	}

	testCases := []struct {
		name                  string
		buildPath             func(*testing.T, *sql.Tx) []hazards.Position
		vehicleHeadingDegrees float64
		hazardExpired         bool
		// expectedDistanceMeters is the distance by road; 0 means the hazard
		// must not be returned.
		expectedDistanceMeters float64
	}{
		// There is no limit on how far along a path a hazard may be (user's
		// call): someone drove from here to that hazard, so it is on this road.
		// The circle's reach at this speed is only 400 meters.
		{name: "a hazard 900 meters ahead by road", buildPath: ownRoad(0), vehicleHeadingDegrees: 140, expectedDistanceMeters: 900},

		// How close the path must pass: at most 20 meters.
		{name: "the path passes 15 meters from the vehicle", buildPath: ownRoad(15), vehicleHeadingDegrees: 140, expectedDistanceMeters: 900},
		{name: "the path passes 25 meters from the vehicle", buildPath: ownRoad(25), vehicleHeadingDegrees: 140},

		// How far the vehicle's heading may be from the road's direction (140
		// degrees): at most 60.
		{name: "the vehicle's heading is 55 degrees off the road", buildPath: ownRoad(0), vehicleHeadingDegrees: 195, expectedDistanceMeters: 900},
		{name: "the vehicle's heading is 65 degrees off the road", buildPath: ownRoad(0), vehicleHeadingDegrees: 205},

		// The opposite carriageway: a path through the vehicle's position that
		// runs the other way, to a hazard 300 meters behind the vehicle.
		{
			name: "a path running the opposite way",
			buildPath: func(test *testing.T, transaction *sql.Tx) []hazards.Position {
				start := positionFrom(test, transaction, vehiclePosition, 100, vehicleHeadingDegrees)
				return straightPath(test, transaction, start, behindVehicleDegrees, 400)
			},
			vehicleHeadingDegrees: 140,
		},

		// A crossing road: a path through the vehicle's position from left to
		// right, to a hazard 300 meters to the right.
		{
			name: "a path crossing the vehicle's road",
			buildPath: func(test *testing.T, transaction *sql.Tx) []hazards.Position {
				start := positionFrom(test, transaction, vehiclePosition, 100, leftOfVehicleDegrees)
				return straightPath(test, transaction, start, rightOfVehicleDegrees, 400)
			},
			vehicleHeadingDegrees: 140,
		},

		{name: "an expired hazard on the vehicle's road", buildPath: ownRoad(0), vehicleHeadingDegrees: 140, hazardExpired: true},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
			path := testCase.buildPath(test, transaction)
			hazardID := seedPotholeAt(test, transaction, path[len(path)-1])
			seedApproachPath(test, transaction, hazardID, path)
			if testCase.hazardExpired {
				_, err := transaction.Exec(`UPDATE hazards SET expires_at = now() - interval '1 second' WHERE id = $1`, hazardID)
				require.NoError(test, err)
			}
			searchQuery := vehicleSearchQuery(60)
			searchQuery.HeadingDegrees = testCase.vehicleHeadingDegrees

			searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, searchQuery)

			require.NoError(test, err)
			if testCase.expectedDistanceMeters == 0 {
				assert.Empty(test, searchResult.Hazards)
				return
			}
			require.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
			assert.Equal(test, hazards.MatchedByRoad, searchResult.Hazards[0].MatchedBy)
			assert.InDelta(test, testCase.expectedDistanceMeters, searchResult.Hazards[0].DistanceMeters, 1)
		})
	}
}

// A hazard can have several paths that match the same vehicle. It is returned
// once, with the shortest distance by road.
func TestFindNearbyUsesTheShortestOfSeveralMatchingPaths(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	vehiclePosition := hazards.Position{Latitude: vehicleLatitude, Longitude: vehicleLongitude}
	start := positionFrom(test, transaction, vehiclePosition, 100, behindVehicleDegrees)

	// The direct road: 500 meters from the vehicle to the hazard.
	directPath := straightPath(test, transaction, start, vehicleHeadingDegrees, 600)
	hazardPosition := directPath[len(directPath)-1]
	// A detour to the same hazard: 200 meters ahead, 100 to the right, 200
	// ahead, 100 back to the left, then the last 100 meters. 700 by road.
	turnOff := positionFrom(test, transaction, vehiclePosition, 200, vehicleHeadingDegrees)
	sideRoadStart := positionFrom(test, transaction, turnOff, 100, 230)
	sideRoadEnd := positionFrom(test, transaction, sideRoadStart, 200, vehicleHeadingDegrees)
	rejoin := positionFrom(test, transaction, sideRoadEnd, 100, 50)
	detourPath := []hazards.Position{start, turnOff, sideRoadStart, sideRoadEnd, rejoin, hazardPosition}

	hazardID := seedPotholeAt(test, transaction, hazardPosition)
	seedApproachPath(test, transaction, hazardID, detourPath)
	seedApproachPath(test, transaction, hazardID, directPath)

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	require.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
	assert.InDelta(test, 500, searchResult.Hazards[0].DistanceMeters, 1)
}

// --- tests for how the two searches are joined ---

// Every hazard in an answer says how it was found (docs/design.md section 5),
// so a device can announce a road match firmly and a straight-ahead match
// more softly.
func TestFindNearbyMarksAHazardFoundOnlyStraightAhead(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	seedPotholeFromVehicle(test, transaction, 100, vehicleHeadingDegrees)

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	require.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
	assert.Equal(test, hazards.MatchedByStraightAhead, searchResult.Hazards[0].MatchedBy)
	assert.InDelta(test, 100, searchResult.Hazards[0].DistanceMeters, 0.5)
}

// A hazard 300 meters straight ahead is inside the circle, and its path also
// passes the vehicle. The path makes a detour, so the two searches disagree
// about the distance. The hazard is returned once, as the road match, with the
// distance by road.
func TestFindNearbyReturnsAHazardFoundBothWaysOnceAsARoadMatch(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	vehiclePosition := hazards.Position{Latitude: vehicleLatitude, Longitude: vehicleLongitude}
	hazardPosition := positionFrom(test, transaction, vehiclePosition, 300, vehicleHeadingDegrees)
	// 100 meters ahead, 80 to the right, 100 ahead, 80 back to the left, and
	// the last 100 meters: 460 by road.
	turnOff := positionFrom(test, transaction, vehiclePosition, 100, vehicleHeadingDegrees)
	sideRoadStart := positionFrom(test, transaction, turnOff, 80, 230)
	sideRoadEnd := positionFrom(test, transaction, sideRoadStart, 100, vehicleHeadingDegrees)
	rejoin := positionFrom(test, transaction, sideRoadEnd, 80, 50)
	hazardID := seedPotholeAt(test, transaction, hazardPosition)
	seedApproachPath(test, transaction, hazardID, []hazards.Position{
		positionFrom(test, transaction, vehiclePosition, 100, behindVehicleDegrees),
		turnOff, sideRoadStart, sideRoadEnd, rejoin, hazardPosition,
	})

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	require.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
	assert.Equal(test, hazards.MatchedByRoad, searchResult.Hazards[0].MatchedBy)
	assert.InDelta(test, 460, searchResult.Hazards[0].DistanceMeters, 1)
}

// The user's call (design section 5): a hazard that has paths, none of them
// matching this vehicle, is still returned by the circle. Here the hazard's
// only path belongs to a crossing road. Leaving the hazard out would miss a
// vehicle arriving by a road nobody has recorded yet.
func TestFindNearbyStillReturnsAHazardWhosePathsDoNotMatch(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	vehiclePosition := hazards.Position{Latitude: vehicleLatitude, Longitude: vehicleLongitude}
	hazardPosition := positionFrom(test, transaction, vehiclePosition, 100, vehicleHeadingDegrees)
	hazardID := seedPotholeAt(test, transaction, hazardPosition)
	// A path that reaches the hazard from the left, at a right angle to the
	// vehicle's road.
	crossingRoadStart := positionFrom(test, transaction, hazardPosition, 300, 50)
	seedApproachPath(test, transaction, hazardID, straightPath(test, transaction, crossingRoadStart, 230, 300))

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	require.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
	assert.Equal(test, hazards.MatchedByStraightAhead, searchResult.Hazards[0].MatchedBy)
	assert.InDelta(test, 100, searchResult.Hazards[0].DistanceMeters, 0.5)
}

// seedRoadHazardAhead places a pothole on the vehicle's own straight road, the
// given distance ahead, with a path from 100 meters behind the vehicle to it.
func seedRoadHazardAhead(test *testing.T, transaction *sql.Tx, distanceAheadMeters float64) int64 {
	test.Helper()
	vehiclePosition := hazards.Position{Latitude: vehicleLatitude, Longitude: vehicleLongitude}
	hazardPosition := positionFrom(test, transaction, vehiclePosition, distanceAheadMeters, vehicleHeadingDegrees)
	hazardID := seedPotholeAt(test, transaction, hazardPosition)
	seedApproachPath(test, transaction, hazardID, []hazards.Position{
		positionFrom(test, transaction, vehiclePosition, 100, behindVehicleDegrees),
		hazardPosition,
	})
	return hazardID
}

// Nearest first holds across both searches: a road match is not listed before
// a nearer straight-ahead match just because it is a road match.
func TestFindNearbyOrdersTheJoinedResultNearestFirst(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	// 600 meters is beyond the circle, so only its path finds this one.
	roadHazardAt600MetersID := seedRoadHazardAhead(test, transaction, 600)
	straightAheadHazardAt350MetersID := seedPotholeFromVehicle(test, transaction, 350, vehicleHeadingDegrees)
	roadHazardAt250MetersID := seedRoadHazardAhead(test, transaction, 250)
	straightAheadHazardAt100MetersID := seedPotholeFromVehicle(test, transaction, 100, vehicleHeadingDegrees)

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	var actualHazardIDs []int64
	for _, hazard := range searchResult.Hazards {
		actualHazardIDs = append(actualHazardIDs, hazard.ID)
	}
	assert.Equal(test, []int64{
		straightAheadHazardAt100MetersID, roadHazardAt250MetersID,
		straightAheadHazardAt350MetersID, roadHazardAt600MetersID,
	}, actualHazardIDs)
}

// The limit of 100 is for the whole answer, not for each search.
func TestFindNearbyLimitsTheJoinedResultToOneHundred(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	// 70 hazards the circle finds, 5 to 350 meters ahead.
	for distanceMeters := 5.0; distanceMeters <= 350; distanceMeters += 5 {
		seedPotholeFromVehicle(test, transaction, distanceMeters, vehicleHeadingDegrees)
	}
	// 40 hazards only their paths find, 410 to 800 meters ahead.
	for distanceMeters := 410.0; distanceMeters <= 800; distanceMeters += 10 {
		seedRoadHazardAhead(test, transaction, distanceMeters)
	}

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(60))

	require.NoError(test, err)
	require.Equal(test, 100, len(searchResult.Hazards), "number of hazards returned")
	// All 70 from the circle and the nearest 30 by road: the last is at 700 meters.
	farthestHazardReturned := searchResult.Hazards[99]
	assert.Equal(test, hazards.MatchedByRoad, farthestHazardReturned.MatchedBy)
	assert.InDelta(test, 700, farthestHazardReturned.DistanceMeters, 1)
}

// Following a road needs the vehicle's heading, which isn't trusted below 5
// kilometers per hour. A slow vehicle gets only the circle around itself.
func TestFindNearbyDoesNotFollowTheRoadForASlowVehicle(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	seedRoadHazardAhead(test, transaction, 600)
	hazardBehindID := seedPotholeFromVehicle(test, transaction, 100, behindVehicleDegrees)

	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, vehicleSearchQuery(0))

	require.NoError(test, err)
	require.Equal(test, 1, len(searchResult.Hazards), "number of hazards returned")
	assert.Equal(test, hazardBehindID, searchResult.Hazards[0].ID)
	assert.Equal(test, hazards.MatchedByStraightAhead, searchResult.Hazards[0].MatchedBy)
}
