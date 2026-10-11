//go:build integration

package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/booking"
	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/dbtest"
	"github.com/pyaethu-aung/service-scheduler/internal/seed"
)

// The full booking stack against seeded postgres:18: every real response
// is validated against the contract.
func TestContract_BookingSeeded(t *testing.T) {
	t.Parallel()

	longAgo := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	pool := dbtest.SeededPool(t)
	avail := availability.New(db.New(pool), 15*time.Minute, longAgo)
	bookings := booking.New(booking.NewPGStore(pool), avail, 15*time.Minute, longAgo)
	h := NewRouter(NewServer(avail, bookings, slog.New(slog.DiscardHandler)))
	c := newContract(t)

	// Saigon, Saturday 2026-10-31 10:00-12:00 local: a brake service (120
	// minutes) fills a bay for the last possible slot.
	body := `{"dealershipId":"` + seed.DealershipSaigon.String() + `","customerId":"` + seed.CustomerMai.String() +
		`","vehicleId":"` + seed.VehicleMai.String() + `","serviceTypeId":"` + seed.ServiceBrakes.String() +
		`","startsAt":"2026-10-31T10:00:00+07:00"}`
	post := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/appointments", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		return c.do(t, h, req)
	}

	key := uuid.NewString()
	if rec := post(key); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"startsAt":"2026-10-31T03:00:00Z"`) {
		t.Fatalf("first booking: %d %s", rec.Code, rec.Body)
	}
	if rec := post(key); rec.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body)
	}
	// Two more fill the three bays; the fourth is a 409.
	for range 2 {
		if rec := post(uuid.NewString()); rec.Code != http.StatusCreated {
			t.Fatalf("filling bays: %d %s", rec.Code, rec.Body)
		}
	}
	rec := post(uuid.NewString())
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"reason":"no_bay_available"`) {
		t.Fatalf("full slot: %d %s", rec.Code, rec.Body)
	}
}
