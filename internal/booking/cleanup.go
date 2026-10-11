package booking

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// CleanupInterval is how often expired Idempotency-Keys are deleted.
const CleanupInterval = time.Hour

// undefinedTable is the PostgreSQL SQLSTATE for a missing table.
const undefinedTable = "42P01"

// CleanupKeys deletes the Idempotency-Keys older than KeyTTL and returns how
// many went. Lookups already ignore expired keys; this only bounds the
// table.
func (s *Service) CleanupKeys(ctx context.Context) (int64, error) {
	return s.store.DeleteExpiredIdempotencyKeys(ctx, s.now().Add(-KeyTTL))
}

// RunKeyCleanup runs CleanupKeys now and then every interval until ctx is
// done. Every replica may run it: the delete is idempotent.
func (s *Service) RunKeyCleanup(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := s.CleanupKeys(ctx)
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == undefinedTable:
			// The server started before `make migrate`; the next run cleans up.
			logger.WarnContext(ctx, "idempotency key cleanup skipped: migrations not applied yet")
		case err != nil && ctx.Err() == nil:
			logger.ErrorContext(ctx, "idempotency key cleanup failed", "error", err)
		case n > 0:
			logger.InfoContext(ctx, "idempotency keys expired", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
