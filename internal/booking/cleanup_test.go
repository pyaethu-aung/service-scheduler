package booking

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
)

func TestCleanupKeys_DeletesOnlyExpired(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.keys["old"] = db.IdempotencyKey{Key: "old", AppointmentID: uuid.New(), CreatedAt: now.Add(-KeyTTL - time.Minute)}
	store.keys["fresh"] = db.IdempotencyKey{Key: "fresh", AppointmentID: uuid.New(), CreatedAt: now.Add(-KeyTTL + time.Minute)}

	n, err := newService(store, &fakeAvailability{}).CleanupKeys(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("CleanupKeys = %d, %v; want 1", n, err)
	}
	if _, ok := store.keys["fresh"]; !ok {
		t.Error("a live key was deleted")
	}
	if _, ok := store.keys["old"]; ok {
		t.Error("an expired key survived")
	}
}

func TestRunKeyCleanup_RunsUntilCancelled(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.keys["old"] = db.IdempotencyKey{Key: "old", CreatedAt: now.Add(-48 * time.Hour)}
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	svc := newService(store, &fakeAvailability{})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); svc.RunKeyCleanup(ctx, time.Millisecond, logger) }()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunKeyCleanup did not stop when its context was cancelled")
	}
	if _, ok := store.keys["old"]; ok || !strings.Contains(logs.String(), "deleted=1") {
		t.Errorf("expired key not removed on the first run; logs: %s", logs.String())
	}
}

func TestRunKeyCleanup_LogsFailures(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.failOn = "DeleteExpiredIdempotencyKeys"
	var logs strings.Builder
	svc := newService(store, &fakeAvailability{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	svc.RunKeyCleanup(ctx, time.Millisecond, slog.New(slog.NewTextHandler(&logs, nil)))
	if !strings.Contains(logs.String(), "cleanup failed") {
		t.Errorf("failure not logged: %s", logs.String())
	}
}

type unmigratedStore struct{ *fakeStore }

func (unmigratedStore) DeleteExpiredIdempotencyKeys(context.Context, time.Time) (int64, error) {
	return 0, &pgconn.PgError{Code: "42P01", Message: `relation "idempotency_keys" does not exist`}
}

func TestRunKeyCleanup_BeforeMigrationsIsAWarning(t *testing.T) {
	t.Parallel()
	var logs strings.Builder
	svc := newService(newFakeStore(), &fakeAvailability{})
	svc.store = unmigratedStore{newFakeStore()}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	svc.RunKeyCleanup(ctx, time.Hour, slog.New(slog.NewTextHandler(&logs, nil)))
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "migrations not applied yet") ||
		strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("logs = %s, want a warning and no error", logs.String())
	}
}
