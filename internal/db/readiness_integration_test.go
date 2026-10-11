//go:build integration

package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/dbtest"
)

func TestReadiness(t *testing.T) {
	t.Parallel()

	r, err := db.NewReadiness(dbtest.Pool(t))
	if err != nil {
		t.Fatal(err)
	}
	if dbErr, migErr := r.Ready(t.Context()); dbErr != nil || migErr != nil {
		t.Fatalf("migrated database: %v / %v, want ready", dbErr, migErr)
	}

	// The container's maintenance database has no migrations applied.
	unmigrated, err := pgxpool.New(t.Context(), strings.Replace(dbtest.DSN(t), "/scheduler?", "/postgres?", 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unmigrated.Close)
	r, err = db.NewReadiness(unmigrated)
	if err != nil {
		t.Fatal(err)
	}
	if dbErr, migErr := r.Ready(t.Context()); dbErr != nil || migErr == nil {
		t.Errorf("unmigrated database: %v / %v, want a migration error only", dbErr, migErr)
	}

	// A cancelled context cannot reach the database at all.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if dbErr, migErr := r.Ready(ctx); dbErr == nil || migErr == nil {
		t.Errorf("unreachable database: %v / %v, want both checks to fail", dbErr, migErr)
	}
}
