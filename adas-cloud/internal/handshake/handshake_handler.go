// Package handshake implements POST /v1/devices/handshake — the once-at-boot
// identity check (docs/design.md sections 7 and 8). Build this after
// internal/devices: it's thin compared to the hazards package.
package handshake

import (
	"database/sql"
	"errors"
)

// Response is the JSON body of a successful handshake. The agreed design
// returns only DeviceID and DeviceType; the three polling fields below are
// outdated and C-104 removes them (polling values come back on every
// GET /v1/hazards response instead).
type Response struct {
	DeviceID                string `json:"device_id"`
	DeviceType              string `json:"device_type,omitempty"`
	PollDistanceMeters      int    `json:"poll_distance_meters,omitempty"`
	LookaheadDistanceMeters int    `json:"lookahead_distance_meters,omitempty"`
	SearchRadiusMeters      int    `json:"search_radius_meters,omitempty"`
}

// Handle authenticates the device (via internal/devices) and returns its
// identity. Wire this up once internal/devices.Authenticate is green.
func Handle(transaction *sql.Tx, deviceID string) (Response, error) {
	// TODO(C-104): take the API key as well, call devices.Authenticate
	// (401/403 on failure), then return the device's ID and type.
	return Response{}, errors.New("not implemented")
}
