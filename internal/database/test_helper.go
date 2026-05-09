//go:build integration

package database

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// SetupTestDB creates a connection to the test database and runs migrations
// The test database (btcgifter_test) is automatically created by docker-compose
func SetupTestDB(t *testing.T) *DB {
	t.Helper()

	cfg := Config{
		Host:            "localhost",
		Port:            "5432",
		User:            "postgres",
		Password:        "postgres",
		DB:              "btcgifter",
		SslMode:         "disable",
		MaxConns:        5,
		MinConns:        1,
		MaxConnLifetime: 5,
		MaxConnIdleTime: 1,
	}

	db, err := NewDB(cfg)
	require.NoError(t, err, "Failed to connect to test database")

	// Set migration path relative to project root
	// Get current file's directory (internal/database)
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	projectRoot := filepath.Join(dir, "../..") // Go up to project root
	migrationsPath := filepath.Join(projectRoot, "migrations")
	db.migrationPath = "file://" + migrationsPath

	// Run migrations to ensure schema is up to date
	err = db.RunMigrations()
	require.NoError(t, err, "Failed to run migrations on test database")

	return db
}

// CleanupTestDB truncates all tables to ensure clean state between tests
func CleanupTestDB(t *testing.T, db *DB) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Truncate in reverse order due to foreign keys
	tables := []string{"transactions", "cards"}
	for _, table := range tables {
		query := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)
		_, err := db.pool.Exec(ctx, query)
		require.NoError(t, err, "Failed to truncate table %s", table)
	}
}
