// Package hazards implements the core business logic behind both hazard
// endpoints: deduplication-on-write for POST /v1/hazards (this file) and the
// lookahead-projected nearby search for GET /v1/hazards (nearby_search.go).
package hazards

import (
	"database/sql"
	"errors"
	"time"
)

// errNotImplemented is what the placeholder functions in this package return
// until their task (C-102, C-103) writes them.
var errNotImplemented = errors.New("not implemented")

// Report is the input for a newly detected hazard, matching the JSON body of
// POST /v1/hazards (docs/design.md section 8; field names in section 12).
type Report struct {
	EventTag       string
	Latitude       float64
	Longitude      float64
	HeadingDegrees float64
	Confidence     float64
	ReportedBy     string // device_id, set by the handler after authentication
	// C-102 adds the report's source (derived from the device type) and makes
	// Confidence optional, because manual reports have none (design section 4).
}

// Outcome is what ProcessReport returns, matching the responses of
// POST /v1/hazards (docs/design.md section 8).
type Outcome struct {
	ID            int64
	Status        string // "created" | "confirmed" | "already_reported"
	Confirmations int
	ExpiresAt     time.Time
}

// ProcessReport implements deduplication-on-write. The rules are in
// docs/design.md section 6; in short:
//  1. Find the nearest live hazard with the same event_tag inside that tag's
//     deduplication radius.
//  2. None found: insert a new hazard (confirmations start at 1, and the
//     reporter gets a hazard_confirmations row) -> "created".
//  3. Found, and this device has not reported it before: add the device's
//     hazard_confirmations row, increment confirmations, reset expires_at
//     -> "confirmed".
//  4. Found, and this device already reported it: change nothing
//     -> "already_reported".
//
// location is intentionally NOT updated on confirmation — the original
// report's coordinates stay authoritative, to avoid drift across many reports.
func ProcessReport(transaction *sql.Tx, report Report) (Outcome, error) {
	// TODO(C-102): implement the steps above. The tag's radius and time to
	// live come from the event-tag registry (LookUpEventTag) and are passed to
	// the query as parameters.
	return Outcome{}, errNotImplemented
}
