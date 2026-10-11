package db

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrSlotTaken means an insert lost a race: an exclusion constraint
// (appointments_no_bay_overlap or appointments_no_technician_overlap)
// rejected it because a concurrent transaction claimed the same bay or
// technician for an overlapping period. Retrying sees the winner.
var ErrSlotTaken = errors.New("slot taken by a concurrent booking")

// exclusionViolation is the PostgreSQL SQLSTATE for exclusion_violation.
const exclusionViolation = "23P01"

// Translate maps PostgreSQL errors to the store's domain errors and returns
// any other error unchanged.
func Translate(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == exclusionViolation {
		return fmt.Errorf("%w (%s)", ErrSlotTaken, pgErr.ConstraintName)
	}
	return err
}
