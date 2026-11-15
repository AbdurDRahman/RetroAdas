package hazards

import (
	"database/sql"
	"fmt"
)

// The limits of the road-following search (docs/design.md section 5). They
// are tunable numbers, not architecture.
const (
	// MaximumDistanceToApproachPathMeters is how close a path must come to the
	// vehicle to count as passing through its position. It has to absorb the
	// positioning error of the trail and of the vehicle, and the road's width.
	MaximumDistanceToApproachPathMeters = 20

	// MaximumHeadingDifferenceToApproachPathDegrees is how far the vehicle's
	// heading may be from the path's direction. It is much tighter than the
	// limit between a report and a hazard: a path's direction comes from its
	// line and is accurate, so only the vehicle's compass error has to fit.
	// It keeps a crossing road and oncoming traffic out.
	MaximumHeadingDifferenceToApproachPathDegrees = 60

	// pathDirectionSampleMeters: the path's direction at the vehicle's place
	// is taken between two places this far behind and ahead of it.
	pathDirectionSampleMeters = 10
)

// findHazardsOnRoad returns the live hazards on the road the vehicle is
// driving: those with an approach path that passes the vehicle's position in
// its direction of travel. Someone who was here, going this way, reached that
// hazard.
//
// The distance is measured along the path, so it is the distance by road. The
// hazard's own stored heading is not looked at: on a winding road it can
// differ from the vehicle's current heading by any amount, and the path
// already says which way the road goes here.
//
// There is no limit on how far along the path the hazard may be. The circle
// needs one, because a bigger circle catches more that is not on the vehicle's
// road; a path has no such problem, and its length is already limited
// (MaximumApproachPathLengthMeters).
func findHazardsOnRoad(transaction *sql.Tx, searchQuery SearchQuery) ([]NearbyHazard, error) {
	// The query in four steps:
	//   located   the paths near the vehicle, each with the vehicle's place on
	//             it as a fraction (0 = the path's start, 1 = its end) and the
	//             path's length.
	//   measured  for each, the distance from that place to the path's end,
	//             and the path's compass direction at that place.
	//   matching  the paths going the vehicle's way; per hazard, the shortest
	//             distance.
	//   the final SELECT adds the hazard's details and drops expired hazards.
	rows, err := transaction.Query(
		`WITH vehicle AS (
		     SELECT ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography AS position
		 ),
		 located AS (
		     SELECT paths.hazard_id, paths.path,
		            ST_LineLocatePoint(paths.path, vehicle.position) AS fraction,
		            ST_Length(paths.path) AS path_length_meters
		     FROM hazard_approach_paths AS paths, vehicle
		     WHERE ST_DWithin(paths.path, vehicle.position, $3::double precision)
		 ),
		 measured AS (
		     SELECT hazard_id,
		            (1 - fraction) * path_length_meters AS distance_along_meters,
		            degrees(ST_Azimuth(
		                ST_LineInterpolatePoint(path, greatest(fraction - $5::double precision / path_length_meters, 0)),
		                ST_LineInterpolatePoint(path, least(fraction + $5::double precision / path_length_meters, 1))
		            )) AS path_direction_degrees
		     FROM located
		 ),
		 matching AS (
		     SELECT hazard_id, min(distance_along_meters) AS distance_meters
		     FROM measured
		     -- The smaller angle between two compass directions, as in the
		     -- circle search.
		     WHERE 180 - abs(abs(path_direction_degrees - $4::double precision) - 180) <= $6::double precision
		     GROUP BY hazard_id
		 )
		 SELECT hazards.id, hazards.event_tag,
		        ST_Y(hazards.location::geometry), ST_X(hazards.location::geometry),
		        matching.distance_meters,
		        hazards.confidence, hazards.confirmations
		 FROM matching
		 JOIN hazards ON hazards.id = matching.hazard_id
		 WHERE hazards.expires_at > now()
		 ORDER BY matching.distance_meters, hazards.id
		 LIMIT $7`,
		searchQuery.Longitude, searchQuery.Latitude, MaximumDistanceToApproachPathMeters,
		searchQuery.HeadingDegrees, pathDirectionSampleMeters,
		MaximumHeadingDifferenceToApproachPathDegrees, MaximumHazardsPerSearch,
	)
	if err != nil {
		return nil, fmt.Errorf("search for hazards on the road: %w", err)
	}
	return readNearbyHazards(rows, MatchedByRoad)
}
