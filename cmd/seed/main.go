// Command seed loads the idempotent development data into DATABASE_URL.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/seed"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(context.Background(), os.Getenv, logger); err != nil {
		logger.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, logger *slog.Logger) error {
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	// One transaction: a failed seed leaves the database as it was.
	if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		return seed.Run(ctx, db.New(tx))
	}); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	counts, err := db.New(conn).CountReferenceData(ctx)
	if err != nil {
		return fmt.Errorf("count: %w", err)
	}
	logger.InfoContext(ctx, "seeded",
		"dealerships", counts.Dealerships,
		"opening_hours", counts.OpeningHours,
		"service_types", counts.ServiceTypes,
		"service_bays", counts.ServiceBays,
		"technicians", counts.Technicians,
		"technician_qualifications", counts.TechnicianQualifications,
		"customers", counts.Customers,
		"vehicles", counts.Vehicles,
	)
	return nil
}
