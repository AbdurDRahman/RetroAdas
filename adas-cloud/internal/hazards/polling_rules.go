package hazards

import (
	"fmt"
	"time"
)

// PollingRules holds the numbers that decide how far ahead the server searches
// and how often a vehicle polls (docs/design.md section 5). They are server
// configuration, not architecture: the server sends the resulting poll
// distance and fallback time in every response, so these can be tuned without
// updating any device.
type PollingRules struct {
	// TimeBetweenPolls is how long a moving vehicle should drive between two
	// polls. The distances below follow from it and the vehicle's speed.
	TimeBetweenPolls time.Duration

	// MinimumLookaheadDistanceMeters keeps slow vehicles looking far enough ahead.
	MinimumLookaheadDistanceMeters float64

	// MaximumSearchRadiusMeters stops a very high (or wrong) speed from
	// turning into a huge search.
	MaximumSearchRadiusMeters float64

	// FallbackTime is the longest a vehicle waits before polling again, even
	// when it isn't moving.
	FallbackTime time.Duration
}

// DefaultPollingRules are the agreed starting values (docs/design.md section 5).
// A server with no polling settings in its environment runs on these.
func DefaultPollingRules() PollingRules {
	return PollingRules{
		TimeBetweenPolls:               12 * time.Second,
		MinimumLookaheadDistanceMeters: 150,
		MaximumSearchRadiusMeters:      1000,
		FallbackTime:                   60 * time.Second,
	}
}

// Validate refuses rules that make no sense. The server checks this once when
// it starts, so a wrong setting stops the start instead of producing strange
// searches later.
func (pollingRules PollingRules) Validate() error {
	if pollingRules.TimeBetweenPolls <= 0 {
		return fmt.Errorf("the time between polls must be more than zero, got %s", pollingRules.TimeBetweenPolls)
	}
	if pollingRules.MinimumLookaheadDistanceMeters <= 0 {
		return fmt.Errorf("the minimum lookahead distance must be more than zero, got %g meters",
			pollingRules.MinimumLookaheadDistanceMeters)
	}
	if pollingRules.MaximumSearchRadiusMeters < pollingRules.MinimumLookaheadDistanceMeters {
		return fmt.Errorf("the maximum search radius (%g meters) must not be smaller than the minimum lookahead distance (%g meters)",
			pollingRules.MaximumSearchRadiusMeters, pollingRules.MinimumLookaheadDistanceMeters)
	}
	if pollingRules.FallbackTime <= 0 {
		return fmt.Errorf("the fallback time must be more than zero, got %s", pollingRules.FallbackTime)
	}
	return nil
}

// LookaheadDistanceMeters is how far ahead of the vehicle the center of the
// search circle lies at this speed: speed × time between polls, held between
// the minimum and the maximum.
func (pollingRules PollingRules) LookaheadDistanceMeters(speedKilometersPerHour float64) float64 {
	// Kilometers per hour to meters per second: 1000 meters in a kilometer,
	// 3600 seconds in an hour.
	speedMetersPerSecond := speedKilometersPerHour * 1000 / 3600
	distanceMeters := speedMetersPerSecond * pollingRules.TimeBetweenPolls.Seconds()

	// The minimum is applied first and the maximum last, so the maximum always
	// wins. A negative speed needs no special case: it lands below the minimum.
	return min(max(distanceMeters, pollingRules.MinimumLookaheadDistanceMeters), pollingRules.MaximumSearchRadiusMeters)
}

// SearchRadiusMeters is the radius of the search circle at this speed.
//
// The design sets it equal to the lookahead distance today (section 5), but it
// is a different thing with its own name: the agreed later refinement ("grow
// the circle") enlarges the circle and leaves the poll distance alone.
func (pollingRules PollingRules) SearchRadiusMeters(speedKilometersPerHour float64) float64 {
	return pollingRules.LookaheadDistanceMeters(speedKilometersPerHour)
}

// PollDistanceMeters is how far the vehicle drives at this speed before it
// polls again. Equal to the lookahead distance today, for the same reason.
func (pollingRules PollingRules) PollDistanceMeters(speedKilometersPerHour float64) float64 {
	return pollingRules.LookaheadDistanceMeters(speedKilometersPerHour)
}
