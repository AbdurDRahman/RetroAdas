package hazards_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database/testdatabase"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// --- test fixtures ---

const (
	jetsonDeviceID = "jetson-4f9c2a1b"
	phoneDeviceID  = "phone-91d0e7c3"
)

// pointerTo turns a number into the pointer the optional Report fields need.
// Go has no shorter way to write "a pointer to 0.9".
func pointerTo(value float64) *float64 {
	return &value
}

// validJetsonReport is a report every rule accepts. Test cases start from it
// and change one thing, so a refusal can only be caused by that change.
func validJetsonReport() hazards.Report {
	return hazards.Report{
		EventTag:       "pothole",
		Latitude:       33.6850,
		Longitude:      73.0490,
		HeadingDegrees: pointerTo(140),
		Confidence:     pointerTo(0.9),
		ReportedBy:     jetsonDeviceID,
		DeviceType:     devices.DeviceTypeJetsonUnit,
	}
}

func countHazards(test *testing.T, transaction *sql.Tx) int {
	test.Helper()
	var hazardCount int
	require.NoError(test, transaction.QueryRow(`SELECT count(*) FROM hazards`).Scan(&hazardCount))
	return hazardCount
}

// seedDevice inserts a device, because a hazard can only be reported by a
// device that exists (a foreign key in the hazards table).
func seedDevice(test *testing.T, transaction *sql.Tx, deviceID string, deviceType string) {
	test.Helper()
	_, err := transaction.Exec(
		`INSERT INTO devices (device_id, api_key_hash, device_type) VALUES ($1, 'not-a-real-hash', $2)`,
		deviceID, deviceType,
	)
	require.NoError(test, err)
}

// storedHazard is a hazard row as the tests read it back. sql.NullFloat64 is
// how Go reads a column that may be empty: Valid says whether there is a value.
type storedHazard struct {
	EventTag       string
	Latitude       float64
	Longitude      float64
	HeadingDegrees sql.NullFloat64
	Confidence     sql.NullFloat64
	Source         string
	ReportedBy     string
	Confirmations  int
	ExpiresAt      time.Time
}

func readHazard(test *testing.T, transaction *sql.Tx, hazardID int64) storedHazard {
	test.Helper()
	var hazard storedHazard
	// ST_Y is the latitude and ST_X the longitude: PostGIS points are
	// (longitude, latitude), the opposite of how people usually say it.
	err := transaction.QueryRow(
		`SELECT event_tag, ST_Y(location::geometry), ST_X(location::geometry),
		        heading_degrees, confidence, source, reported_by, confirmations, expires_at
		 FROM hazards WHERE id = $1`,
		hazardID,
	).Scan(&hazard.EventTag, &hazard.Latitude, &hazard.Longitude,
		&hazard.HeadingDegrees, &hazard.Confidence, &hazard.Source,
		&hazard.ReportedBy, &hazard.Confirmations, &hazard.ExpiresAt)
	require.NoError(test, err)
	return hazard
}

// hasConfirmationRow says whether hazard_confirmations records this device for
// this hazard.
func hasConfirmationRow(test *testing.T, transaction *sql.Tx, hazardID int64, deviceID string) bool {
	test.Helper()
	var rowExists bool
	err := transaction.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM hazard_confirmations WHERE hazard_id = $1 AND device_id = $2)`,
		hazardID, deviceID,
	).Scan(&rowExists)
	require.NoError(test, err)
	return rowExists
}

// seededHazard describes a hazard that already exists before the report under
// test arrives.
type seededHazard struct {
	EventTag       string
	Latitude       float64
	Longitude      float64
	HeadingDegrees *float64
	ReportedBy     string
	// ExpiresIn is how long the hazard still has to live. Tests use a value
	// that differs from the tag's time to live, so "the expiry was reset" and
	// "the expiry was left alone" give visibly different results.
	ExpiresIn time.Duration
}

// seedHazard inserts a hazard directly, without going through ProcessReport,
// so a test controls exactly what exists beforehand. Like ProcessReport, it
// also records the reporter in hazard_confirmations. It returns the new id.
func seedHazard(test *testing.T, transaction *sql.Tx, hazard seededHazard) int64 {
	test.Helper()
	var hazardID int64
	err := transaction.QueryRow(
		`INSERT INTO hazards (event_tag, location, heading_degrees, confidence, source, reported_by, expires_at)
		 VALUES ($1, ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography, $4, 0.9, 'auto', $5,
		         now() + make_interval(secs => $6))
		 RETURNING id`,
		hazard.EventTag, hazard.Longitude, hazard.Latitude, hazard.HeadingDegrees,
		hazard.ReportedBy, hazard.ExpiresIn.Seconds(),
	).Scan(&hazardID)
	require.NoError(test, err)

	_, err = transaction.Exec(
		`INSERT INTO hazard_confirmations (hazard_id, device_id) VALUES ($1, $2)`,
		hazardID, hazard.ReportedBy,
	)
	require.NoError(test, err)
	return hazardID
}

// seedJetsonPothole is the starting point of most tests: both devices exist,
// and the Jetson has already reported a pothole that expires in one hour.
func seedJetsonPothole(test *testing.T, transaction *sql.Tx) int64 {
	test.Helper()
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	seedDevice(test, transaction, phoneDeviceID, devices.DeviceTypeMobileApp)
	return seedHazard(test, transaction, seededHazard{
		EventTag:       "pothole",
		Latitude:       33.6850,
		Longitude:      73.0490,
		HeadingDegrees: pointerTo(140),
		ReportedBy:     jetsonDeviceID,
		ExpiresIn:      time.Hour,
	})
}

// phonePotholeReportNearby is the phone reporting the same pothole from about
// 7 meters away (well inside the 25 meter radius), travelling the same way.
func phonePotholeReportNearby() hazards.Report {
	return hazards.Report{
		EventTag:       "pothole",
		Latitude:       33.68505,
		Longitude:      73.04905,
		HeadingDegrees: pointerTo(140),
		ReportedBy:     phoneDeviceID,
		DeviceType:     devices.DeviceTypeMobileApp,
	}
}

// --- tests ---

// Step 0 of ProcessReport: a report that breaks a rule is refused with a named
// error, and nothing is stored.
func TestProcessReportRefusesInvalidReports(test *testing.T) {
	testCases := []struct {
		name          string
		change        func(report *hazards.Report)
		expectedError error
	}{
		{
			name:          "a tag that is not in the registry",
			change:        func(report *hazards.Report) { report.EventTag = "spaceship" },
			expectedError: hazards.ErrUnknownEventTag,
		},
		{
			// Only the app may report a checkpost (design section 4).
			name:          "a tag this device type may not report",
			change:        func(report *hazards.Report) { report.EventTag = "checkpost" },
			expectedError: hazards.ErrEventTagNotAllowed,
		},
		{
			name:          "a detector's report without a confidence",
			change:        func(report *hazards.Report) { report.Confidence = nil },
			expectedError: hazards.ErrInvalidConfidence,
		},
		{
			name:          "a detector's report with a confidence above 1",
			change:        func(report *hazards.Report) { report.Confidence = pointerTo(1.5) },
			expectedError: hazards.ErrInvalidConfidence,
		},
		{
			name:          "a detector's report with a negative confidence",
			change:        func(report *hazards.Report) { report.Confidence = pointerTo(-0.1) },
			expectedError: hazards.ErrInvalidConfidence,
		},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			hazardCountBefore := countHazards(test, transaction)

			report := validJetsonReport()
			testCase.change(&report)
			_, err := hazards.ProcessReport(transaction, report)

			assert.ErrorIs(test, err, testCase.expectedError)
			assert.Equal(test, hazardCountBefore, countHazards(test, transaction), "a refused report must store nothing")
		})
	}
}

// Steps 1 and 2 of ProcessReport: nothing of that kind is nearby, so the report
// becomes a new hazard.
func TestProcessReportCreatesAHazardWhenNoneIsNearby(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)

	outcome, err := hazards.ProcessReport(transaction, validJetsonReport())

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusCreated, outcome.Status)
	// The first reporter counts, so a new hazard starts at 1 (design section 6).
	assert.Equal(test, 1, outcome.Confirmations)
	// A pothole lives 14 days. "Within a minute" allows for the test's own run time.
	assert.WithinDuration(test, time.Now().Add(14*24*time.Hour), outcome.ExpiresAt, time.Minute)

	// What was stored is what was reported.
	hazard := readHazard(test, transaction, outcome.ID)
	assert.Equal(test, "pothole", hazard.EventTag)
	assert.InDelta(test, 33.6850, hazard.Latitude, 0.000001)
	assert.InDelta(test, 73.0490, hazard.Longitude, 0.000001)
	assert.Equal(test, sql.NullFloat64{Float64: 140, Valid: true}, hazard.HeadingDegrees)
	assert.True(test, hazard.Confidence.Valid)
	assert.InDelta(test, 0.9, hazard.Confidence.Float64, 0.000001)
	assert.Equal(test, hazards.SourceAuto, hazard.Source)
	assert.Equal(test, jetsonDeviceID, hazard.ReportedBy)
	assert.Equal(test, 1, hazard.Confirmations)
	assert.WithinDuration(test, outcome.ExpiresAt, hazard.ExpiresAt, time.Second)

	// The reporter is recorded, so its own repeat can be recognized later.
	assert.True(test, hasConfirmationRow(test, transaction, outcome.ID, jetsonDeviceID))
}

func TestProcessReportStoresAPersonsReportWithoutConfidenceOrHeading(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, phoneDeviceID, devices.DeviceTypeMobileApp)

	// A phone sends a confidence anyway, and no heading (someone on foot).
	report := hazards.Report{
		EventTag:   "debris",
		Latitude:   33.6850,
		Longitude:  73.0490,
		Confidence: pointerTo(0.99),
		ReportedBy: phoneDeviceID,
		DeviceType: devices.DeviceTypeMobileApp,
	}
	outcome, err := hazards.ProcessReport(transaction, report)

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusCreated, outcome.Status)

	hazard := readHazard(test, transaction, outcome.ID)
	assert.Equal(test, hazards.SourceManual, hazard.Source)
	// The phone's "0.99" is ignored: a person's report has no detector score,
	// so a phone can't pose as a confident detector (design section 4).
	assert.False(test, hazard.Confidence.Valid, "a manual report must store no confidence")
	assert.False(test, hazard.HeadingDegrees.Valid, "no heading was sent, so none is stored")
	// Debris lives 1 day: the time to live comes from the tag, not a fixed value.
	assert.WithinDuration(test, time.Now().Add(24*time.Hour), outcome.ExpiresAt, time.Minute)
}

// Step 3 of ProcessReport: another device reports a hazard that already exists.
func TestProcessReportConfirmsAHazardReportedByAnotherDevice(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seededHazardID := seedJetsonPothole(test, transaction)
	hazardCountBefore := countHazards(test, transaction)

	outcome, err := hazards.ProcessReport(transaction, phonePotholeReportNearby())

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusConfirmed, outcome.Status)
	assert.Equal(test, seededHazardID, outcome.ID, "it must be the existing hazard, not a new one")
	assert.Equal(test, 2, outcome.Confirmations)
	// The expiry was 1 hour away; a confirmation resets it to a full time to live.
	assert.WithinDuration(test, time.Now().Add(14*24*time.Hour), outcome.ExpiresAt, time.Minute)
	assert.Equal(test, hazardCountBefore, countHazards(test, transaction), "no second hazard")

	hazard := readHazard(test, transaction, seededHazardID)
	assert.Equal(test, 2, hazard.Confirmations)
	assert.WithinDuration(test, outcome.ExpiresAt, hazard.ExpiresAt, time.Second)
	// The first report stays the truth about the hazard itself: its location is
	// not averaged, and its confidence and source are not replaced.
	assert.InDelta(test, 33.6850, hazard.Latitude, 0.000001)
	assert.InDelta(test, 73.0490, hazard.Longitude, 0.000001)
	assert.InDelta(test, 0.9, hazard.Confidence.Float64, 0.000001)
	assert.Equal(test, hazards.SourceAuto, hazard.Source)
	assert.Equal(test, jetsonDeviceID, hazard.ReportedBy)

	assert.True(test, hasConfirmationRow(test, transaction, seededHazardID, phoneDeviceID))
}

// Step 4 of ProcessReport: a device reports a hazard it has already reported or
// confirmed. Nothing may change, or one device could inflate the count and
// keep a fake hazard alive forever.
func TestProcessReportRecognizesARepeatFromTheSameDevice(test *testing.T) {
	test.Run("the original reporter reports it again", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		seededHazardID := seedJetsonPothole(test, transaction)
		hazardCountBefore := countHazards(test, transaction)

		outcome, err := hazards.ProcessReport(transaction, validJetsonReport())

		require.NoError(test, err)
		assert.Equal(test, hazards.StatusAlreadyReported, outcome.Status)
		assert.Equal(test, seededHazardID, outcome.ID)
		assert.Equal(test, 1, outcome.Confirmations)
		// Still 1 hour away: a repeat must not reset the expiry.
		assert.WithinDuration(test, time.Now().Add(time.Hour), outcome.ExpiresAt, time.Minute)
		assert.Equal(test, hazardCountBefore, countHazards(test, transaction), "no second hazard")

		hazard := readHazard(test, transaction, seededHazardID)
		assert.Equal(test, 1, hazard.Confirmations)
		assert.WithinDuration(test, time.Now().Add(time.Hour), hazard.ExpiresAt, time.Minute)
	})

	test.Run("a device that confirmed it reports it again", func(test *testing.T) {
		transaction := testdatabase.NewTransaction(test)
		seededHazardID := seedJetsonPothole(test, transaction)

		firstOutcome, err := hazards.ProcessReport(transaction, phonePotholeReportNearby())
		require.NoError(test, err)
		require.Equal(test, hazards.StatusConfirmed, firstOutcome.Status)

		secondOutcome, err := hazards.ProcessReport(transaction, phonePotholeReportNearby())

		require.NoError(test, err)
		assert.Equal(test, hazards.StatusAlreadyReported, secondOutcome.Status)
		assert.Equal(test, seededHazardID, secondOutcome.ID)
		assert.Equal(test, 2, secondOutcome.Confirmations, "the count stays at 2, it does not become 3")
		assert.Equal(test, 2, readHazard(test, transaction, seededHazardID).Confirmations)
	})
}

// The remaining rules of step 1: which existing hazards count as "the same one".

func TestProcessReportNeverMatchesAnExpiredHazard(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	seedDevice(test, transaction, phoneDeviceID, devices.DeviceTypeMobileApp)
	// Same place, same tag, but it expired an hour ago.
	expiredHazardID := seedHazard(test, transaction, seededHazard{
		EventTag: "pothole", Latitude: 33.6850, Longitude: 73.0490,
		HeadingDegrees: pointerTo(140), ReportedBy: jetsonDeviceID,
		ExpiresIn: -time.Hour,
	})

	outcome, err := hazards.ProcessReport(transaction, phonePotholeReportNearby())

	require.NoError(test, err)
	// An expired hazard is history. A new report there is a new hazard, and
	// the old one is not brought back to life.
	assert.Equal(test, hazards.StatusCreated, outcome.Status)
	assert.NotEqual(test, expiredHazardID, outcome.ID)
	assert.Equal(test, 1, readHazard(test, transaction, expiredHazardID).Confirmations)
}

func TestProcessReportMatchesTheNearestOfSeveralHazards(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	seedDevice(test, transaction, phoneDeviceID, devices.DeviceTypeMobileApp)
	// Two potholes, both inside the 25 meter radius of the report below. The
	// far one is inserted FIRST on purpose: a query that just took "the first
	// row it finds" would pick it, and this test would catch that.
	farHazardID := seedHazard(test, transaction, seededHazard{
		EventTag: "pothole", Latitude: 33.68518, Longitude: 73.0490, // about 20 meters north
		HeadingDegrees: pointerTo(140), ReportedBy: jetsonDeviceID, ExpiresIn: time.Hour,
	})
	nearHazardID := seedHazard(test, transaction, seededHazard{
		EventTag: "pothole", Latitude: 33.68504, Longitude: 73.0490, // about 4 meters north
		HeadingDegrees: pointerTo(140), ReportedBy: jetsonDeviceID, ExpiresIn: time.Hour,
	})

	report := phonePotholeReportNearby()
	report.Latitude, report.Longitude = 33.6850, 73.0490
	outcome, err := hazards.ProcessReport(transaction, report)

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusConfirmed, outcome.Status)
	assert.Equal(test, nearHazardID, outcome.ID)
	assert.Equal(test, 1, readHazard(test, transaction, farHazardID).Confirmations, "the far one is untouched")
}

// One degree of latitude is about 111 kilometers, so moving a report north by
// 0.00018 degrees moves it about 20 meters, and 0.0009 degrees about 100 meters.
func TestProcessReportUsesEachTagsOwnRadius(test *testing.T) {
	testCases := []struct {
		name           string
		eventTag       string
		latitudeOffset float64
		expectedStatus string
	}{
		{name: "pothole (25 m radius), 20 m away -> the same hazard", eventTag: "pothole", latitudeOffset: 0.00018, expectedStatus: hazards.StatusConfirmed},
		{name: "pothole (25 m radius), 30 m away -> a new hazard", eventTag: "pothole", latitudeOffset: 0.00027, expectedStatus: hazards.StatusCreated},
		{name: "accident (150 m radius), 100 m away -> the same hazard", eventTag: "accident", latitudeOffset: 0.0009, expectedStatus: hazards.StatusConfirmed},
		{name: "accident (150 m radius), 200 m away -> a new hazard", eventTag: "accident", latitudeOffset: 0.0018, expectedStatus: hazards.StatusCreated},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
			seedDevice(test, transaction, phoneDeviceID, devices.DeviceTypeMobileApp)
			seedHazard(test, transaction, seededHazard{
				EventTag: testCase.eventTag, Latitude: 33.6850, Longitude: 73.0490,
				HeadingDegrees: pointerTo(140), ReportedBy: jetsonDeviceID, ExpiresIn: time.Hour,
			})

			report := phonePotholeReportNearby()
			report.EventTag = testCase.eventTag
			report.Latitude, report.Longitude = 33.6850+testCase.latitudeOffset, 73.0490
			outcome, err := hazards.ProcessReport(transaction, report)

			require.NoError(test, err)
			assert.Equal(test, testCase.expectedStatus, outcome.Status)
		})
	}
}

func TestProcessReportTreatsADifferentTagAsADifferentHazard(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	seedDevice(test, transaction, phoneDeviceID, devices.DeviceTypeMobileApp)
	// Debris at exactly the spot where the phone will report a pothole.
	debrisHazardID := seedHazard(test, transaction, seededHazard{
		EventTag: "debris", Latitude: 33.68505, Longitude: 73.04905,
		HeadingDegrees: pointerTo(140), ReportedBy: jetsonDeviceID, ExpiresIn: time.Hour,
	})

	outcome, err := hazards.ProcessReport(transaction, phonePotholeReportNearby())

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusCreated, outcome.Status)
	assert.NotEqual(test, debrisHazardID, outcome.ID)
	assert.Equal(test, 1, readHazard(test, transaction, debrisHazardID).Confirmations)
}

// The direction rule (docs/design.md section 6): for a tag where direction
// matters, a report from oncoming traffic is a different hazard, because the
// two sides of a road are often closer together than the tag's radius.
func TestProcessReportTreatsOncomingTrafficAsADifferentHazard(test *testing.T) {
	testCases := []struct {
		name          string
		eventTag      string
		hazardHeading *float64 // the stored hazard's heading; nil = none
		reportHeading *float64 // the new report's heading; nil = none
		// StatusConfirmed = "the same hazard", StatusCreated = "a different one".
		expectedStatus string
	}{
		{name: "same heading -> same hazard", eventTag: "pothole", hazardHeading: pointerTo(140), reportHeading: pointerTo(140), expectedStatus: hazards.StatusConfirmed},
		{name: "a side road, 90 degrees apart -> same hazard", eventTag: "pothole", hazardHeading: pointerTo(140), reportHeading: pointerTo(230), expectedStatus: hazards.StatusConfirmed},

		// The edge of the rule: "more than 135 degrees" is a different hazard.
		{name: "134 degrees apart -> same hazard", eventTag: "pothole", hazardHeading: pointerTo(140), reportHeading: pointerTo(274), expectedStatus: hazards.StatusConfirmed},
		{name: "exactly 135 degrees apart -> same hazard", eventTag: "pothole", hazardHeading: pointerTo(140), reportHeading: pointerTo(275), expectedStatus: hazards.StatusConfirmed},
		{name: "136 degrees apart -> different hazard", eventTag: "pothole", hazardHeading: pointerTo(140), reportHeading: pointerTo(276), expectedStatus: hazards.StatusCreated},
		{name: "oncoming, 180 degrees apart -> different hazard", eventTag: "pothole", hazardHeading: pointerTo(140), reportHeading: pointerTo(320), expectedStatus: hazards.StatusCreated},

		// A compass wraps around: 350 and 10 are both roughly north, 20 apart.
		{name: "350 and 10 are only 20 degrees apart -> same hazard", eventTag: "pothole", hazardHeading: pointerTo(350), reportHeading: pointerTo(10), expectedStatus: hazards.StatusConfirmed},
		{name: "350 and 170 are 180 degrees apart -> different hazard", eventTag: "pothole", hazardHeading: pointerTo(350), reportHeading: pointerTo(170), expectedStatus: hazards.StatusCreated},

		// A missing heading on either side: only location counts.
		{name: "the report has no heading -> same hazard", eventTag: "pothole", hazardHeading: pointerTo(140), reportHeading: nil, expectedStatus: hazards.StatusConfirmed},
		{name: "the hazard has no heading -> same hazard", eventTag: "pothole", hazardHeading: nil, reportHeading: pointerTo(320), expectedStatus: hazards.StatusConfirmed},

		// An accident affects both directions, so direction is ignored for it.
		{name: "an accident reported from the oncoming side -> same hazard", eventTag: "accident", hazardHeading: pointerTo(140), reportHeading: pointerTo(320), expectedStatus: hazards.StatusConfirmed},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			transaction := testdatabase.NewTransaction(test)
			seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
			seedDevice(test, transaction, phoneDeviceID, devices.DeviceTypeMobileApp)
			seedHazard(test, transaction, seededHazard{
				EventTag: testCase.eventTag, Latitude: 33.6850, Longitude: 73.0490,
				HeadingDegrees: testCase.hazardHeading, ReportedBy: jetsonDeviceID, ExpiresIn: time.Hour,
			})

			report := phonePotholeReportNearby() // about 7 meters away
			report.EventTag = testCase.eventTag
			report.HeadingDegrees = testCase.reportHeading
			outcome, err := hazards.ProcessReport(transaction, report)

			require.NoError(test, err)
			assert.Equal(test, testCase.expectedStatus, outcome.Status)
		})
	}
}

// Once both directions have their own hazard at one spot, a report must go to
// the hazard travelling its way, even when the other one is closer.
func TestProcessReportMatchesTheHazardGoingTheSameWay(test *testing.T) {
	transaction := testdatabase.NewTransaction(test)
	seedDevice(test, transaction, jetsonDeviceID, devices.DeviceTypeJetsonUnit)
	seedDevice(test, transaction, phoneDeviceID, devices.DeviceTypeMobileApp)
	// Closest to the report, but going the other way (heading 140).
	oncomingHazardID := seedHazard(test, transaction, seededHazard{
		EventTag: "pothole", Latitude: 33.68504, Longitude: 73.0490, // about 4 meters north
		HeadingDegrees: pointerTo(140), ReportedBy: jetsonDeviceID, ExpiresIn: time.Hour,
	})
	// Further away, but going the same way as the report (heading 320).
	sameWayHazardID := seedHazard(test, transaction, seededHazard{
		EventTag: "pothole", Latitude: 33.68514, Longitude: 73.0490, // about 16 meters north
		HeadingDegrees: pointerTo(320), ReportedBy: jetsonDeviceID, ExpiresIn: time.Hour,
	})

	report := phonePotholeReportNearby()
	report.Latitude, report.Longitude = 33.6850, 73.0490
	report.HeadingDegrees = pointerTo(320)
	outcome, err := hazards.ProcessReport(transaction, report)

	require.NoError(test, err)
	assert.Equal(test, hazards.StatusConfirmed, outcome.Status)
	assert.Equal(test, sameWayHazardID, outcome.ID)
	assert.Equal(test, 1, readHazard(test, transaction, oncomingHazardID).Confirmations, "the oncoming one is untouched")
}
