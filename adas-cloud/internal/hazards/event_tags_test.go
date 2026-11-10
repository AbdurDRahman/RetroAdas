package hazards_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/devices"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// The registry's values are the agreed table in docs/design.md section 4, plus
// the "direction matters" setting from section 6. Writing them out again here
// is deliberate: changing a rule then takes two edits (the registry and this
// test), so a value can't change by accident.
func TestLookUpEventTagReturnsTheAgreedRules(test *testing.T) {
	testCases := []struct {
		name                              string
		expectedTimeToLive                time.Duration
		expectedDeduplicationRadiusMeters float64
		expectedAllowedDeviceTypes        []string
		expectedDirectionMatters          bool
	}{
		{
			name:                              "accident",
			expectedTimeToLive:                90 * time.Minute,
			expectedDeduplicationRadiusMeters: 150,
			expectedAllowedDeviceTypes:        []string{devices.DeviceTypeJetsonUnit, devices.DeviceTypeMobileApp},
			expectedDirectionMatters:          false,
		},
		{
			name:                              "pothole",
			expectedTimeToLive:                14 * 24 * time.Hour,
			expectedDeduplicationRadiusMeters: 25,
			expectedAllowedDeviceTypes:        []string{devices.DeviceTypeJetsonUnit, devices.DeviceTypeMobileApp},
			expectedDirectionMatters:          true,
		},
		{
			name:                              "checkpost",
			expectedTimeToLive:                4 * time.Hour,
			expectedDeduplicationRadiusMeters: 100,
			expectedAllowedDeviceTypes:        []string{devices.DeviceTypeMobileApp},
			expectedDirectionMatters:          false,
		},
		{
			name:                              "debris",
			expectedTimeToLive:                24 * time.Hour,
			expectedDeduplicationRadiusMeters: 50,
			expectedAllowedDeviceTypes:        []string{devices.DeviceTypeMobileApp},
			expectedDirectionMatters:          true,
		},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			actualEventTag, found := hazards.LookUpEventTag(testCase.name)

			require.True(test, found)
			assert.Equal(test, testCase.name, actualEventTag.Name)
			assert.Equal(test, testCase.expectedTimeToLive, actualEventTag.TimeToLive)
			assert.Equal(test, testCase.expectedDeduplicationRadiusMeters, actualEventTag.DeduplicationRadiusMeters)
			assert.ElementsMatch(test, testCase.expectedAllowedDeviceTypes, actualEventTag.AllowedDeviceTypes)
			assert.Equal(test, testCase.expectedDirectionMatters, actualEventTag.DirectionMatters)
		})
	}
}

func TestLookUpEventTagRejectsUnknownNames(test *testing.T) {
	// "Pothole" matters: tag names are exact. If the lookup ignored capital
	// letters, the same hazard type could be stored under two spellings and
	// the two would never deduplicate against each other.
	for _, unknownName := range []string{"spaceship", "Pothole", " pothole", ""} {
		test.Run("unknown: "+unknownName, func(test *testing.T) {
			_, found := hazards.LookUpEventTag(unknownName)

			assert.False(test, found)
		})
	}
}

func TestEventTagAllowsDeviceType(test *testing.T) {
	checkpost, found := hazards.LookUpEventTag("checkpost")
	require.True(test, found)

	// Only a person can recognize a checkpost today, so only the app may report one.
	assert.True(test, checkpost.AllowsDeviceType(devices.DeviceTypeMobileApp))
	assert.False(test, checkpost.AllowsDeviceType(devices.DeviceTypeJetsonUnit))
	assert.False(test, checkpost.AllowsDeviceType("spaceship"))
}

func TestSourceForDeviceType(test *testing.T) {
	// A detector's report is "auto"; a person's report is "manual". The server
	// derives this from the device type, so a phone can't pose as a detector.
	actualSource, known := hazards.SourceForDeviceType(devices.DeviceTypeJetsonUnit)
	require.True(test, known)
	assert.Equal(test, hazards.SourceAuto, actualSource)

	actualSource, known = hazards.SourceForDeviceType(devices.DeviceTypeMobileApp)
	require.True(test, known)
	assert.Equal(test, hazards.SourceManual, actualSource)

	_, known = hazards.SourceForDeviceType("spaceship")
	assert.False(test, known)
}
