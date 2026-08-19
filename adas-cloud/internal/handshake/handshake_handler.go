// Package handshake implements POST /v1/devices/handshake — the
// once-at-boot check-in described in api_design.md Section 3. Build this
// last: it's thin (credential check + optional config echo) compared to the
// hazards package.
package handshake

import "database/sql"

// Response matches the JSON shape in api_design.md Section 3. Per that doc,
// a bare 200 OK with just DeviceID is enough for MVP — the rest are
// optional tunables.
type Response struct {
	DeviceID               string `json:"device_id"`
	DeviceType             string `json:"device_type,omitempty"`
	PollIntervalDistanceM  int    `json:"poll_interval_distance_m,omitempty"`
	LookaheadM             int    `json:"lookahead_m,omitempty"`
	HazardRadiusM          int    `json:"hazard_radius_m,omitempty"`
}

// Handle authenticates the device (via internal/devices) and returns its
// session config. Wire this up once internal/devices.Authenticate is green.
func Handle(tx *sql.Tx, deviceID string) (Response, error) {
	// TODO: call devices.Authenticate first (401/403 on failure), then
	// build Response. Config values (poll distance, lookahead, radius) can
	// be hardcoded constants for MVP per the open item in api_design.md.
	return Response{}, sql.ErrNoRows
}
