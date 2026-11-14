// Package db opens the AIvalon SQLite database and applies pending goose
// migrations on startup. See docs/DESIGN.md ("Database schema (draft)") for
// the schema and the reasoning behind the pure-Go driver and the 0600 file
// permission.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open creates (if needed) the SQLite file at path with 0600 permissions,
// applies any pending migrations, and returns a ready-to-use *sql.DB.
func Open(path string) (*sql.DB, error) {
	// Create the file ourselves first so we control its permissions from the
	// start; the sqlite driver would otherwise create it with the process's
	// default umask, which is not guaranteed to be restrictive.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("creating database file: %w", err)
		}
		f.Close()
	} else if err := os.Chmod(path, 0o600); err != nil {
		// Existing file: re-assert the permission rather than trust whatever
		// created it originally.
		return nil, fmt.Errorf("securing database file permissions: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, fmt.Errorf("setting goose dialect: %w", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return nil, fmt.Errorf("applying migrations: %w", err)
	}

	return sqlDB, nil
}
