package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/config"
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
