package devices_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
)

// TestAuthenticate is the starting point for the TDD loop on this package.
// Run it now — it should fail (Authenticate is not implemented yet). Make it
// pass by filling in devices.Authenticate, not by changing this test.
func TestAuthenticate(test *testing.T) {
	testCases := []struct {
		name           string
		deviceToSeed   *seededDevice // nil = no device row inserted
		deviceID       string
		apiKey         string
		expectedResult devices.AuthenticationResult
	}{
		{
			name:           "unknown device_id",
			deviceToSeed:   nil,
			deviceID:       "vehicle-does-not-exist",
			apiKey:         "irrelevant",
			expectedResult: devices.AuthenticationUnknownDevice,
		},
		{
			name:           "known device, wrong api key",
			deviceToSeed:   &seededDevice{DeviceID: "vehicle-0427", APIKey: "correct-key", Revoked: false},
			deviceID:       "vehicle-0427",
			apiKey:         "wrong-key",
			expectedResult: devices.AuthenticationUnknownDevice,
		},
		{
			name:           "known device, correct key, revoked",
			deviceToSeed:   &seededDevice{DeviceID: "vehicle-0427", APIKey: "correct-key", Revoked: true},
			deviceID:       "vehicle-0427",
			apiKey:         "correct-key",
			expectedResult: devices.AuthenticationRevoked,
		},
		{
			name:           "known device, correct key, active",
			deviceToSeed:   &seededDevice{DeviceID: "vehicle-0427", APIKey: "correct-key", Revoked: false},
			deviceID:       "vehicle-0427",
			apiKey:         "correct-key",
			expectedResult: devices.AuthenticationOK,
		},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)

			if testCase.deviceToSeed != nil {
				seedDevice(test, transaction, *testCase.deviceToSeed)
			}

			actualResult, err := devices.Authenticate(transaction, testCase.deviceID, testCase.apiKey)
			require.NoError(test, err)
			require.Equal(test, testCase.expectedResult, actualResult)
		})
	}
}

// --- test fixtures ---

type seededDevice struct {
	DeviceID string
	APIKey   string
	Revoked  bool
}

// seedDevice inserts a device row directly for test setup. It hashes APIKey
// the same way production code will, so tests exercise the real comparison
// path rather than bypassing it.
//
// It takes the same *sql.Tx the test passes to devices.Authenticate, so the
// seeded row is visible to the code under test and is rolled back with
// everything else when the test ends.
func seedDevice(test *testing.T, transaction *sql.Tx, device seededDevice) {
	test.Helper()
	// TODO(C-101): implement once Authenticate's hashing approach is decided —
	// insert into devices(device_id, api_key_hash, device_type, revoked)
	// using the same hash function Authenticate() uses for comparison.
	test.Skip("seedDevice not implemented yet — fill in alongside devices.Authenticate")
}
