package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps a *sql.DB with application-specific query methods.
type DB struct {
	db *sql.DB
}

// Open creates or opens a SQLite database at the given path with WAL mode
// and a single-writer connection pool.
func Open(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create db directory %s: %w", dir, err)
	}
	return openWithDSN(dbPath)
}

// OpenMemory opens an in-memory SQLite database for testing.
func OpenMemory() (*DB, error) {
	return openWithDSN("file::memory:?cache=shared")
}

func openWithDSN(dsn string) (*DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dsn, err)
	}

	// Single writer — SQLite allows only one writer at a time.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	return &DB{db: db}, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// migrate executes the schema DDL. Safe to run repeatedly (IF NOT EXISTS).
func migrate(db *sql.DB) error {
	ctx := context.Background()

	// Enable WAL mode and busy timeout on the initial connection.
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("set busy timeout: %w", err)
	}

	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("exec schema: %w", err)
	}

	return nil
}
