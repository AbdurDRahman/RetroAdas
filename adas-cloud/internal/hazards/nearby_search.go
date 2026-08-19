package hazards

import "database/sql"

// SearchQuery matches the query params documented in api_design.md Section 5,
// Option A: the client sends raw state and the server does the lookahead
// projection itself via ST_Project.
type SearchQuery struct {
	Lat        float64
	Lng        float64
	HeadingDeg float64
	SpeedKmh   float64
	RadiusM    int
}

// LookaheadMeters is the fixed default distance ahead of the vehicle's
// position (along HeadingDeg) that gets queried, per api_design.md's design
// principle "projected lookahead, not current position". Could later be
// derived from SpeedKmh instead of being a constant.
const LookaheadMeters = 200

// NearbyHazard is one entry in the GET /v1/hazards response, matching
// api_design.md Section 5.
type NearbyHazard struct {
	ID            int64
	EventTag      string
	Lat, Lng      float64
	DistanceM     float64
	Confidence    float64
	Confirmations int
}

// FindNearby projects a point LookaheadMeters ahead of (q.Lat, q.Lng) along
// q.HeadingDeg, then returns all non-expired hazards within q.RadiusM of that
// projected point.
func FindNearby(tx *sql.Tx, q SearchQuery) ([]NearbyHazard, error) {
	// TODO:
	// SELECT id, event_tag,
	//        ST_Y(location::geometry) AS lat, ST_X(location::geometry) AS lng,
	//        ST_Distance(location, projected) AS distance_m,
	//        confidence, confirmations
	// FROM hazards,
	//      LATERAL (
	//        SELECT ST_Project(
	//                 ST_MakePoint(q.Lng, q.Lat)::geography,
	//                 LookaheadMeters,
	//                 radians(q.HeadingDeg)
	//               ) AS projected
	//      ) p
	// WHERE expires_at > now()
	//   AND ST_DWithin(location, projected, q.RadiusM)
	// ORDER BY distance_m ASC
	return nil, sql.ErrNoRows
}
