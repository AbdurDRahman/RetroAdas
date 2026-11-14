package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/config"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/internal/hazards"
)

const (
	validDatabaseURL = "postgres://adas_user:changeme@localhost:5432/adas_dev?sslmode=disable"
	// 40 characters: comfortably over the 32-character minimum.
	validPepper = "test-pepper-not-a-real-secret-0123456789"
	// 31 characters: one short of the minimum, to test the boundary exactly.
	tooShortPepper = "only-thirty-one-characters-long"
)

// setEnvironment sets all three variables for one test. test.Setenv puts the
// old values back when the test ends, so tests can't leak settings into each
// other. An empty string counts as "not set" for the loaders.
func setEnvironment(test *testing.T, databaseURL string, pepper string, serverPort string) {
	test.Helper()
	test.Setenv("DATABASE_URL", databaseURL)
	test.Setenv("API_KEY_PEPPER", pepper)
	test.Setenv("SERVER_PORT", serverPort)
	// The polling variables start empty, so a value in the developer's own
	// shell can't change what a test sees.
	for _, name := range pollingRuleVariableNames {
		test.Setenv(name, "")
	}
}

var pollingRuleVariableNames = []string{
	"TIME_BETWEEN_POLLS_SECONDS",
	"MINIMUM_LOOKAHEAD_DISTANCE_METERS",
	"MAXIMUM_SEARCH_RADIUS_METERS",
	"FALLBACK_TIME_SECONDS",
}

func TestLoad(test *testing.T) {
	test.Run("everything set -> values are returned", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, validPepper, "9090")

		configuration, err := config.Load()

		require.NoError(test, err)
		assert.Equal(test, validDatabaseURL, configuration.DatabaseURL)
		assert.Equal(test, validPepper, configuration.APIKeyPepper)
		assert.Equal(test, "9090", configuration.ServerPort)
	})

	test.Run("no SERVER_PORT -> defaults to 8080", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, validPepper, "")

		configuration, err := config.Load()

		require.NoError(test, err)
		assert.Equal(test, "8080", configuration.ServerPort)
	})

	test.Run("no DATABASE_URL -> error", func(test *testing.T) {
		setEnvironment(test, "", validPepper, "")

		_, err := config.Load()

		assert.ErrorContains(test, err, "DATABASE_URL")
	})

	test.Run("no API_KEY_PEPPER -> error", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, "", "")

		_, err := config.Load()

		assert.ErrorContains(test, err, "API_KEY_PEPPER")
	})

	test.Run("API_KEY_PEPPER too short -> error that does not reveal it", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, tooShortPepper, "")

		_, err := config.Load()

		require.ErrorContains(test, err, "API_KEY_PEPPER")
		// Error messages end up in logs. A secret must never be printed there,
		// not even a weak one that is being rejected.
		assert.NotContains(test, err.Error(), tooShortPepper)
	})
}

// The migrate program only talks to the database, so it must start without
// the pepper (a secret it never uses).
func TestLoadDatabaseURL(test *testing.T) {
	test.Run("works with only DATABASE_URL set", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, "", "")

		actualDatabaseURL, err := config.LoadDatabaseURL()

		require.NoError(test, err)
		assert.Equal(test, validDatabaseURL, actualDatabaseURL)
	})

	test.Run("no DATABASE_URL -> error", func(test *testing.T) {
		setEnvironment(test, "", "", "")

		_, err := config.LoadDatabaseURL()

		assert.ErrorContains(test, err, "DATABASE_URL")
	})
}

// The four polling numbers are optional (docs/design.md section 5): a server
// with none of them set runs on the agreed defaults.
func TestLoadPollingRules(test *testing.T) {
	test.Run("none set -> the agreed defaults", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, validPepper, "")

		configuration, err := config.Load()

		require.NoError(test, err)
		assert.Equal(test, hazards.DefaultPollingRules(), configuration.PollingRules)
	})

	test.Run("all four set -> those values", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, validPepper, "")
		test.Setenv("TIME_BETWEEN_POLLS_SECONDS", "10")
		test.Setenv("MINIMUM_LOOKAHEAD_DISTANCE_METERS", "120.5")
		test.Setenv("MAXIMUM_SEARCH_RADIUS_METERS", "800")
		test.Setenv("FALLBACK_TIME_SECONDS", "45")

		configuration, err := config.Load()

		require.NoError(test, err)
		assert.Equal(test, hazards.PollingRules{
			TimeBetweenPolls:               10 * time.Second,
			MinimumLookaheadDistanceMeters: 120.5,
			MaximumSearchRadiusMeters:      800,
			FallbackTime:                   45 * time.Second,
		}, configuration.PollingRules)
	})

	test.Run("only one set -> the others keep their defaults", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, validPepper, "")
		test.Setenv("TIME_BETWEEN_POLLS_SECONDS", "8")

		configuration, err := config.Load()

		require.NoError(test, err)
		expectedPollingRules := hazards.DefaultPollingRules()
		expectedPollingRules.TimeBetweenPolls = 8 * time.Second
		assert.Equal(test, expectedPollingRules, configuration.PollingRules)
	})

	// "NaN" and "Inf" matter: Go reads both as valid numbers, and either one
	// would turn every search distance into nonsense.
	for _, notANumber := range []string{"twelve", "12 seconds", "NaN", "Inf"} {
		test.Run("not a usable number ("+notANumber+") -> error naming the variable", func(test *testing.T) {
			setEnvironment(test, validDatabaseURL, validPepper, "")
			test.Setenv("TIME_BETWEEN_POLLS_SECONDS", notANumber)

			_, err := config.Load()

			assert.ErrorContains(test, err, "TIME_BETWEEN_POLLS_SECONDS")
		})
	}

	test.Run("rules that make no sense -> error", func(test *testing.T) {
		setEnvironment(test, validDatabaseURL, validPepper, "")
		// Smaller than the default minimum lookahead distance of 150 meters.
		test.Setenv("MAXIMUM_SEARCH_RADIUS_METERS", "100")

		_, err := config.Load()

		assert.ErrorContains(test, err, "maximum search radius")
	})
}
