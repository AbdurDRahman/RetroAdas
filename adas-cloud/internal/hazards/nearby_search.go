package hazards

import "database/sql"

// SearchQuery matches the query parameters of GET /v1/hazards
// (docs/design.md section 8; names in section 12): the client sends its raw
// state and the server does the lookahead projection itself via ST_Project.
type SearchQuery struct {
	Latitude               float64
	Longitude              float64
	HeadingDegrees         float64
	SpeedKilometersPerHour float64
	RadiusMeters           int
}

// LookaheadDistanceMeters is the fixed distance ahead of the vehicle's
// position (along HeadingDegrees) that gets searched. C-103 replaces it with
// the agreed speed-based rule (docs/design.md section 5):
//
//	search_radius = lookahead_distance = poll_distance = speed × time_between_polls
const LookaheadDistanceMeters = 200

// NearbyHazard is one entry in the GET /v1/hazards response.
type NearbyHazard struct {
	ID                  int64
	EventTag            string
	Latitude, Longitude float64
	DistanceMeters      float64
	Confidence          float64
	Confirmations       int
}

// FindNearby projects a point LookaheadDistanceMeters ahead of
// (searchQuery.Latitude, searchQuery.Longitude) along
// searchQuery.HeadingDegrees, then returns all non-expired hazards within
// searchQuery.RadiusMeters of that projected point.
func FindNearby(transaction *sql.Tx, searchQuery SearchQuery) ([]NearbyHazard, error) {
	// TODO(C-103):
	// SELECT id, event_tag,
	//        ST_Y(location::geometry) AS latitude, ST_X(location::geometry) AS longitude,
	//        ST_Distance(location, projected) AS distance_meters,
	//        confidence, confirmations
	// FROM hazards,
	//      LATERAL (
	//        SELECT ST_Project(
	//                 ST_MakePoint(searchQuery.Longitude, searchQuery.Latitude)::geography,
	//                 LookaheadDistanceMeters,
	//                 radians(searchQuery.HeadingDegrees)
	//               ) AS projected
	//      ) projection
	// WHERE expires_at > now()
	//   AND ST_DWithin(location, projected, searchQuery.RadiusMeters)
	// ORDER BY distance_meters ASC
	return nil, sql.ErrNoRows
}
