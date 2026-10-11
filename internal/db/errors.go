package db

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrSlotTaken means a booking lost a race to a concurrent transaction for
// the same bay or technician and wrote nothing. PostgreSQL reports the loss
// as 23P01 when an exclusion constraint (appointments_no_bay_overlap or
// appointments_no_technician_overlap) sees the winner, or as 40P01 when
// racing inserts into those constraints wait on each other and one is
// chosen as the deadlock victim. Retrying in a new transaction sees the
// winner.
var ErrSlotTaken = errors.New("slot taken by a concurrent booking")

// PostgreSQL SQLSTATEs that mean a lost booking race.
const (
	exclusionViolation = "23P01"
	deadlockDetected   = "40P01"
)

// Translate maps PostgreSQL errors to the store's domain errors and returns
// any other error unchanged.
func Translate(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == exclusionViolation || pgErr.Code == deadlockDetected) {
		return fmt.Errorf("%w (%s)", ErrSlotTaken, pgErr.ConstraintName)
	}
	return err
}
