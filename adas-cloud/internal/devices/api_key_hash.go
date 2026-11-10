package devices

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HashAPIKey turns a raw API key into the value stored in devices.api_key_hash
// (docs/design.md section 7). Everything that stores or checks a key must go
// through this one function, so the two sides can never disagree: checking a
// request (Authenticate), the provisioning tool (C-107) and phone registration
// (C-114).
//
// The result is 64 hexadecimal characters: the 32 bytes of an HMAC-SHA256,
// written as text so it fits a TEXT column and is readable in a database shell.
func HashAPIKey(pepper string, rawAPIKey string) string {
	// The pepper is the HMAC's secret and the key is the message. Mixing the
	// pepper in this way (instead of hashing pepper+key glued together) is what
	// HMAC is designed for; a home-made combination can have subtle weaknesses.
	keyedHash := hmac.New(sha256.New, []byte(pepper))

	// Write on a hash never returns an error (the hash.Hash documentation
	// guarantees it), so there is nothing to check here.
	keyedHash.Write([]byte(rawAPIKey))

	// Sum(nil) returns the finished 32 bytes without appending them to anything.
	return hex.EncodeToString(keyedHash.Sum(nil))
}
