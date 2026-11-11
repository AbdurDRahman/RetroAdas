// Package hazards implements the core business logic behind both hazard
// endpoints: deduplication-on-write for POST /v1/hazards (this file) and the
// lookahead-projected nearby search for GET /v1/hazards (nearby_search.go).
package hazards

import (
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"
)

// errNotImplemented is what the placeholder functions in this package return
// until their task (C-102, C-103) writes them.
var errNotImplemented = errors.New("not implemented")

// The reasons ProcessReport refuses a report. They are named values so the
// HTTP handler (C-105) can tell them apart with errors.Is and answer with the
// right status code, as agreed in docs/design.md section 8.
var (
	// ErrUnknownEventTag: the tag is not in the registry. HTTP 400.
	ErrUnknownEventTag = errors.New("unknown event tag")

	// ErrEventTagNotAllowed: the tag exists, but this type of device may not
	// report it (for example a Jetson unit reporting a checkpost). HTTP 403.
	ErrEventTagNotAllowed = errors.New("event tag not allowed for this device type")

	// ErrInvalidConfidence: a detector's report without a confidence between 0
	// and 1. HTTP 400.
	ErrInvalidConfidence = errors.New("an auto report needs a confidence between 0 and 1")
)

// MaximumSameDirectionHeadingDifferenceDegrees is the direction rule's
// threshold (docs/design.md section 6): two headings further apart than this
// count as opposite directions. Oncoming traffic differs by about 180 degrees
// and a phone compass can be off by 20 to 30, so 135 leaves a margin on both
// sides, and a car joining from a side road (about 90) still counts as the
// same direction.
const MaximumSameDirectionHeadingDifferenceDegrees = 135

// The three things that can happen to an accepted report (design section 8).
const (
	StatusCreated         = "created"
	StatusConfirmed       = "confirmed"
	StatusAlreadyReported = "already_reported"
)

// Report is one device's report of a hazard: the JSON body of POST /v1/hazards
// (docs/design.md section 8) plus who sent it.
type Report struct {
	EventTag  string
	Latitude  float64
	Longitude float64

	// HeadingDegrees is optional: someone on foot has no meaningful heading.
	// A pointer so that "not sent" (nil) is different from "heading 0" (north).
	HeadingDegrees *float64

	// Confidence is the detector's score. Required for a detector's report;
	// ignored for a person's report, which stores none (design section 4).
	Confidence *float64

	// ReportedBy and DeviceType come from authentication, never from the
	// request body: a client can't choose who it is or what kind of device it is.
	ReportedBy string
	DeviceType string
}

// Outcome is what ProcessReport returns for an accepted report, matching the
// response of POST /v1/hazards (docs/design.md section 8).
type Outcome struct {
	ID            int64
	Status        string // StatusCreated | StatusConfirmed | StatusAlreadyReported
	Confirmations int
	ExpiresAt     time.Time
}

// ProcessReport implements deduplication-on-write. The rules are in
// docs/design.md section 6; in short:
//  0. Refuse a report with an unknown tag, a tag this device type may not
//     report, or (for a detector) no valid confidence.
//  1. Find the nearest live hazard with the same event_tag inside that tag's
//     deduplication radius, travelling the same way when direction matters.
//  2. None found: insert a new hazard (confirmations start at 1, and the
//     reporter gets a hazard_confirmations row) -> StatusCreated.
//  3. Found, and this device has not reported it before: add the device's
//     hazard_confirmations row, increment confirmations, reset expires_at
//     -> StatusConfirmed.
//  4. Found, and this device already reported it: change nothing
//     -> StatusAlreadyReported.
//
// location is intentionally NOT updated on confirmation — the original
// report's coordinates stay authoritative, to avoid drift across many reports.
func ProcessReport(transaction *sql.Tx, report Report) (Outcome, error) {
	// Step 0 comes before any SQL, so a refused report can't store anything.
	eventTag, source, err := checkReport(report)
	if err != nil {
		return Outcome{}, err
	}

	// From here until the caller's transaction ends, this is the only report of
	// this tag being processed, on any server. Without it, two reports of the
	// same new hazard could both look, both find nothing, and both create it.
	if err := waitForOtherReportsOfTag(transaction, eventTag); err != nil {
		return Outcome{}, err
	}

	// Step 1: is this a hazard we already know?
	matchedHazardID, found, err := findMatchingHazard(transaction, report, eventTag)
	if err != nil {
		return Outcome{}, err
	}

	// Step 2: nothing matched, so this report becomes a new hazard.
	if !found {
		return createHazard(transaction, report, eventTag, source)
	}

	// Steps 3 and 4: it matched, so confirm it (or recognize a repeat).
	return confirmHazard(transaction, matchedHazardID, report, eventTag)
}

// waitForOtherReportsOfTag takes a Postgres advisory lock for the tag (task
// C-116). An advisory lock is a named lock that Postgres keeps for us; it
// locks no table and no row, so searches are never slowed by it. If another
// transaction holds the same lock, this call simply waits until that
// transaction ends, and then the search in step 1 sees what it saved.
//
// "xact" means the lock belongs to the transaction: Postgres releases it
// automatically on commit or rollback, so it can never be left behind.
//
// The caller must keep its transaction short (one report, then commit),
// because every other report of this tag waits for it.
func waitForOtherReportsOfTag(transaction *sql.Tx, eventTag EventTag) error {
	_, err := transaction.Exec(`SELECT pg_advisory_xact_lock($1)`, advisoryLockKeyForTag(eventTag))
	if err != nil {
		return fmt.Errorf("wait for other reports of %q: %w", eventTag.Name, err)
	}
	return nil
}

// advisoryLockKeyForTag turns a tag name into the number Postgres identifies
// the lock by: the same tag always gives the same number. The "hazard-report:"
// prefix keeps these numbers apart from locks taken for other purposes (the
// migration runner has one too).
func advisoryLockKeyForTag(eventTag EventTag) int64 {
	hasher := fnv.New64a()
	hasher.Write([]byte("hazard-report:" + eventTag.Name))
	return int64(hasher.Sum64())
}

// findMatchingHazard is step 1: it returns the id of the nearest live hazard
// with the same tag inside that tag's deduplication radius, travelling the same
// way when direction matters for the tag. The second value is false when there
// is none.
func findMatchingHazard(transaction *sql.Tx, report Report, eventTag EventTag) (int64, bool, error) {
	// - expires_at > now(): an expired hazard is never matched.
	// - ST_DWithin: within the tag's radius, in meters. It uses the GiST index
	//   on location, so it stays fast with many hazards.
	// - The direction rule is the bracketed block. A hazard passes it when ANY
	//   of its four lines is true:
	//     1. direction doesn't matter for this tag ($5 is false);
	//     2. the report has no heading ($6 is empty);
	//     3. the hazard has no heading;
	//     4. the two headings are at most $7 degrees apart.
	//   "180 - abs(abs(a - b) - 180)" is the difference the short way around
	//   the compass, always between 0 and 180: for 350 and 10 it gives 20, where
	//   plain subtraction would give 340.
	// - ORDER BY distance + LIMIT 1: of the hazards that passed every rule, the
	//   nearest wins. Direction is checked BEFORE picking the nearest, so a
	//   closer hazard going the other way can't hide the right one.
	var hazardID int64
	err := transaction.QueryRow(
		`SELECT id
		 FROM hazards
		 WHERE event_tag = $1
		   AND expires_at > now()
		   AND ST_DWithin(location, ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography, $4)
		   AND (
		        NOT $5::boolean
		     OR $6::float8 IS NULL
		     OR heading_degrees IS NULL
		     OR 180 - abs(abs(heading_degrees - $6::float8) - 180) <= $7::float8
		   )
		 ORDER BY ST_Distance(location, ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)
		 LIMIT 1`,
		report.EventTag, report.Longitude, report.Latitude, eventTag.DeduplicationRadiusMeters,
		eventTag.DirectionMatters, report.HeadingDegrees, MaximumSameDirectionHeadingDifferenceDegrees,
	).Scan(&hazardID)

	// "No row" is the normal answer for a new hazard, not a failure.
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("look for a matching hazard: %w", err)
	}
	return hazardID, true, nil
}

// confirmHazard is steps 3 and 4: the report matched an existing hazard. A
// device that hasn't reported it before confirms it; a device that has changes
// nothing.
func confirmHazard(transaction *sql.Tx, hazardID int64, report Report, eventTag EventTag) (Outcome, error) {
	// Try to record this device for this hazard. The table's primary key is
	// (hazard_id, device_id), so if the device is already there the insert
	// conflicts, and DO NOTHING turns that into "0 rows inserted" instead of an
	// error. The database itself decides "new or repeat" in one statement: no
	// "check first, then insert" gap for two requests to slip through.
	insertResult, err := transaction.Exec(
		`INSERT INTO hazard_confirmations (hazard_id, device_id) VALUES ($1, $2)
		 ON CONFLICT (hazard_id, device_id) DO NOTHING`,
		hazardID, report.ReportedBy,
	)
	if err != nil {
		return Outcome{}, fmt.Errorf("record the confirmation: %w", err)
	}
	insertedRowCount, err := insertResult.RowsAffected()
	if err != nil {
		return Outcome{}, fmt.Errorf("record the confirmation: %w", err)
	}

	outcome := Outcome{ID: hazardID}

	// Step 4: this device already reported it. Read the hazard as it is and
	// change nothing — no extra confirmation, no expiry reset.
	if insertedRowCount == 0 {
		outcome.Status = StatusAlreadyReported
		err = transaction.QueryRow(
			`SELECT confirmations, expires_at FROM hazards WHERE id = $1`,
			hazardID,
		).Scan(&outcome.Confirmations, &outcome.ExpiresAt)
		if err != nil {
			return Outcome{}, fmt.Errorf("read the hazard: %w", err)
		}
		return outcome, nil
	}

	// Step 3: a new device. Count it and give the hazard a full time to live
	// again. "confirmations + 1" is computed by the database, so two
	// confirmations at the same moment can't overwrite each other. location,
	// confidence and reported_by are deliberately left alone.
	outcome.Status = StatusConfirmed
	err = transaction.QueryRow(
		`UPDATE hazards
		 SET confirmations = confirmations + 1,
		     expires_at = now() + make_interval(secs => $2)
		 WHERE id = $1
		 RETURNING confirmations, expires_at`,
		hazardID, eventTag.TimeToLive.Seconds(),
	).Scan(&outcome.Confirmations, &outcome.ExpiresAt)
	if err != nil {
		return Outcome{}, fmt.Errorf("confirm the hazard: %w", err)
	}
	return outcome, nil
}

// createHazard is step 2: it stores a new hazard and records its reporter in
// hazard_confirmations. Both inserts run in the caller's transaction, so
// either both are kept or neither is.
func createHazard(transaction *sql.Tx, report Report, eventTag EventTag, source string) (Outcome, error) {
	// A person's report stores no confidence, whatever the phone sent.
	confidence := report.Confidence
	if source == SourceManual {
		confidence = nil
	}

	outcome := Outcome{Status: StatusCreated}
	// - PostGIS points are (longitude, latitude), in that order.
	// - 4326 names the coordinate system GPS uses (WGS 84).
	// - The time to live is passed in seconds and added to the database's own
	//   clock, so every replica agrees on "now".
	// - confirmations is not listed: the table starts it at 1.
	// - RETURNING hands back what the database filled in, saving a second query.
	err := transaction.QueryRow(
		`INSERT INTO hazards (event_tag, location, heading_degrees, confidence, source, reported_by, expires_at)
		 VALUES ($1, ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography, $4, $5, $6, $7,
		         now() + make_interval(secs => $8))
		 RETURNING id, confirmations, expires_at`,
		report.EventTag, report.Longitude, report.Latitude, report.HeadingDegrees, confidence,
		source, report.ReportedBy, eventTag.TimeToLive.Seconds(),
	).Scan(&outcome.ID, &outcome.Confirmations, &outcome.ExpiresAt)
	if err != nil {
		return Outcome{}, fmt.Errorf("insert hazard: %w", err)
	}

	// The first reporter gets a row too, so its own repeat is recognized later.
	_, err = transaction.Exec(
		`INSERT INTO hazard_confirmations (hazard_id, device_id) VALUES ($1, $2)`,
		outcome.ID, report.ReportedBy,
	)
	if err != nil {
		return Outcome{}, fmt.Errorf("record the reporter: %w", err)
	}

	return outcome, nil
}

// checkReport is step 0: it refuses a report that breaks a rule, and otherwise
// returns what the later steps need — the tag's rules from the registry and
// the report's source (auto or manual).
func checkReport(report Report) (EventTag, string, error) {
	eventTag, known := LookUpEventTag(report.EventTag)
	if !known {
		// %w keeps the named error findable with errors.Is, while the message
		// also says which tag it was (useful in a log or an error response).
		return EventTag{}, "", fmt.Errorf("%w: %q", ErrUnknownEventTag, report.EventTag)
	}
	if !eventTag.AllowsDeviceType(report.DeviceType) {
		return EventTag{}, "", fmt.Errorf("%w: %q from a %q device", ErrEventTagNotAllowed, report.EventTag, report.DeviceType)
	}

	source, known := SourceForDeviceType(report.DeviceType)
	if !known {
		// Can't happen for a tag that allowed this device type, because the
		// registry only lists known types. If it ever does, it is our own bug,
		// not the client's, so it is not one of the named refusals.
		return EventTag{}, "", fmt.Errorf("device type %q has no source", report.DeviceType)
	}

	// Only a detector's report carries a score. A person's report may arrive
	// with or without one; either way none is stored.
	if source == SourceAuto && !isValidConfidence(report.Confidence) {
		return EventTag{}, "", ErrInvalidConfidence
	}

	return eventTag, source, nil
}

// isValidConfidence reports whether a detector's confidence is present and
// between 0 and 1, both included.
func isValidConfidence(confidence *float64) bool {
	return confidence != nil && *confidence >= 0 && *confidence <= 1
}
