package devices_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
)

// TestAuthenticate covers every outcome of checking a request's device ID and
// API key (docs/design.md section 7).
func TestAuthenticate(test *testing.T) {
	testCases := []struct {
		name           string
		deviceToSeed   *seededDevice // nil = no device row inserted
		deviceID       string
		apiKey         string
		expectedResult devices.AuthenticationResult
		// The device type comes back only for AuthenticationOK. For every other
		// result it must be empty, so a caller can't act on a rejected device.
		expectedDeviceType string
	}{
		{
			name:           "unknown device_id",
			deviceToSeed:   nil,
			deviceID:       "jetson-00000000",
			apiKey:         "irrelevant",
			expectedResult: devices.AuthenticationUnknownDevice,
		},
		{
			name:           "known device, wrong api key",
			deviceToSeed:   &seededDevice{DeviceID: "jetson-4f9c2a1b", APIKey: "correct-key", DeviceType: "jetson_unit"},
			deviceID:       "jetson-4f9c2a1b",
			apiKey:         "wrong-key",
			expectedResult: devices.AuthenticationUnknownDevice,
		},
		{
			name:           "known device, correct key, revoked",
			deviceToSeed:   &seededDevice{DeviceID: "jetson-4f9c2a1b", APIKey: "correct-key", DeviceType: "jetson_unit", Revoked: true},
			deviceID:       "jetson-4f9c2a1b",
			apiKey:         "correct-key",
			expectedResult: devices.AuthenticationRevoked,
		},
		{
			// "Revoked" must only be reachable with the correct key. Otherwise
			// anyone could learn that a device ID exists just by trying it.
			name:           "revoked device, wrong api key -> looks unknown, not revoked",
			deviceToSeed:   &seededDevice{DeviceID: "jetson-4f9c2a1b", APIKey: "correct-key", DeviceType: "jetson_unit", Revoked: true},
			deviceID:       "jetson-4f9c2a1b",
			apiKey:         "wrong-key",
			expectedResult: devices.AuthenticationUnknownDevice,
		},
		{
			name:               "Jetson unit, correct key, active",
			deviceToSeed:       &seededDevice{DeviceID: "jetson-4f9c2a1b", APIKey: "correct-key", DeviceType: "jetson_unit"},
			deviceID:           "jetson-4f9c2a1b",
			apiKey:             "correct-key",
			expectedResult:     devices.AuthenticationOK,
			expectedDeviceType: "jetson_unit",
		},
		{
			name:               "phone, correct key, active",
			deviceToSeed:       &seededDevice{DeviceID: "phone-91d0e7c3", APIKey: "correct-key", DeviceType: "mobile_app"},
			deviceID:           "phone-91d0e7c3",
			apiKey:             "correct-key",
			expectedResult:     devices.AuthenticationOK,
			expectedDeviceType: "mobile_app",
		},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)

			if testCase.deviceToSeed != nil {
				seedDevice(test, transaction, *testCase.deviceToSeed)
			}

			actualResult, actualDeviceType, err := devices.Authenticate(transaction, testPepper, testCase.deviceID, testCase.apiKey)
			require.NoError(test, err)
			assert.Equal(test, testCase.expectedResult, actualResult)
			assert.Equal(test, testCase.expectedDeviceType, actualDeviceType)
		})
	}
}

// --- test fixtures ---

type seededDevice struct {
	DeviceID   string
	APIKey     string // the raw key; seedDevice stores only its hash
	DeviceType string
	Revoked    bool
}

// seedDevice inserts a device row directly for test setup. It hashes APIKey
// with the same function and pepper the code under test uses, so tests
// exercise the real comparison instead of bypassing it.
//
// It takes the same *sql.Tx the test passes to devices.Authenticate, so the
// seeded row is visible to the code under test and is rolled back with
// everything else when the test ends.
func seedDevice(test *testing.T, transaction *sql.Tx, device seededDevice) {
	test.Helper()

	_, err := transaction.Exec(
		`INSERT INTO devices (device_id, api_key_hash, device_type, revoked)
		 VALUES ($1, $2, $3, $4)`,
		device.DeviceID, devices.HashAPIKey(testPepper, device.APIKey), device.DeviceType, device.Revoked,
	)
	require.NoError(test, err)
}
