//go:build integration

// Package dbtest gives integration tests a real PostgreSQL 18: one
// testcontainers-go container per test binary, migrated with the embedded
// goose migrations. Tests isolate themselves by creating their own rows
// (fresh UUIDs), so they can run in parallel against the one database.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	"github.com/pressly/goose/v3"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/seed"
	"github.com/pyaethu-aung/service-scheduler/migrations"
)

// Image is the PostgreSQL image every integration test runs against.
const Image = "postgres:18"

var (
	once     sync.Once
	dsn      string
	startErr error

	seedOnce sync.Once
	seedErr  error
)

// DSN returns the connection string of the shared, migrated database,
// starting the container on first use. The container is removed by the
// testcontainers reaper when the test binary exits.
func DSN(t testing.TB) string {
	t.Helper()
	once.Do(func() { dsn, startErr = start(context.Background()) })
	if startErr != nil {
		t.Fatalf("dbtest: %v", startErr)
	}
	return dsn
}

// Pool returns a connection pool to the shared database, closed when t ends.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), DSN(t))
	if err != nil {
		t.Fatalf("dbtest: pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// SeededPool is Pool with the internal/seed data loaded once per test
// binary. Tests that book appointments must use dates no other test in the
// package uses, so the shared seed rows stay predictable.
func SeededPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := Pool(t)
	seedOnce.Do(func() {
		seedErr = pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
			return seed.Run(context.Background(), db.New(tx))
		})
	})
	if seedErr != nil {
		t.Fatalf("dbtest: seed: %v", seedErr)
	}
	return pool
}

func start(ctx context.Context) (string, error) {
	ctr, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("scheduler"),
		tcpostgres.WithUsername("scheduler"),
		tcpostgres.WithPassword("scheduler"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", fmt.Errorf("start %s: %w", Image, err)
	}
	conn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", fmt.Errorf("connection string: %w", err)
	}
	if err := migrate(ctx, conn); err != nil {
		return "", err
	}
	return conn, nil
}

func migrate(ctx context.Context, conn string) error {
	sqlDB, err := sql.Open("pgx", conn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
