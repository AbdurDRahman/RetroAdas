// Package hazards implements the core business logic behind both hazard
// endpoints: deduplication-on-write for POST /v1/hazards (this file) and the
// lookahead-projected nearby search for GET /v1/hazards (nearby_search.go).
package hazards

import (
	"database/sql"
	"time"
)

// Report is the input for a newly detected hazard, matching the JSON body of
// POST /v1/hazards (docs/design.md section 8; field names in section 12).
type Report struct {
	EventTag       string
	Latitude       float64
	Longitude      float64
	HeadingDegrees float64
	Confidence     float64
	ReportedBy     string // device_id, set by the handler after authentication
}

// Outcome is what ProcessReport returns, matching the two possible responses
// of POST /v1/hazards: 201 "created" vs 200 "confirmed".
type Outcome struct {
	ID            int64
	Status        string // "created" | "confirmed"
	Confirmations int
	ExpiresAt     time.Time
}

// deduplicationRadiusMeters is the distance within which a new report of the
// same event_tag is treated as confirming an existing hazard rather than a new
// one. Flat for all event_tags for now — C-102 moves it into the per-tag
// event-tag registry (docs/design.md section 4).
const deduplicationRadiusMeters = 100

// ProcessReport implements deduplication-on-write (docs/design.md section 6):
//  1. Look for an existing, non-expired hazard with the same event_tag
//     within deduplicationRadiusMeters.
//  2. If found: increment confirmations, reset expires_at, return it.
//  3. If not found: insert a new row with expires_at = now() + time to live.
//
// location is intentionally NOT updated on confirmation — the original
// report's coordinates stay authoritative, to avoid drift across many reports.
func ProcessReport(transaction *sql.Tx, report Report) (Outcome, error) {
	// TODO(C-102):
	// 1. SELECT id FROM hazards
	//    WHERE event_tag = report.EventTag
	//      AND expires_at > now()
	//      AND ST_DWithin(location, ST_MakePoint(report.Longitude, report.Latitude)::geography, deduplicationRadiusMeters)
	//    LIMIT 1
	//    (C-102 also adds: nearest match first, and one confirmation per device.)
	// 2. If a row matched:
	//      UPDATE hazards
	//      SET confirmations = confirmations + 1,
	//          expires_at = now() + timeToLiveFor(report.EventTag)
	//      WHERE id = $1
	//      RETURNING id, confirmations, expires_at
	//    -> Outcome{Status: "confirmed", ...}
	// 3. Else:
	//      INSERT INTO hazards (event_tag, location, heading_degrees, confidence,
	//                            reported_by, expires_at)
	//      VALUES (..., now() + timeToLiveFor(report.EventTag))
	//      RETURNING id, expires_at
	//    -> Outcome{Status: "created", Confirmations: 0, ...}
	return Outcome{}, sql.ErrNoRows
}

// timeToLiveFor returns the lifetime to apply at insert/confirmation time for
// a given event_tag. Values are illustrative placeholders pending
// field-testing data; C-102 moves them into the event-tag registry
// (docs/design.md section 4).
func timeToLiveFor(eventTag string) time.Duration {
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
