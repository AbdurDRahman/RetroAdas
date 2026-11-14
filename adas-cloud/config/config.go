// Package config centralizes all environment-driven settings.
// Nothing in this project should read os.Getenv directly outside this file —
// that keeps the laptop -> single virtual machine -> separate database machine
// transition to a change of environment variables, with zero code changes
// anywhere else.
package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

// MinimumAPIKeyPepperLength is the shortest pepper the server accepts, in
// characters. A short or empty pepper would quietly remove the protection it
// exists for, so the server refuses to start instead. Generate one with:
//
//	openssl rand -base64 32
const MinimumAPIKeyPepperLength = 32

// Config holds everything the server needs to boot.
type Config struct {
	// DatabaseURL points at the Postgres+PostGIS instance used by the running
	// server (e.g. postgres://user:pass@localhost:5432/adas_dev).
	// Today this is the same virtual machine as the API. When the database
	// moves to its own machine, only this value changes.
	DatabaseURL string

	// ServerPort is the port the HTTP server listens on.
	ServerPort string

	// APIKeyPepper is the server-side secret mixed into every API key hash
	// (docs/design.md section 7). It must be identical on every replica, and
	// changing it invalidates every device key. Never log it.
	APIKeyPepper string

	// PollingRules are the four numbers behind the search distance and the
	// polling pace (docs/design.md section 5). Each has an optional
	// environment variable; without it the agreed default applies.
	PollingRules hazards.PollingRules
}

// Load reads the server's configuration from environment variables and fails
// loudly if anything required is missing, rather than silently falling back to
// a default that could mask a misconfigured environment.
func Load() (*Config, error) {
	databaseURL, err := LoadDatabaseURL()
	if err != nil {
		return nil, err
	}

	apiKeyPepper := os.Getenv("API_KEY_PEPPER")
	if apiKeyPepper == "" {
		return nil, fmt.Errorf("API_KEY_PEPPER is not set (see .env.example)")
	}
	// The message gives the lengths, never the value: errors end up in logs.
	if len(apiKeyPepper) < MinimumAPIKeyPepperLength {
		return nil, fmt.Errorf("API_KEY_PEPPER is %d characters long; it must be at least %d",
			len(apiKeyPepper), MinimumAPIKeyPepperLength)
	}

	serverPort := os.Getenv("SERVER_PORT")
	if serverPort == "" {
		serverPort = "8080"
	}

	pollingRules, err := loadPollingRules()
	if err != nil {
		return nil, err
	}

	return &Config{
		DatabaseURL:  databaseURL,
		ServerPort:   serverPort,
		APIKeyPepper: apiKeyPepper,
		PollingRules: pollingRules,
	}, nil
}

// loadPollingRules starts from the defaults and replaces each number whose
// environment variable is set, then checks that the result makes sense.
func loadPollingRules() (hazards.PollingRules, error) {
	pollingRules := hazards.DefaultPollingRules()

	timeBetweenPollsSeconds, err := readOptionalNumber("TIME_BETWEEN_POLLS_SECONDS", pollingRules.TimeBetweenPolls.Seconds())
	if err != nil {
		return hazards.PollingRules{}, err
	}
	minimumLookaheadDistanceMeters, err := readOptionalNumber("MINIMUM_LOOKAHEAD_DISTANCE_METERS", pollingRules.MinimumLookaheadDistanceMeters)
	if err != nil {
		return hazards.PollingRules{}, err
	}
	maximumSearchRadiusMeters, err := readOptionalNumber("MAXIMUM_SEARCH_RADIUS_METERS", pollingRules.MaximumSearchRadiusMeters)
	if err != nil {
		return hazards.PollingRules{}, err
	}
	fallbackTimeSeconds, err := readOptionalNumber("FALLBACK_TIME_SECONDS", pollingRules.FallbackTime.Seconds())
	if err != nil {
		return hazards.PollingRules{}, err
	}

	pollingRules = hazards.PollingRules{
		TimeBetweenPolls:               secondsToDuration(timeBetweenPollsSeconds),
		MinimumLookaheadDistanceMeters: minimumLookaheadDistanceMeters,
		MaximumSearchRadiusMeters:      maximumSearchRadiusMeters,
		FallbackTime:                   secondsToDuration(fallbackTimeSeconds),
	}
	if err := pollingRules.Validate(); err != nil {
		return hazards.PollingRules{}, fmt.Errorf("polling rules: %w", err)
	}
	return pollingRules, nil
}

// readOptionalNumber reads a number from an environment variable, or returns
// defaultValue when the variable is not set.
func readOptionalNumber(variableName string, defaultValue float64) (float64, error) {
	text := os.Getenv(variableName)
	if text == "" {
		return defaultValue, nil
	}
	number, err := strconv.ParseFloat(text, 64)
	// Go reads "NaN" and "Inf" as valid numbers. Neither is a usable setting.
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("%s must be a number, got %q", variableName, text)
	}
	return number, nil
}

// secondsToDuration turns a number of seconds, which may have decimals, into
// Go's duration type.
func secondsToDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

// LoadDatabaseURL reads only DATABASE_URL. It is for programs that talk to the
// database and nothing else (cmd/migrate), so they never need to be given the
// server's secrets.
func LoadDatabaseURL() (string, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return "", fmt.Errorf("DATABASE_URL is not set (see .env.example)")
	}
	return databaseURL, nil
}
