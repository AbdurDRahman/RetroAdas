package database_test

import (
	"errors"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
)

// The devices table itself must refuse a device type the design doesn't know
// (docs/design.md section 9). The type decides what a device may report, so a
// typing mistake in a tool or a query must fail loudly at the insert, not
// create a device that silently matches no rule.
func TestDevicesTableOnlyAcceptsKnownDeviceTypes(test *testing.T) {
	testCases := []struct {
		name             string
		deviceType       string
		expectedAccepted bool
	}{
		{name: "jetson_unit is accepted", deviceType: "jetson_unit", expectedAccepted: true},
		{name: "mobile_app is accepted", deviceType: "mobile_app", expectedAccepted: true},
		{name: "an unknown type is rejected", deviceType: "spaceship", expectedAccepted: false},
		{name: "wrong capital letters are rejected", deviceType: "Jetson_Unit", expectedAccepted: false},
		{name: "an empty type is rejected", deviceType: "", expectedAccepted: false},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)

			_, err := transaction.Exec(
				`INSERT INTO devices (device_id, api_key_hash, device_type) VALUES ($1, $2, $3)`,
				"jetson-4f9c2a1b", "not-a-real-hash", testCase.deviceType,
			)

			if testCase.expectedAccepted {
				require.NoError(test, err)
				return
			}

			// Rejected for the right reason: by this exact rule, not by some
			// unrelated problem with the insert. lib/pq reports which
			// constraint refused the row.
			var databaseError *pq.Error
			require.True(test, errors.As(err, &databaseError), "expected a database error, got: %v", err)
			assert.Equal(test, "devices_device_type_check", databaseError.Constraint)
		})
	}
}
