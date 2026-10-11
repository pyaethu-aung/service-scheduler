package db

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestTranslate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		slotTaken bool
	}{
		{"exclusion violation", &pgconn.PgError{Code: "23P01", ConstraintName: "appointments_no_bay_overlap"}, true},
		{"deadlock between racing inserts", &pgconn.PgError{Code: "40P01"}, true},
		{"wrapped exclusion violation", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23P01"}), true},
		{"unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := errors.Is(Translate(tt.err), ErrSlotTaken); got != tt.slotTaken {
				t.Errorf("Translate(%v) is ErrSlotTaken = %v, want %v", tt.err, got, tt.slotTaken)
			}
		})
	}
}
