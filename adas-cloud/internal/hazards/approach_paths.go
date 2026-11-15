package hazards

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// Position is one point of a trail.
type Position struct {
	Latitude  float64
	Longitude float64
}

// The limits a trail must stay inside (docs/design.md section 5). They are
// tunable numbers, not architecture.
const (
	MinimumApproachPathPoints = 2
	MaximumApproachPathPoints = 80

	// The whole path, from its first point to the reported position.
	MinimumApproachPathLengthMeters = 50
	MaximumApproachPathLengthMeters = 1200

	// MaximumApproachPathGapMeters is the most two neighbouring points may be
	// apart. A larger gap is a positioning jump or a tunnel, and the line
	// drawn across it would not be the road.
	MaximumApproachPathGapMeters = 200

	// MaximumApproachPathEndDistanceMeters is how far the trail's last point
	// may be from the reported position. A trail that ends somewhere else
	// does not show how this hazard was reached.
	MaximumApproachPathEndDistanceMeters = 50
)

// The limits on what a hazard keeps (docs/design.md section 5).
const (
	// MaximumApproachPathsPerHazard is how many paths a hazard keeps. When a
	// new route arrives at a full hazard, the oldest path is dropped.
	MaximumApproachPathsPerHazard = 8

	// MaximumApproachPathsPerDevicePerHazard stops one device from filling
	// every place of a hazard.
	MaximumApproachPathsPerDevicePerHazard = 2

	// SameRouteDistanceMeters: a new trail that stays this close to a stored
	// path along its whole length is the same route, and is not stored again.
	SameRouteDistanceMeters = 15

	// sameRouteCheckSpacingMeters is how often along the new trail its
	// distance to a stored path is checked. Checking only the recorded points
	// would miss a trail that leaves the road between two of them.
	sameRouteCheckSpacingMeters = 10
)

// ErrInvalidApproachPath: the report's trail breaks one of the limits above.
// The whole report is refused. HTTP 400.
var ErrInvalidApproachPath = errors.New("invalid approach path")

// checkApproachPathShape checks what needs no distances: the number of points,
// and that each one is a real position on Earth.
func checkApproachPathShape(approachPath []Position) error {
	pointCount := len(approachPath)
	if pointCount < MinimumApproachPathPoints || pointCount > MaximumApproachPathPoints {
		return fmt.Errorf("%w: %d points, expected %d to %d", ErrInvalidApproachPath,
			pointCount, MinimumApproachPathPoints, MaximumApproachPathPoints)
	}
	for pointNumber, position := range approachPath {
		// Written as "not inside the range" on purpose: a value that is not a
		// number fails every comparison, so "outside the range" would let it through.
		latitudeIsValid := position.Latitude >= -90 && position.Latitude <= 90
		longitudeIsValid := position.Longitude >= -180 && position.Longitude <= 180
		if !latitudeIsValid || !longitudeIsValid {
			return fmt.Errorf("%w: point %d is not a position on Earth", ErrInvalidApproachPath, pointNumber+1)
		}
	}
	return nil
}

// checkApproachPathDistances checks the trail's distances with PostGIS, the
// same measure every other distance in this system uses.
func checkApproachPathDistances(transaction *sql.Tx, report Report) error {
	// The line measured here is the one that would be stored.
	longitudes, latitudes := approachPathCoordinates(report)

	// "steps" holds the distance from each point to the one before it. The
	// last step is the one from the trail's last point to the report; every
	// earlier step is a gap inside the trail.
	var pathLengthMeters, largestGapMeters, endDistanceMeters float64
	err := transaction.QueryRow(
		`WITH points AS (
		     SELECT point_number,
		            ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geography AS position
		     FROM unnest($1::double precision[], $2::double precision[])
		          WITH ORDINALITY AS trail (longitude, latitude, point_number)
		 ),
		 steps AS (
		     SELECT point_number,
		            ST_Distance(position, lag(position) OVER (ORDER BY point_number)) AS step_meters
		     FROM points
		 )
		 SELECT sum(step_meters),
		        max(step_meters) FILTER (WHERE point_number < cardinality($1::double precision[])),
		        max(step_meters) FILTER (WHERE point_number = cardinality($1::double precision[]))
		 FROM steps`,
		pq.Array(longitudes), pq.Array(latitudes),
	).Scan(&pathLengthMeters, &largestGapMeters, &endDistanceMeters)
	if err != nil {
		return fmt.Errorf("measure the approach path: %w", err)
	}

	if largestGapMeters > MaximumApproachPathGapMeters {
		return fmt.Errorf("%w: two neighbouring points are %.0f meters apart, at most %d allowed",
			ErrInvalidApproachPath, largestGapMeters, MaximumApproachPathGapMeters)
	}
	if endDistanceMeters > MaximumApproachPathEndDistanceMeters {
		return fmt.Errorf("%w: it ends %.0f meters from the reported position, at most %d allowed",
			ErrInvalidApproachPath, endDistanceMeters, MaximumApproachPathEndDistanceMeters)
	}
	if pathLengthMeters < MinimumApproachPathLengthMeters || pathLengthMeters > MaximumApproachPathLengthMeters {
		return fmt.Errorf("%w: it is %.0f meters long, expected %d to %d",
			ErrInvalidApproachPath, pathLengthMeters, MinimumApproachPathLengthMeters, MaximumApproachPathLengthMeters)
	}
	return nil
}

// approachPathCoordinates returns the path a report's trail becomes: the
// trail's points in order, then the reported position as the final point, so
// a path always ends at the report. The two lists are what the queries take:
// Postgres receives a list of numbers easily, but not a list of points.
func approachPathCoordinates(report Report) (longitudes []float64, latitudes []float64) {
	for _, position := range report.ApproachPath {
		longitudes = append(longitudes, position.Longitude)
		latitudes = append(latitudes, position.Latitude)
	}
	longitudes = append(longitudes, report.Longitude)
	latitudes = append(latitudes, report.Latitude)
	return longitudes, latitudes
}

// newPathSQL is the start of every query that needs the report's path as one
// line. It turns two lists of numbers ($2 longitudes, $3 latitudes) into
// points and joins them. "ORDER BY point_number" is needed: without it
// Postgres may join the points in any order.
const newPathSQL = `
	WITH new_path AS (
	    SELECT ST_MakeLine(ST_SetSRID(ST_MakePoint(longitude, latitude), 4326) ORDER BY point_number)::geography AS path
	    FROM unnest($2::double precision[], $3::double precision[])
	         WITH ORDINALITY AS trail (longitude, latitude, point_number)
	)`

// addApproachPath stores the report's trail as an approach path of the hazard,
// unless a limit says no (docs/design.md section 5). It returns whether the
// path was stored. Not storing is a normal outcome, not an error.
func addApproachPath(transaction *sql.Tx, hazardID int64, report Report) (bool, error) {
	longitudes, latitudes := approachPathCoordinates(report)

	// The route check comes first, so a trail the hazard already has never
	// uses up one of the device's places.
	alreadyKnown, err := hazardHasRoute(transaction, hazardID, longitudes, latitudes)
	if err != nil {
		return false, err
	}
	if alreadyKnown {
		return false, nil
	}

	deviceMayAdd, err := countPathForDevice(transaction, hazardID, report.ReportedBy)
	if err != nil {
		return false, err
	}
	if !deviceMayAdd {
		return false, nil
	}

	// The row holds the hazard and the line only: no device and no time
	// (section 7).
	_, err = transaction.Exec(
		newPathSQL+`
		 INSERT INTO hazard_approach_paths (hazard_id, path)
		 SELECT $1, path FROM new_path`,
		hazardID, pq.Array(longitudes), pq.Array(latitudes),
	)
	if err != nil {
		return false, fmt.Errorf("store the approach path: %w", err)
	}

	if err := dropOldestPathsOverTheLimit(transaction, hazardID); err != nil {
		return false, err
	}
	return true, nil
}

// hazardHasRoute reports whether the new trail lies on top of a path the
// hazard already has.
//
// Both are lines, and lines are what is compared, never single points: two
// cars on one road record their points at different places. The new trail is
// cut into pieces of at most sameRouteCheckSpacingMeters (ST_Segmentize), and
// every corner of those pieces must be within SameRouteDistanceMeters of the
// stored line, measured to the nearest place anywhere on it.
func hazardHasRoute(transaction *sql.Tx, hazardID int64, longitudes []float64, latitudes []float64) (bool, error) {
	var alreadyKnown bool
	// Read as: "is there a stored path for which no check position of the new
	// trail is too far away?"
	err := transaction.QueryRow(
		newPathSQL+`,
		 check_positions AS (
		     SELECT position.geom::geography AS position
		     FROM new_path,
		          ST_DumpPoints(ST_Segmentize(new_path.path, $4::double precision)::geometry) AS position
		 )
		 SELECT EXISTS (
		     SELECT 1
		     FROM hazard_approach_paths AS stored
		     WHERE stored.hazard_id = $1
		       AND NOT EXISTS (
		           SELECT 1 FROM check_positions
		           WHERE NOT ST_DWithin(check_positions.position, stored.path, $5::double precision)
		       )
		 )`,
		hazardID, pq.Array(longitudes), pq.Array(latitudes),
		sameRouteCheckSpacingMeters, SameRouteDistanceMeters,
	).Scan(&alreadyKnown)
	if err != nil {
		return false, fmt.Errorf("compare the approach path with the stored ones: %w", err)
	}
	return alreadyKnown, nil
}

// countPathForDevice adds one to the number of paths this device has added to
// this hazard, and reports whether that is allowed. At the limit it changes
// nothing and returns false.
//
// The count lives in its own table so that a path row never names a device
// (section 7). It is never lowered, also not when a path is dropped: otherwise
// a device could add its paths, wait for them to be dropped, and add more.
func countPathForDevice(transaction *sql.Tx, hazardID int64, deviceID string) (bool, error) {
	// One statement does "first path: insert 1; later path: add 1, but only
	// below the limit". When the WHERE stops the update, no row is changed.
	result, err := transaction.Exec(
		`INSERT INTO hazard_approach_path_counts (hazard_id, device_id, paths_added)
		 VALUES ($1, $2, 1)
		 ON CONFLICT (hazard_id, device_id) DO UPDATE
		     SET paths_added = hazard_approach_path_counts.paths_added + 1
		     WHERE hazard_approach_path_counts.paths_added < $3`,
		hazardID, deviceID, MaximumApproachPathsPerDevicePerHazard,
	)
	if err != nil {
		return false, fmt.Errorf("count the device's approach paths: %w", err)
	}
	changedRows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count the device's approach paths: %w", err)
	}
	return changedRows == 1, nil
}

// dropOldestPathsOverTheLimit keeps the hazard's newest paths, up to the
// limit, and deletes the rest. The table has no time column, so "newest" is
// the highest id.
func dropOldestPathsOverTheLimit(transaction *sql.Tx, hazardID int64) error {
	_, err := transaction.Exec(
		`DELETE FROM hazard_approach_paths
		 WHERE hazard_id = $1
		   AND id NOT IN (
		       SELECT id FROM hazard_approach_paths
		       WHERE hazard_id = $1
		       ORDER BY id DESC
		       LIMIT $2
		   )`,
		hazardID, MaximumApproachPathsPerHazard,
	)
	if err != nil {
		return fmt.Errorf("drop the hazard's oldest approach paths: %w", err)
	}
	return nil
}
