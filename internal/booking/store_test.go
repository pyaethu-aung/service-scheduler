package booking

import (
	"testing"
	"time"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
)

func TestDayLockKey(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	got, err := dayLockKey(db.BookAppointmentParams{DealershipID: dealershipID, DayStart: day})
	if want := dealershipID.String() + "|2026-10-19T17:00:00Z"; err != nil || got != want {
		t.Errorf("dayLockKey = %q, %v; want %q (the same instant in any offset gives the same key)", got, err, want)
	}
	if _, err := dayLockKey(db.BookAppointmentParams{DealershipID: dealershipID, DayStart: "2026-10-20"}); err == nil {
		t.Error("a day start that is not a time.Time was accepted")
	}
}
