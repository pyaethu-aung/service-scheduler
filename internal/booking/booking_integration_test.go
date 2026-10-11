//go:build integration

package booking_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/booking"
	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/dbtest"
	"github.com/pyaethu-aung/service-scheduler/internal/seed"
)

var longAgo = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func newService(t *testing.T) (*booking.Service, *booking.PGStore) {
	t.Helper()
	pool := dbtest.SeededPool(t)
	store := booking.NewPGStore(pool)
	avail := availability.New(db.New(pool), 15*time.Minute, longAgo)
	return booking.New(store, avail, 15*time.Minute, longAgo), store
}

// londonRequest books an oil change at the London site (GMT in November).
func londonRequest(key, startsAt string) booking.Request {
	start, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		panic(err)
	}
	return booking.Request{
		IdempotencyKey: key,
		DealershipID:   seed.DealershipLondon,
		CustomerID:     seed.CustomerOlive,
		VehicleID:      seed.VehicleOlive,
		ServiceTypeID:  seed.ServiceOilChange,
		StartsAt:       start,
	}
}

func TestBook_BooksAndReplays(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	key := uuid.NewString()
	req := londonRequest(key, "2026-11-03T10:00:00Z") // Tuesday 10:00 GMT

	first, err := svc.Book(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	a := first.Appointment
	if first.Replayed || a.Status != "confirmed" || a.Dealership.ID != seed.DealershipLondon ||
		a.ServiceType.Code != "OIL_CHANGE" || a.Bay.Name == "" || a.Technician.Name == "" ||
		!a.StartsAt.Equal(req.StartsAt) || a.EndsAt.Sub(a.StartsAt) != time.Hour {
		t.Fatalf("first booking = %+v", first)
	}

	second, err := svc.Book(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.Appointment.ID != a.ID {
		t.Errorf("replay = %+v, want appointment %s", second, a.ID)
	}

	other := londonRequest(key, "2026-11-03T11:00:00Z")
	var ve *booking.ValidationError
	if _, err := svc.Book(t.Context(), other); !errors.As(err, &ve) || ve.Params[0].Name != "Idempotency-Key" {
		t.Errorf("reused key error = %v, want an Idempotency-Key ValidationError", err)
	}
}

func TestBook_FullTimeIs409WithSuggestions(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)

	// Three bays in London: three bookings at 10:00 fill them.
	for range 3 {
		if _, err := svc.Book(t.Context(), londonRequest(uuid.NewString(), "2026-11-04T10:00:00Z")); err != nil {
			t.Fatal(err)
		}
	}
	_, err := svc.Book(t.Context(), londonRequest(uuid.NewString(), "2026-11-04T10:00:00Z"))
	var ue *booking.UnavailableError
	if !errors.As(err, &ue) || ue.Reason != booking.ReasonNoBay {
		t.Fatalf("fourth booking error = %v, want no_bay_available", err)
	}
	// 09:00 and 11:00 only touch the full hour, so they are the nearest free
	// starts, with 08:45 next (tie with 11:15 goes to the earlier).
	want := []string{"2026-11-04T08:45:00Z", "2026-11-04T09:00:00Z", "2026-11-04T11:00:00Z"}
	if len(ue.Suggested) != 3 {
		t.Fatalf("suggested = %v, want %v", ue.Suggested, want)
	}
	for i, w := range want {
		if ue.Suggested[i].Format(time.RFC3339) != w {
			t.Errorf("suggested = %v, want %v", ue.Suggested, want)
			break
		}
	}
}

func TestPGStore_Book(t *testing.T) {
	t.Parallel()
	_, store := newService(t)
	ctx := t.Context()
	liveSince := time.Now().Add(-booking.KeyTTL)
	params := func(start string) db.BookAppointmentParams {
		s, _ := time.Parse(time.RFC3339, start)
		return db.BookAppointmentParams{
			DealershipID: seed.DealershipLondon, CustomerID: seed.CustomerSam, VehicleID: seed.VehicleSam,
			ServiceTypeID: seed.ServiceOilChange, StartsAt: s, EndsAt: s.Add(time.Hour),
			DayStart: s.Truncate(24 * time.Hour), DayEnd: s.Truncate(24 * time.Hour).Add(24 * time.Hour),
		}
	}

	key := uuid.NewString()
	id, err := store.Book(ctx, params("2026-11-05T10:00:00Z"), key, "hash", liveSince)
	if err != nil || id == uuid.Nil {
		t.Fatalf("book = %s, %v", id, err)
	}

	// The key is live, so a second claim fails and its appointment is rolled
	// back: the 11:00 slot stays empty.
	if _, err := store.Book(ctx, params("2026-11-05T11:00:00Z"), key, "hash", liveSince); !errors.Is(err, booking.ErrKeyClaimed) {
		t.Fatalf("second claim error = %v, want ErrKeyClaimed", err)
	}
	rows, err := store.ListConfirmedAppointmentsInRange(ctx, db.ListConfirmedAppointmentsInRangeParams{
		DealershipID: seed.DealershipLondon,
		WindowStart:  time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC),
		WindowEnd:    time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
	})
	if err != nil || len(rows) != 0 {
		t.Errorf("11:00 has %d confirmed rows (%v), want 0 after the rollback", len(rows), err)
	}

	// Fill the three bays, then nothing is free: pgx.ErrNoRows.
	for range 2 {
		if _, err := store.Book(ctx, params("2026-11-05T10:00:00Z"), uuid.NewString(), "hash", liveSince); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Book(ctx, params("2026-11-05T10:00:00Z"), uuid.NewString(), "hash", liveSince); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("full slot error = %v, want pgx.ErrNoRows", err)
	}
}

func TestPGStore_Book_SerializesPerDealershipDay(t *testing.T) {
	t.Parallel()
	pool := dbtest.SeededPool(t)
	store := booking.NewPGStore(pool)
	liveSince := time.Now().Add(-booking.KeyTTL)
	params := func(day time.Time) db.BookAppointmentParams {
		start := day.Add(10 * time.Hour)
		return db.BookAppointmentParams{
			DealershipID: seed.DealershipSaigon, CustomerID: seed.CustomerAn, VehicleID: seed.VehicleAnSedan,
			ServiceTypeID: seed.ServiceOilChange, StartsAt: start, EndsAt: start.Add(time.Hour),
			DayStart: day, DayEnd: day.Add(24 * time.Hour),
		}
	}
	held := time.Date(2026, 11, 9, 17, 0, 0, 0, time.UTC) // Tuesday 2026-11-10 in Ho Chi Minh City
	other := held.Add(24 * time.Hour)

	// Another transaction holds the lock for that dealership and day.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if err := db.New(tx).LockDealershipDay(t.Context(), seed.DealershipSaigon.String()+"|"+held.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	// A booking on the same day waits for it...
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if _, err := store.Book(ctx, params(held), uuid.NewString(), "hash", liveSince); err == nil {
		t.Fatal("booking on the locked day did not wait for the lock")
	}
	// ...while another day goes straight through.
	if _, err := store.Book(t.Context(), params(other), uuid.NewString(), "hash", liveSince); err != nil {
		t.Fatalf("booking on another day: %v", err)
	}
	// Once released, the same day books.
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Book(t.Context(), params(held), uuid.NewString(), "hash", liveSince); err != nil {
		t.Fatalf("booking after the lock was released: %v", err)
	}
}

func TestCancel_FreesTheSlot(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	const start = "2026-11-10T10:00:00Z" // Tuesday, London (GMT)

	var booked []booking.Appointment
	for range 3 {
		res, err := svc.Book(t.Context(), londonRequest(uuid.NewString(), start))
		if err != nil {
			t.Fatal(err)
		}
		booked = append(booked, res.Appointment)
	}
	var ue *booking.UnavailableError
	if _, err := svc.Book(t.Context(), londonRequest(uuid.NewString(), start)); !errors.As(err, &ue) {
		t.Fatalf("fourth booking error = %v, want 409 while all bays are taken", err)
	}

	first, err := svc.Cancel(t.Context(), booked[1].ID)
	if err != nil || first.Status != "cancelled" {
		t.Fatalf("cancel = %+v, %v", first, err)
	}
	second, err := svc.Cancel(t.Context(), booked[1].ID)
	if err != nil || second != first {
		t.Errorf("second cancel = %+v, %v; want the same state", second, err)
	}
	if got, err := svc.Get(t.Context(), booked[1].ID); err != nil || got != first {
		t.Errorf("get after cancel = %+v, %v", got, err)
	}

	// The freed bay takes the next booking for the same time.
	res, err := svc.Book(t.Context(), londonRequest(uuid.NewString(), start))
	if err != nil {
		t.Fatalf("booking after cancel: %v", err)
	}
	if res.Appointment.Bay.ID != booked[1].Bay.ID {
		t.Errorf("rebooked bay %s, want the freed bay %s", res.Appointment.Bay.ID, booked[1].Bay.ID)
	}

	var nf *booking.NotFoundError
	if _, err := svc.Cancel(t.Context(), uuid.New()); !errors.As(err, &nf) {
		t.Errorf("cancel unknown: error = %v", err)
	}
}
