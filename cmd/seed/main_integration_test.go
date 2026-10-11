//go:build integration

package main

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/dbtest"
)

// The test binary owns its own container, so the counts below are exactly
// the seed data.
func TestRun_SeedsAndIsIdempotent(t *testing.T) {
	dsn := dbtest.DSN(t)
	env := func(k string) string {
		if k == "DATABASE_URL" {
			return dsn
		}
		return ""
	}
	logger := slog.New(slog.DiscardHandler)

	want := db.CountReferenceDataRow{
		Dealerships:              2,
		OpeningHours:             12, // Monday to Saturday at both sites
		ServiceTypes:             4,
		ServiceBays:              6,
		Technicians:              8,
		TechnicianQualifications: 20,
		Customers:                4,
		Vehicles:                 5,
	}
	q := db.New(dbtest.Pool(t))

	for i := 1; i <= 2; i++ {
		if err := run(t.Context(), env, logger); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		got, err := q.CountReferenceData(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("after run %d: counts = %+v, want %+v", i, got, want)
		}
	}
}

func TestRun_Errors(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	err := run(t.Context(), func(string) string { return "" }, logger)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Errorf("missing DATABASE_URL: error = %v", err)
	}

	err = run(t.Context(), func(string) string { return "::not a url::" }, logger)
	if err == nil || !strings.Contains(err.Error(), "connect") {
		t.Errorf("invalid DATABASE_URL: error = %v", err)
	}

	// The container's maintenance database has no schema, so the first
	// upsert fails and the transaction rolls back.
	unmigrated := strings.Replace(dbtest.DSN(t), "/scheduler?", "/postgres?", 1)
	err = run(t.Context(), func(string) string { return unmigrated }, logger)
	if err == nil || !strings.Contains(err.Error(), "seed: seed service type") {
		t.Errorf("unmigrated database: error = %v", err)
	}
}
