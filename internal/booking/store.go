package booking

import (
	"context"
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
// isolation level, prevent double booking (Section 5.3).
func (s *PGStore) Book(ctx context.Context, arg db.BookAppointmentParams, key, requestHash string, liveSince time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.WithTx(tx)
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
