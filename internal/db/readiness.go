package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/pyaethu-aung/service-scheduler/migrations"
)

// Readiness checks that the database answers and that it is migrated to the
// latest migration this build embeds, for /readyz.
type Readiness struct {
	pool     *pgxpool.Pool
	provider *goose.Provider
	want     int64
}

// NewReadiness prepares the checks; it does not touch the database.
func NewReadiness(pool *pgxpool.Pool) (*Readiness, error) {
	provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS)
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	sources := provider.ListSources()
	if len(sources) == 0 {
		return nil, fmt.Errorf("no embedded migrations")
	}
	return &Readiness{pool: pool, provider: provider, want: sources[len(sources)-1].Version}, nil
}

// Ready returns the database check's error and the migration check's
// error; both nil means ready.
func (r *Readiness) Ready(ctx context.Context) (database, migration error) {
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err), fmt.Errorf("not checked: database unreachable")
	}
	got, err := r.provider.GetDBVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("read migration version: %w", err)
	}
	if got != r.want {
		return nil, fmt.Errorf("at version %d, this build expects %d", got, r.want)
	}
	return nil, nil
}
