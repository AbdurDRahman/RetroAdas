package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// The .sql files are copied into the compiled binary at build time, so the
// binary always carries the migrations that match its own code: no separate
// files to ship in the image, and no way for the two to get out of step.
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Migrate applies every migration in database/migrations that this database
// hasn't applied yet, in file-name order, and returns the names of the files it
// applied (empty when the database was already up to date). See task C-005.
//
// Each file runs in its own transaction together with its "applied" record in
// schema_migrations, so a failing file leaves no trace. A Postgres advisory
// lock makes concurrent runs wait for each other instead of clashing.
func Migrate(cntx context.Context, connectionPool *sql.DB) ([]string, error) {
	// The embedded files live under "migrations/"; goose expects them at the top level.
	migrationFiles, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	// Retry the lock every 1 second, for at most 300 tries (5 minutes), so a
	// waiting run notices quickly when the other run is done.
	sessionLocker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return nil, fmt.Errorf("create migration lock: %w", err)
	}

	migrationProvider, err := goose.NewProvider(goose.DialectPostgres, connectionPool, migrationFiles,
		goose.WithSessionLocker(sessionLocker),
		// goose's default name is goose_db_version; ours follows the naming rule.
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}

	migrationResults, err := migrationProvider.Up(cntx)
	if err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	appliedFileNames := []string{}
	for _, migrationResult := range migrationResults {
		appliedFileNames = append(appliedFileNames, path.Base(migrationResult.Source.Path))
	}
	return appliedFileNames, nil
}
