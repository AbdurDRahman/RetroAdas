package devices_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
)

// A fixed pepper for tests only. The real one comes from API_KEY_PEPPER and is
// never written in a file that goes to git. Passing it in directly means the
// tests need no environment variable.
const testPepper = "test-pepper-not-a-real-secret-0123456789"

func TestHashAPIKey(test *testing.T) {
	test.Run("matches a reference value computed outside this program", func(test *testing.T) {
		// Computed independently with:
		//   printf 'correct-key' | openssl dgst -sha256 -hmac 'test-pepper-not-a-real-secret-0123456789'
		// This is what proves the function really is HMAC-SHA256 with the
		// pepper as its secret, and not, say, a plain SHA-256 of the key.
		expectedHash := "f7d37998d0603eebe51813e8ab1eb3d0554ebbf4d54a9f39bc62be22c9ee6128"

		actualHash := devices.HashAPIKey(testPepper, "correct-key")

		assert.Equal(test, expectedHash, actualHash)
	})

	test.Run("a different pepper gives a different hash", func(test *testing.T) {
		// The point of the pepper: hashes from a leaked database can't be
		// checked by someone who doesn't also have the pepper.
		hashWithTestPepper := devices.HashAPIKey(testPepper, "correct-key")
		hashWithOtherPepper := devices.HashAPIKey("some-other-pepper", "correct-key")

		assert.NotEqual(test, hashWithTestPepper, hashWithOtherPepper)
	})

	test.Run("a different key gives a different hash", func(test *testing.T) {
		hashOfCorrectKey := devices.HashAPIKey(testPepper, "correct-key")
		hashOfWrongKey := devices.HashAPIKey(testPepper, "wrong-key")

		assert.NotEqual(test, hashOfCorrectKey, hashOfWrongKey)
	})
}
