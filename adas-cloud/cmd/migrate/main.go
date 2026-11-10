// Command migrate applies every pending database migration and exits (task C-005).
//
// It runs before the server, never inside it:
//   - Stage 1/2: docker-entrypoint.sh runs it when RUN_MIGRATIONS=true.
//   - Stage 3: a one-off Kubernetes Job runs it once per deploy.
//   - By hand: `go run ./cmd/migrate` (needs DATABASE_URL).
//
// Running it again when nothing is pending is safe and does nothing.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/config"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database"
)

func main() {
	// All the work is in run(), which returns an error instead of calling
	// log.Fatal itself: log.Fatal exits immediately and would skip the deferred
	// connectionPool.Close(), leaving the connection for Postgres to clean up.
	if err := run(); err != nil {
		log.Printf("migrate failed: %v", err)
		os.Exit(1)
	}
}

func run() error {
	// Ctrl+C, or Kubernetes stopping the Job (SIGTERM), cancels the context.
	// The migration that is running then rolls back as a whole, instead of the
	// process being killed in the middle of it.
	cntx, stopListeningForSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopListeningForSignals()

	configuration, err := config.Load()
	if err != nil {
		return err
	}

	connectionPool, err := database.Connect(configuration.DatabaseURL)
	if err != nil {
		return err
	}
	defer connectionPool.Close()

	appliedFileNames, err := database.Migrate(cntx, connectionPool)
	if err != nil {
		return err
	}

	// Never prints DATABASE_URL: it contains the database password.
	if len(appliedFileNames) == 0 {
		log.Printf("migrate: database already up to date")
		return nil
	}
	for _, appliedFileName := range appliedFileNames {
		log.Printf("migrate: applied %s", appliedFileName)
	}
	return nil
}
