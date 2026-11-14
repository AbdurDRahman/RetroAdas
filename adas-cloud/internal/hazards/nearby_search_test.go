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
