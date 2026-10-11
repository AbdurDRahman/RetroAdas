// Package handshake implements POST /v1/devices/handshake — the once-at-boot
// check-in (docs/design.md sections 7 and 8). Build this after
// internal/devices: it's thin (credential check + configuration echo)
// compared to the hazards package.
package handshake

import "database/sql"

// Response is the JSON body of a successful handshake. A bare 200 OK with just
// DeviceID is enough for MVP — the rest are optional tunables. Which tunables
// it carries is settled in C-104: with the speed-based polling rule
// (docs/design.md section 5), the per-poll values come back on every
// GET /v1/hazards response instead, so some of these may be dropped.
type Response struct {
	DeviceID                string `json:"device_id"`
	DeviceType              string `json:"device_type,omitempty"`
	PollDistanceMeters      int    `json:"poll_distance_meters,omitempty"`
	LookaheadDistanceMeters int    `json:"lookahead_distance_meters,omitempty"`
	SearchRadiusMeters      int    `json:"search_radius_meters,omitempty"`
}

// Handle authenticates the device (via internal/devices) and returns its
// session configuration. Wire this up once internal/devices.Authenticate is
// green.
func Handle(transaction *sql.Tx, deviceID string) (Response, error) {
	// TODO(C-104): call devices.Authenticate first (401/403 on failure), then
	// build Response. Configuration values can be constants for MVP.
	return Response{}, sql.ErrNoRows
}
