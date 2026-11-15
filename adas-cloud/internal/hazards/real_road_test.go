package hazards_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// --- fixtures: a real road ---
//
// testdata/salt_range_m2.geojson holds about 5 kilometers of the M-2 motorway
// through the Salt Range, both carriageways, from OpenStreetMap (credit and
// details in testdata/README.md). These helpers let a test say "the vehicle
// is 300 meters along the southbound carriageway" and get the real position
// and the real direction of the road there.

// testRoad is one road of the file: its points in driving order.
type testRoad struct {
	longitudes []float64
	latitudes  []float64
}

func loadTestRoad(test *testing.T, name string) testRoad {
	test.Helper()
	fileContent, err := os.ReadFile("testdata/salt_range_m2.geojson")
	require.NoError(test, err)

	var file struct {
		Features []struct {
			Properties struct {
				Name string `json:"name"`
			} `json:"properties"`
			Geometry struct {
				// GeoJSON writes a point as [longitude, latitude].
				Coordinates [][2]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	require.NoError(test, json.Unmarshal(fileContent, &file))

	for _, feature := range file.Features {
		if feature.Properties.Name != name {
			continue
		}
		var road testRoad
		for _, coordinate := range feature.Geometry.Coordinates {
			road.longitudes = append(road.longitudes, coordinate[0])
			road.latitudes = append(road.latitudes, coordinate[1])
		}
		return road
	}
	require.FailNow(test, "no road named "+name+" in the test data")
	return testRoad{}
}

// roadLineSQL starts every query below: it turns the road's two lists of
// numbers ($1 longitudes, $2 latitudes) into one line.
const roadLineSQL = `
	WITH road AS (
	    SELECT ST_MakeLine(ST_SetSRID(ST_MakePoint(longitude, latitude), 4326) ORDER BY point_number)::geography AS line
	    FROM unnest($1::double precision[], $2::double precision[])
	         WITH ORDINALITY AS points (longitude, latitude, point_number)
	)`

// placeOnRoad returns the position a distance along the road from its start,
// and the compass direction the road runs there (taken over the next 10 meters).
func placeOnRoad(test *testing.T, transaction *sql.Tx, road testRoad, metersAlong float64) (hazards.Position, float64) {
	test.Helper()
	var position hazards.Position
	var headingDegrees float64
	err := transaction.QueryRow(
		roadLineSQL+`
		 SELECT ST_Y(place::geometry), ST_X(place::geometry),
		        degrees(ST_Azimuth(place, ST_LineInterpolatePoint(line, least(($3::double precision + 10) / ST_Length(line), 1))))
		 FROM road,
		      LATERAL (SELECT ST_LineInterpolatePoint(line, $3::double precision / ST_Length(line)) AS place) AS located`,
		pq.Array(road.longitudes), pq.Array(road.latitudes), metersAlong,
	).Scan(&position.Latitude, &position.Longitude, &headingDegrees)
	require.NoError(test, err)
	return position, headingDegrees
}

// trailAlongRoad is what a device driving the road would have recorded: a
// point every 25 meters, from fromMeters up to (not including) toMeters, where
// it sends its report.
func trailAlongRoad(test *testing.T, transaction *sql.Tx, road testRoad, fromMeters float64, toMeters float64) []hazards.Position {
	test.Helper()
	rows, err := transaction.Query(
		roadLineSQL+`
		 SELECT ST_Y(place::geometry), ST_X(place::geometry)
		 FROM road,
		      generate_series($3::numeric, $4::numeric - 25, 25) AS meters_along,
		      LATERAL (SELECT ST_LineInterpolatePoint(line, meters_along::double precision / ST_Length(line)) AS place) AS located
		 ORDER BY meters_along`,
		pq.Array(road.longitudes), pq.Array(road.latitudes), fromMeters, toMeters,
	)
	require.NoError(test, err)
	defer rows.Close()

	var trail []hazards.Position
	for rows.Next() {
		var position hazards.Position
		require.NoError(test, rows.Scan(&position.Latitude, &position.Longitude))
		trail = append(trail, position)
	}
	require.NoError(test, rows.Err())
	return trail
}

// metersAlongRoadNearestTo returns how far along the road its nearest place to
// a position lies. It is how a test finds "the spot on the other carriageway
// right beside this one".
func metersAlongRoadNearestTo(test *testing.T, transaction *sql.Tx, road testRoad, position hazards.Position) float64 {
	test.Helper()
	var metersAlong float64
	err := transaction.QueryRow(
		roadLineSQL+`
		 SELECT ST_LineLocatePoint(line, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography) * ST_Length(line)
		 FROM road`,
		pq.Array(road.longitudes), pq.Array(road.latitudes), position.Longitude, position.Latitude,
	).Scan(&metersAlong)
	require.NoError(test, err)
	return metersAlong
}

// potholeMetersAlong is where the pothole of these tests lies on the
// southbound carriageway: just after the winding first kilometer, where the
// road swings from south to south-east, west, north-west and back to south.
const potholeMetersAlong = 1100

// reportPotholeOnTheSouthboundCarriageway sends a real report through
// ProcessReport, as a Jetson driving the southbound carriageway would: the
// pothole's position, the road's direction there and, if asked, the trail of
// the kilometer before it.
func reportPotholeOnTheSouthboundCarriageway(test *testing.T, transaction *sql.Tx, southbound testRoad, withTrail bool) int64 {
	test.Helper()
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	potholePosition, roadDirectionDegrees := placeOnRoad(test, transaction, southbound, potholeMetersAlong)
	report := hazards.Report{
		EventTag:       "pothole",
		Latitude:       potholePosition.Latitude,
		Longitude:      potholePosition.Longitude,
		HeadingDegrees: pointerTo(roadDirectionDegrees),
		Confidence:     pointerTo(0.9),
		ReportedBy:     jetsonDeviceID,
		DeviceType:     devices.DeviceTypeJetsonUnit,
	}
	if withTrail {
		report.ApproachPath = trailAlongRoad(test, transaction, southbound, potholeMetersAlong-1000, potholeMetersAlong)
	}
	outcome, err := hazards.ProcessReport(transaction, report)
	require.NoError(test, err)
	require.Equal(test, withTrail, outcome.ApproachPathStored)
	return outcome.ID
}

// searchFromRoad polls as a vehicle driving a road at 100 kilometers per hour,
// a distance along it, heading the way the road runs there.
func searchFromRoad(test *testing.T, transaction *sql.Tx, road testRoad, metersAlong float64) []hazards.NearbyHazard {
	test.Helper()
	position, roadDirectionDegrees := placeOnRoad(test, transaction, road, metersAlong)
	searchResult, err := hazards.FindNearby(transaction, searchTestPollingRules, hazards.SearchQuery{
		Latitude:               position.Latitude,
		Longitude:              position.Longitude,
		HeadingDegrees:         roadDirectionDegrees,
		SpeedKilometersPerHour: 100,
	})
	require.NoError(test, err)
	return searchResult.Hazards
}

// --- tests on the real road ---

// The whole point of approach paths, on a real mountain road: from every place
// in the bends before the pothole, the vehicle is told about it, with the
// distance by road.
func TestRealRoadAHazardBeyondTheBendsIsFoundAllTheWayThroughThem(test *testing.T) {
	southbound := loadTestRoad(test, "m2_southbound")
	for _, vehicleMetersAlong := range []float64{120, 300, 500, 700, 900, 1050} {
		test.Run(fmt.Sprintf("vehicle %.0f meters along", vehicleMetersAlong), func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			hazardID := reportPotholeOnTheSouthboundCarriageway(test, transaction, southbound, true)

			nearbyHazards := searchFromRoad(test, transaction, southbound, vehicleMetersAlong)

			require.Equal(test, 1, len(nearbyHazards), "number of hazards returned")
			assert.Equal(test, hazardID, nearbyHazards[0].ID)
			assert.Equal(test, hazards.MatchedByRoad, nearbyHazards[0].MatchedBy)
			// 5 meters of tolerance: the trail has a point every 25 meters,
			// so its straight pieces are a little shorter than the real bends.
			assert.InDelta(test, potholeMetersAlong-vehicleMetersAlong, nearbyHazards[0].DistanceMeters, 5)
		})
	}
}

// The same pothole reported without a trail, so only the straight-ahead circle
// can find it. This test records what the circle does on real bends, which is
// the reason the paths were built (docs/design.md section 5).
func TestRealRoadWithoutATrailTheCircleLosesTheHazardInTheBends(test *testing.T) {
	southbound := loadTestRoad(test, "m2_southbound")

	// In the bends the road points away from the pothole, and the circle
	// looks where the road points.
	for _, vehicleMetersAlong := range []float64{120, 300, 700} {
		test.Run(fmt.Sprintf("vehicle %.0f meters along gets nothing", vehicleMetersAlong), func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			reportPotholeOnTheSouthboundCarriageway(test, transaction, southbound, false)

			assert.Empty(test, searchFromRoad(test, transaction, southbound, vehicleMetersAlong))
		})
	}

	// Where the circle does catch it, the distance is the straight line
	// across the bend: far less than the 600 meters the vehicle will drive.
	test.Run("vehicle 500 meters along gets the straight-line distance", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		reportPotholeOnTheSouthboundCarriageway(test, transaction, southbound, false)

		nearbyHazards := searchFromRoad(test, transaction, southbound, 500)

		require.Equal(test, 1, len(nearbyHazards), "number of hazards returned")
		assert.Equal(test, hazards.MatchedByStraightAhead, nearbyHazards[0].MatchedBy)
		assert.Less(test, nearbyHazards[0].DistanceMeters, 400.0)
	})
}

func TestRealRoadAVehiclePastTheHazardIsNotToldAboutIt(test *testing.T) {
	southbound := loadTestRoad(test, "m2_southbound")
	transaction := testdatabase.NewTransaction(test)
	reportPotholeOnTheSouthboundCarriageway(test, transaction, southbound, true)

	assert.Empty(test, searchFromRoad(test, transaction, southbound, potholeMetersAlong+100))
}

// The two carriageways are 13 to 25 meters apart here, so the pothole's path
// passes within reach of a vehicle on the northbound side. Only the direction
// rule keeps it out, and it must: that pothole is not on the northbound
// carriageway.
func TestRealRoadTheOppositeCarriagewayIsNotToldAboutTheHazard(test *testing.T) {
	southbound := loadTestRoad(test, "m2_southbound")
	northbound := loadTestRoad(test, "m2_northbound")

	// Places beside the path (300 and 800 meters along the southbound side)
	// and one beyond the pothole, driving towards it (1,400 meters along).
	for _, southboundMetersAlong := range []float64{300, 800, 1400} {
		test.Run(fmt.Sprintf("beside southbound %.0f meters along", southboundMetersAlong), func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			reportPotholeOnTheSouthboundCarriageway(test, transaction, southbound, true)
			placeOnSouthbound, _ := placeOnRoad(test, transaction, southbound, southboundMetersAlong)
			northboundMetersAlong := metersAlongRoadNearestTo(test, transaction, northbound, placeOnSouthbound)

			assert.Empty(test, searchFromRoad(test, transaction, northbound, northboundMetersAlong))
		})
	}
}

// A service road leaves the motorway about 200 meters along the southbound
// side. A vehicle that has followed it to its end, 130 meters on, has left the
// pothole's road and is no longer told about it.
func TestRealRoadAVehicleThatLeftTheMotorwayIsNotToldAboutTheHazard(test *testing.T) {
	southbound := loadTestRoad(test, "m2_southbound")
	serviceRoad := loadTestRoad(test, "service_road_2")
	transaction := testdatabase.NewTransaction(test)
	reportPotholeOnTheSouthboundCarriageway(test, transaction, southbound, true)

	assert.Empty(test, searchFromRoad(test, transaction, serviceRoad, 130))
}
