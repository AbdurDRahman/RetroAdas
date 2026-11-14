package hazards_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// The rule is docs/design.md section 5:
//
//	search_radius = lookahead_distance = poll_distance = speed × time_between_polls
//
// with a minimum and a maximum. The three distances are always equal, so one
// calculation gives all of them. It needs no database, which is why this test
// doesn't open one.
func TestLookaheadDistanceMeters(test *testing.T) {
	// The rules are written out here instead of taken from the defaults, so
	// this test keeps checking the arithmetic even if a default is tuned later.
	pollingRules := hazards.PollingRules{
		TimeBetweenPolls:               12 * time.Second,
		MinimumLookaheadDistanceMeters: 150,
		MaximumSearchRadiusMeters:      1000,
	}

	testCases := []struct {
		name                   string
		speedKilometersPerHour float64
		expectedDistanceMeters float64
	}{
		// Below 45 kilometers per hour the plain multiplication gives less
		// than 150 meters, so the minimum applies.
		{name: "stopped", speedKilometersPerHour: 0, expectedDistanceMeters: 150},
		{name: "30 kilometers per hour would be 100 meters, raised to the minimum", speedKilometersPerHour: 30, expectedDistanceMeters: 150},
		{name: "45 kilometers per hour is exactly the minimum", speedKilometersPerHour: 45, expectedDistanceMeters: 150},

		// The middle of the range: the rows of the table in the design.
		{name: "60 kilometers per hour", speedKilometersPerHour: 60, expectedDistanceMeters: 200},
		{name: "100 kilometers per hour", speedKilometersPerHour: 100, expectedDistanceMeters: 333.33},
		{name: "120 kilometers per hour", speedKilometersPerHour: 120, expectedDistanceMeters: 400},

		// No real car reaches the maximum, but a wrong speed reading from the
		// positioning receiver can. It must not turn into a huge search.
		{name: "300 kilometers per hour is exactly the maximum", speedKilometersPerHour: 300, expectedDistanceMeters: 1000},
		{name: "a speed reading of 500 is held at the maximum", speedKilometersPerHour: 500, expectedDistanceMeters: 1000},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			actualDistanceMeters := pollingRules.LookaheadDistanceMeters(testCase.speedKilometersPerHour)

			// One centimeter of tolerance: 100 kilometers per hour gives
			// 333.333... meters, which no decimal number equals exactly.
			assert.InDelta(test, testCase.expectedDistanceMeters, actualDistanceMeters, 0.01)
		})
	}
}

// The agreed starting values (docs/design.md section 5). Writing them out
// again here is deliberate, as with the event-tag registry: changing a default
// takes two edits, so it can't change by accident.
func TestDefaultPollingRulesAreTheAgreedValues(test *testing.T) {
	pollingRules := hazards.DefaultPollingRules()

	assert.Equal(test, 12*time.Second, pollingRules.TimeBetweenPolls)
	assert.Equal(test, 150.0, pollingRules.MinimumLookaheadDistanceMeters)
	assert.Equal(test, 1000.0, pollingRules.MaximumSearchRadiusMeters)
	assert.Equal(test, 60*time.Second, pollingRules.FallbackTime)
	assert.NoError(test, pollingRules.Validate())
}

// Rules that make no sense are refused when the server starts, instead of
// producing strange searches later.
func TestPollingRulesValidateRefusesRulesThatMakeNoSense(test *testing.T) {
	testCases := []struct {
		name   string
		change func(pollingRules *hazards.PollingRules)
		// A word the error must contain, so the operator can tell which
		// setting is wrong.
		expectedErrorMentions string
	}{
		{name: "no time between polls", change: func(pollingRules *hazards.PollingRules) { pollingRules.TimeBetweenPolls = 0 }, expectedErrorMentions: "time between polls"},
		{name: "negative time between polls", change: func(pollingRules *hazards.PollingRules) { pollingRules.TimeBetweenPolls = -time.Second }, expectedErrorMentions: "time between polls"},
		{name: "no minimum lookahead distance", change: func(pollingRules *hazards.PollingRules) { pollingRules.MinimumLookaheadDistanceMeters = 0 }, expectedErrorMentions: "minimum lookahead distance"},
		{name: "minimum larger than maximum", change: func(pollingRules *hazards.PollingRules) { pollingRules.MaximumSearchRadiusMeters = 100 }, expectedErrorMentions: "maximum search radius"},
		{name: "no fallback time", change: func(pollingRules *hazards.PollingRules) { pollingRules.FallbackTime = 0 }, expectedErrorMentions: "fallback time"},
	}

	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			pollingRules := hazards.DefaultPollingRules()
			testCase.change(&pollingRules)

			assert.ErrorContains(test, pollingRules.Validate(), testCase.expectedErrorMentions)
		})
	}
}
