// Package devices handles authentication of incoming requests against the
// devices table (docs/design.md, sections 7 and 9). Every request except
// registration and the health checks goes through this package, reads
// included.
package devices

import (
	"crypto/hmac"
	"database/sql"
	"errors"
	"fmt"
)

// fakeStoredHash stands in for the stored hash when the device ID doesn't
// exist, so that an unknown device goes through exactly the same hashing and
// comparing as a known one (docs/design.md section 7). It has the same length
// as a real hash (64 characters) so the comparison does the same work.
const fakeStoredHash = "0000000000000000000000000000000000000000000000000000000000000000"

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
//
// It returns the device's type (jetson_unit or mobile_app) only when the
// result is AuthenticationOK; for every other result the type is empty.
func Authenticate(transaction *sql.Tx, pepper string, deviceID string, rawAPIKey string) (AuthenticationResult, string, error) {
	var storedHash, deviceType string
	var revoked bool
	err := transaction.QueryRow(
		`SELECT api_key_hash, device_type, revoked FROM devices WHERE device_id = $1`,
		deviceID,
	).Scan(&storedHash, &deviceType, &revoked)

	// "No such device" is an expected answer, not a failure. Instead of
	// returning early, carry on with a fake hash: the steps below then run for
	// every request, so the response time doesn't reveal which IDs exist.
	deviceExists := true
	if errors.Is(err, sql.ErrNoRows) {
		deviceExists = false
		storedHash = fakeStoredHash
		err = nil
	}
	// Anything else (connection lost, query cancelled) is a real failure. The
	// caller must not treat it as "unknown device": that would turn a database
	// problem into a wave of 401 responses.
	if err != nil {
		return AuthenticationUnknownDevice, "", fmt.Errorf("look up device: %w", err)
	}

	// hmac.Equal takes the same time wherever the first difference is, so the
	// response time doesn't reveal how much of a guessed key was right.
	presentedHash := HashAPIKey(pepper, rawAPIKey)
	keyMatches := hmac.Equal([]byte(presentedHash), []byte(storedHash))

	// One answer for "no such device" and "wrong key", on purpose.
	if !deviceExists || !keyMatches {
		return AuthenticationUnknownDevice, "", nil
	}
	// Checked only after the key matched, so only the key's owner ever learns
	// that a device is revoked.
	if revoked {
		return AuthenticationRevoked, "", nil
	}
	return AuthenticationOK, deviceType, nil
}
