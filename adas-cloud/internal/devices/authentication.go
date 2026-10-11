// Package devices handles authentication of incoming requests against the
// devices table (docs/design.md, sections 7 and 9). Every write endpoint
// (POST /v1/hazards, POST /v1/devices/handshake) and, depending on the open
// decision in docs/design.md section 7, possibly GET /v1/hazards too, goes
// through this package.
package devices

import (
	"database/sql"
	"errors"
)

// AuthenticationResult describes the outcome of checking a device's
// credentials.
type AuthenticationResult int

const (
	// AuthenticationOK means the device_id exists, the API key hash matches,
	// and the device is not revoked.
	AuthenticationOK AuthenticationResult = iota

	// AuthenticationUnknownDevice means no row exists for that device_id, or
	// the key hash doesn't match it. Maps to HTTP 401. Both cases share one
	// result on purpose: telling a caller "this device exists, only the key is
	// wrong" would let an attacker discover valid device IDs.
	AuthenticationUnknownDevice

	// AuthenticationRevoked means the device_id exists and the key matches,
	// but the device has been revoked. Maps to HTTP 403.
	AuthenticationRevoked
)

// Authenticate checks a device_id + raw API key against the devices table.
// The raw key is hashed and compared against the stored api_key_hash —
// the raw key is never persisted or logged.
func Authenticate(transaction *sql.Tx, deviceID string, rawAPIKey string) (AuthenticationResult, error) {
	// TODO(C-101): hash rawAPIKey with the same algorithm used at provisioning
	// time (bcrypt vs a keyed hash is decided in C-101) before comparing.
	//
	// TODO(C-101): query:
	//   SELECT api_key_hash, revoked FROM devices WHERE device_id = $1
	// then:
	//   - no row                           -> AuthenticationUnknownDevice
	//   - row found, hash mismatch         -> AuthenticationUnknownDevice
	//   - row found, hash matches, revoked -> AuthenticationRevoked
	//   - row found, hash matches, active  -> AuthenticationOK
	return AuthenticationUnknownDevice, errors.New("not implemented")
}
