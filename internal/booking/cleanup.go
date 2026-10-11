package booking

import (
	"context"
	"log/slog"
	"time"
)

// CleanupInterval is how often expired Idempotency-Keys are deleted.
const CleanupInterval = time.Hour

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
		switch {
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
