// Package hazards implements the core business logic behind both hazard
// endpoints: dedup-on-write for POST /v1/hazards (this file) and the
// lookahead-projected nearby search for GET /v1/hazards (nearby_search.go).
package hazards

import (
	"database/sql"
	"time"
)

// Report is the input for a newly detected hazard, matching the JSON body
// documented in api_design.md Section 4.
type Report struct {
	EventTag    string
	Lat         float64
	Lng         float64
	HeadingDeg  float64
	Confidence  float64
	ReportedBy  string // device_id, set by the handler after authentication
}

// Outcome is what ProcessReport returns, matching the two possible response
// shapes documented in api_design.md Section 4 (201 "created" vs 200
// "confirmed").
type Outcome struct {
	ID            int64
	Status        string // "created" | "confirmed"
	Confirmations int
	ExpiresAt     time.Time
}

// dedupRadiusMeters is the distance within which a new report of the same
// event_tag is treated as confirming an existing hazard rather than a new
// one. Flat for all event_tags in MVP — see database_schema.md open item
// about whether accident vs pothole should differ.
const dedupRadiusMeters = 100

// ProcessReport implements the dedup-on-write logic from
// database_schema.md Section 4:
//  1. Look for an existing, non-expired hazard with the same event_tag
//     within dedupRadiusMeters.
//  2. If found: increment confirmations, reset expires_at, return it.
//  3. If not found: insert a new row with expires_at = now() + TTL(event_tag).
//
// location is intentionally NOT updated on confirmation — the original
// report's coordinates stay authoritative (see schema doc, Section 4).
func ProcessReport(tx *sql.Tx, r Report) (Outcome, error) {
	// TODO:
	// 1. SELECT id FROM hazards
	//    WHERE event_tag = r.EventTag
	//      AND expires_at > now()
	//      AND ST_DWithin(location, ST_MakePoint(r.Lng, r.Lat)::geography, dedupRadiusMeters)
	//    LIMIT 1
	// 2. If a row matched:
	//      UPDATE hazards
	//      SET confirmations = confirmations + 1,
	//          expires_at = now() + ttlFor(r.EventTag)
	//      WHERE id = $1
	//      RETURNING id, confirmations, expires_at
	//    -> Outcome{Status: "confirmed", ...}
	// 3. Else:
	//      INSERT INTO hazards (event_tag, location, heading_deg, confidence,
	//                            reported_by, expires_at)
	//      VALUES (..., now() + ttlFor(r.EventTag))
	//      RETURNING id, expires_at
	//    -> Outcome{Status: "created", Confirmations: 0, ...}
	return Outcome{}, sql.ErrNoRows
}

// ttlFor returns the lifetime to apply at insert/reset time for a given
// event_tag, per the TTL table in database_schema.md Section 4.
// Values here are illustrative placeholders pending field-testing data.
func ttlFor(eventTag string) time.Duration {
	switch eventTag {
	case "checkpost":
		return 4 * time.Hour
	case "accident":
		return 90 * time.Minute
	case "pothole", "debris":
		return 14 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}
