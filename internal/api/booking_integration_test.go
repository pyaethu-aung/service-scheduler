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

// Cancel through the full stack: the slot frees, a second cancel returns
// the same body, and GET shows the cancelled state.
func TestContract_CancelSeeded(t *testing.T) {
	t.Parallel()

	longAgo := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	pool := dbtest.SeededPool(t)
	avail := availability.New(db.New(pool), 15*time.Minute, longAgo)
	bookings := booking.New(booking.NewPGStore(pool), avail, 15*time.Minute, longAgo)
	h := NewRouter(NewServer(avail, bookings, slog.New(slog.DiscardHandler)))
	c := newContract(t)

	res, err := bookings.Book(t.Context(), booking.Request{
		IdempotencyKey: uuid.NewString(), DealershipID: seed.DealershipLondon, CustomerID: seed.CustomerSam,
		VehicleID: seed.VehicleSam, ServiceTypeID: seed.ServiceOilChange,
		StartsAt: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/appointments/" + res.Appointment.ID.String()
	do := func(method, p string) *httptest.ResponseRecorder {
		return c.do(t, h, httptest.NewRequestWithContext(t.Context(), method, p, nil))
	}

	first := do(http.MethodPost, path+"/cancel")
	second := do(http.MethodPost, path+"/cancel")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel: %d %s", first.Code, first.Body)
	}
	if second.Code != http.StatusOK || second.Body.String() != first.Body.String() {
		t.Errorf("second cancel: %d %s, want the same 200 body", second.Code, second.Body)
	}
	if got := do(http.MethodGet, path); got.Code != http.StatusOK || got.Body.String() != first.Body.String() {
		t.Errorf("get after cancel: %d %s", got.Code, got.Body)
	}
	if got := do(http.MethodGet, "/v1/appointments/"+uuid.NewString()); got.Code != http.StatusNotFound {
		t.Errorf("get unknown: %d", got.Code)
	}
}
