package devices_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/YOUR-ORG/adas-cloud/internal/devices"
	testdb "github.com/YOUR-ORG/adas-cloud/database/testing"
)

// TestAuthenticate is the starting point for the TDD loop on this package.
// Run it now — it should fail (Authenticate is not implemented yet). Make it
// pass by filling in devices.Authenticate, not by changing this test.
func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name       string
		seedDevice *seededDevice // nil = no device row inserted
		deviceID   string
		apiKey     string
		want       devices.AuthResult
	}{
		{
			name:       "unknown device_id",
			seedDevice: nil,
			deviceID:   "veh-does-not-exist",
			apiKey:     "irrelevant",
			want:       devices.AuthUnknownDevice,
		},
		{
			name:       "known device, wrong api key",
			seedDevice: &seededDevice{DeviceID: "veh-0427", APIKey: "correct-key", Revoked: false},
			deviceID:   "veh-0427",
			apiKey:     "wrong-key",
			want:       devices.AuthUnknownDevice,
		},
		{
			name:       "known device, correct key, revoked",
			seedDevice: &seededDevice{DeviceID: "veh-0427", APIKey: "correct-key", Revoked: true},
			deviceID:   "veh-0427",
			apiKey:     "correct-key",
			want:       devices.AuthRevoked,
		},
		{
			name:       "known device, correct key, active",
			seedDevice: &seededDevice{DeviceID: "veh-0427", APIKey: "correct-key", Revoked: false},
			deviceID:   "veh-0427",
			apiKey:     "correct-key",
			want:       devices.AuthOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := testdb.NewTransaction(t)

			if tt.seedDevice != nil {
				seedDevice(t, tx, *tt.seedDevice)
			}

			got, err := devices.Authenticate(tx, tt.deviceID, tt.apiKey)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
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
func seedDevice(t *testing.T, tx interface {
	Exec(query string, args ...any) (any, error)
}, d seededDevice) {
	t.Helper()
	// TODO: implement once Authenticate's hashing approach is decided —
	// insert into devices(device_id, api_key_hash, device_type, revoked)
	// using the same hash function Authenticate() uses for comparison.
	t.Skip("seedDevice not implemented yet — fill in alongside devices.Authenticate")
}
