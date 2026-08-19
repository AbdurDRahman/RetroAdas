// Package devices handles authentication of incoming requests against the
// devices table (database_schema.md, Section 3). Every write endpoint
// (POST /v1/hazards, POST /v1/devices/handshake) and, per the API design's
// open item #2, potentially GET /v1/hazards too, goes through this package.
package devices

import (
	"database/sql"
	"errors"
)

// AuthResult describes the outcome of checking a device's credentials.
type AuthResult int

const (
	// AuthOK means the device_id exists, the API key hash matches, and the
	// device is not revoked.
	AuthOK AuthResult = iota

	// AuthUnknownDevice means no row exists for that device_id, or the key
	// hash doesn't match it. Maps to HTTP 401 per api_design.md.
	AuthUnknownDevice

	// AuthRevoked means the device_id exists and the key matches, but the
	// device has been revoked. Maps to HTTP 403 per api_design.md.
	AuthRevoked
)

// Authenticate checks a device_id + raw API key against the devices table.
// The raw key is hashed and compared against the stored api_key_hash —
// the raw key is never persisted or logged.
func Authenticate(tx *sql.Tx, deviceID string, rawAPIKey string) (AuthResult, error) {
	// TODO: hash rawAPIKey with the same algorithm used at provisioning time
	// (e.g. bcrypt or a keyed hash) before comparing.
	//
	// TODO: query:
	//   SELECT api_key_hash, revoked FROM devices WHERE device_id = $1
	// then:
	//   - no row                          -> AuthUnknownDevice
	//   - row found, hash mismatch        -> AuthUnknownDevice
	//   - row found, hash matches, revoked -> AuthRevoked
	//   - row found, hash matches, active  -> AuthOK
	return AuthUnknownDevice, errors.New("not implemented")
}
