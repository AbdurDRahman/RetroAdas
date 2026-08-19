// Package config centralizes all environment-driven settings for the server.
// Nothing in this project should read os.Getenv directly outside this file —
// that keeps the dev -> stress-test -> separate-DB-machine transition to a
// single env var change, with zero code changes anywhere else.
package config

import (
	"fmt"
	"os"
)

// Config holds everything the server needs to boot.
type Config struct {
	// DatabaseURL points at the Postgres+PostGIS instance used by the running
	// server (e.g. postgres://user:pass@localhost:5432/adas_dev).
	// Today this is the same VM as the API. When DB work moves to its own
	// machine for stress testing, only this value changes.
	DatabaseURL string

	// ServerPort is the port the HTTP server listens on.
	ServerPort string
}

// Load reads configuration from environment variables and fails loudly if
// anything required is missing, rather than silently falling back to a
// default that could mask a misconfigured environment.
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set (see .env.example)")
	}

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}

	return &Config{
		DatabaseURL: dbURL,
		ServerPort:  port,
	}, nil
}
