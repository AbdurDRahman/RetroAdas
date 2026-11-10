// Package config centralizes all environment-driven settings.
// Nothing in this project should read os.Getenv directly outside this file —
// that keeps the laptop -> single virtual machine -> separate database machine
// transition to a change of environment variables, with zero code changes
// anywhere else.
package config

import (
	"fmt"
	"os"
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

	return &Config{
		DatabaseURL:  databaseURL,
		ServerPort:   serverPort,
		APIKeyPepper: apiKeyPepper,
	}, nil
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
