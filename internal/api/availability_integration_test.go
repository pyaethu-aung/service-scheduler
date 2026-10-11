//go:build integration

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/dbtest"
	"github.com/pyaethu-aung/service-scheduler/internal/seed"
)

// The full stack against seeded postgres:18, validated against the contract.
func TestContract_AvailabilitySeeded(t *testing.T) {
	t.Parallel()

	longAgo := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	svc := availability.New(db.New(dbtest.SeededPool(t)), 15*time.Minute, longAgo)
	h := NewRouter(NewServer(svc, slog.New(slog.DiscardHandler)))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		availabilityURL(seed.DealershipSaigon.String(), seed.ServiceBrakes.String(), "2026-10-24"), nil) // Saturday 08:00-12:00
	rec := newContract(t).do(t, h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got Availability
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// A 120-minute brake service fits 08:00 to 10:00 local (UTC+7).
	if got.Timezone != "Asia/Ho_Chi_Minh" || got.DurationMinutes != 120 || len(got.AvailableStarts) != 9 ||
		!got.AvailableStarts[0].Equal(time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)) ||
		!got.AvailableStarts[8].Equal(time.Date(2026, 10, 24, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("got %+v, want 9 starts from 01:00Z to 03:00Z", got)
	}
}
