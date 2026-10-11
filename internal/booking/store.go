package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
)

// PGStore is the PostgreSQL Store: the sqlc queries plus the booking
// transaction.
type PGStore struct {
	*db.Queries
	pool *pgxpool.Pool
}

var _ Store = (*PGStore)(nil)

// NewPGStore returns a Store over pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{Queries: db.New(pool), pool: pool}
}

// Book claims a bay and a technician and records the Idempotency-Key in one
// transaction at READ COMMITTED: the exclusion constraints, not the
// isolation level, prevent double booking (Section 5.3). It first takes an
// advisory lock on the dealership's local day, so concurrent bookings that
// could collide queue instead of deadlocking.
func (s *PGStore) Book(ctx context.Context, arg db.BookAppointmentParams, key, requestHash string, liveSince time.Time) (uuid.UUID, error) {
	lock, err := dayLockKey(arg)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.WithTx(tx)
		if err := q.LockDealershipDay(ctx, lock); err != nil {
			return db.Translate(err)
		}
		a, err := q.BookAppointment(ctx, arg)
		if err != nil {
			return db.Translate(err)
		}
		claimed, err := q.ClaimIdempotencyKey(ctx, db.ClaimIdempotencyKeyParams{
			Key: key, RequestHash: requestHash, AppointmentID: a.ID, LiveSince: liveSince,
		})
		if err != nil {
			return err
		}
		if claimed == 0 {
			return ErrKeyClaimed // rolls back this appointment
		}
		id = a.ID
		return nil
	})
	return id, err
}

// dayLockKey names the advisory lock for a booking: its dealership and the
// start of its local day. Bookings on other days or at other dealerships
// never wait on each other.
func dayLockKey(arg db.BookAppointmentParams) (string, error) {
	day, ok := arg.DayStart.(time.Time)
	if !ok {
		return "", fmt.Errorf("booking day start is %T, want time.Time", arg.DayStart)
	}
	return arg.DealershipID.String() + "|" + day.UTC().Format(time.RFC3339), nil
}
