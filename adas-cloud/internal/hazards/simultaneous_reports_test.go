package hazards_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// Several devices report the same new pothole at the same instant (task
// C-116). Each must see the others' work: the result is one hazard confirmed
// by all of them, not one hazard per device.
//
// The other tests can't show this. They run inside one transaction that is
// rolled back, and this problem only exists between separate transactions that
// each save their work. So this test gets its own empty database.
func TestProcessReportHandlesSimultaneousReportsOfOneHazard(test *testing.T) {
	connectionPool := testdatabase.NewEmptyDatabase(test)
	_, err := database.Migrate(test.Context(), connectionPool)
	require.NoError(test, err)

	const deviceCount = 8
	var deviceIDs []string
	for deviceNumber := range deviceCount {
		deviceID := fmt.Sprintf("jetson-0000000%d", deviceNumber)
		_, err := connectionPool.Exec(
			`INSERT INTO devices (device_id, api_key_hash, device_type) VALUES ($1, 'not-a-real-hash', $2)`,
			deviceID, devices.DeviceTypeJetsonUnit,
		)
		require.NoError(test, err)
		deviceIDs = append(deviceIDs, deviceID)
	}

	type reportResult struct {
		outcome hazards.Outcome
		err     error
	}
	results := make(chan reportResult, deviceCount)

	// Each device gets its transaction opened BEFORE anyone reports. Opening a
	// database connection takes a few milliseconds, longer than a whole report,
	// so without this the reports would run one after another by accident and
	// the test would pass without testing anything.
	var transactions []*sql.Tx
	for range deviceIDs {
		transaction, err := connectionPool.Begin()
		require.NoError(test, err)
		transactions = append(transactions, transaction)
	}

	// Every goroutine waits on startSignal, so all the reports are released at
	// the same moment. Each one then does what the HTTP handler will do per
	// request (C-105): process one report in its own transaction and save it.
	startSignal := make(chan struct{})
	for deviceNumber, deviceID := range deviceIDs {
		go func() {
			<-startSignal
			transaction := transactions[deviceNumber]
			report := validJetsonReport()
			report.ReportedBy = deviceID

			outcome, err := hazards.ProcessReport(transaction, report)
			if err != nil {
				_ = transaction.Rollback()
				results <- reportResult{err: err}
				return
			}
			results <- reportResult{outcome: outcome, err: transaction.Commit()}
		}()
	}
	close(startSignal)

	statusCounts := map[string]int{}
	for range deviceCount {
		result := <-results
		require.NoError(test, result.err)
		statusCounts[result.outcome.Status]++
	}

	// Exactly one device was first; every other one confirmed its hazard.
	assert.Equal(test, 1, statusCounts[hazards.StatusCreated], "only one report may create the hazard")
	assert.Equal(test, deviceCount-1, statusCounts[hazards.StatusConfirmed])

	var hazardCount, confirmations int
	err = connectionPool.QueryRow(`SELECT count(*), coalesce(max(confirmations), 0) FROM hazards`).Scan(&hazardCount, &confirmations)
	require.NoError(test, err)
	assert.Equal(test, 1, hazardCount, "one real-world hazard must be one row")
	assert.Equal(test, deviceCount, confirmations)
}
