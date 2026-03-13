package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"btc-giftcard/pkg/logger"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type Config struct {
	Host            string
	Port            string
	User            string
	Password        string
	DB              string
	SslMode         string
	MaxConns        int
	MinConns        int
	MaxConnLifetime int
	MaxConnIdleTime int
}

type DB struct {
	pool          *pgxpool.Pool
	migrationPath string // Path to migrations directory
}

func NewDB(cfg Config) (*DB, error) {
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DB, cfg.SslMode)
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		logger.Error("Failed to parse connection config", zap.Error(err))
		return nil, err
	}

	// Configure connection pool
	config.MaxConns = int32(cfg.MaxConns)
	config.MinConns = int32(cfg.MinConns)
	config.MaxConnLifetime = time.Duration(cfg.MaxConnLifetime) * time.Minute
	config.MaxConnIdleTime = time.Duration(cfg.MaxConnIdleTime) * time.Minute

	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		logger.Error("Failed to create db connection pool", zap.Error(err))
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		logger.Error("Database ping failed", zap.Error(err))
		return nil, err
	}

	logger.Info("Database connection pool created successfully")

	return &DB{
		pool:          pool,
		migrationPath: "file://migrations", // Default path for production
	}, nil
}

// Ping checks if the database is reachable
func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

// RunMigrations uses golang-migrate to execute database migrations
func (db *DB) RunMigrations() error {
	// Get underlying *sql.DB from pgxpool for golang-migrate
	// golang-migrate uses database/sql interface
	connStr := db.pool.Config().ConnString()
	sqlDB, err := sql.Open("postgres", connStr)
	if err != nil {
		logger.Error("Failed to open sql.DB for migrations", zap.Error(err))
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer sqlDB.Close()

	// Create postgres driver instance
	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		logger.Error("Failed to create postgres driver", zap.Error(err))
		return fmt.Errorf("failed to create postgres driver: %w", err)
	}

	// Create migrate instance
	m, err := migrate.NewWithDatabaseInstance(
		db.migrationPath, // Source: read from migrations/ directory
		"postgres",       // Database name
		driver,           // Database driver instance
	)
	if err != nil {
		logger.Error("Failed to create migrate instance", zap.Error(err))
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	// Run all pending migrations
	logger.Info("Running database migrations...")
	if err := m.Up(); err != nil {
		if err == migrate.ErrNoChange {
			logger.Info("No new migrations to apply")
			return nil
		}
		logger.Error("Migration failed", zap.Error(err))
		return fmt.Errorf("migration failed: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		logger.Error("Failed to get migration version", zap.Error(err))
		return fmt.Errorf("failed to get migration version: %w", err)
	}

	if dirty {
		logger.Error("Database is in dirty state", zap.Uint("version", version))
		return fmt.Errorf("database is in dirty state at version %d", version)
	}

	logger.Info("Migrations completed successfully", zap.Uint("version", version))
	return nil
}

// RunInTx executes fn inside a single DB transaction.
// fn receives a Querier scoped to the transaction — pass it to WithTx on any
// repository to make that repository use the same connection.
// If fn returns an error the transaction is rolled back.
// If fn returns nil the transaction is committed.
func (db *DB) RunInTx(ctx context.Context, fn func(q Querier) error) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) // no-op if Commit already succeeded

	if err := fn(tx); err != nil {
		return err // Rollback fires via defer
	}

	return tx.Commit(ctx)
}

// Close gracefully shuts down the connection pool
func (db *DB) Close() {
	if db.pool != nil {
		logger.Info("Closing database connection pool")
		db.pool.Close()
	}
}
