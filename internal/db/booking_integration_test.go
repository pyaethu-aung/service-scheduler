//go:build integration

package db_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
)

func (f fixture) bookParams(start, end time.Time) db.BookAppointmentParams {
	return db.BookAppointmentParams{
		DealershipID:  f.dealership,
		CustomerID:    f.customer,
		VehicleID:     f.vehicle,
		ServiceTypeID: f.serviceType,
		StartsAt:      start,
		EndsAt:        end,
		DayStart:      at(0, 0),
		DayEnd:        at(0, 0).Add(24 * time.Hour),
	}
}

func TestBookAppointment_AssignsFreeBayAndLeastLoadedTechnician(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// techs[0] already has two hours today, techs[1] one: the least-loaded
	// free technician is techs[1] until it is busy at the requested time.
	f.mustBook(t, f.bays[0], f.techs[0], at(8, 0), at(10, 0))
	f.mustBook(t, f.bays[1], f.techs[1], at(8, 0), at(9, 0))

	got, err := f.q.BookAppointment(t.Context(), f.bookParams(at(11, 0), at(12, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if got.TechnicianID != f.techs[1] {
		t.Errorf("technician = %s, want the least-loaded %s", got.TechnicianID, f.techs[1])
	}
	if got.BayID != f.bays[0] && got.BayID != f.bays[1] {
		t.Errorf("bay %s is not one of the dealership's", got.BayID)
	}
	if got.Status != "confirmed" || !got.StartsAt.Equal(at(11, 0)) || !got.EndsAt.Equal(at(12, 0)) {
		t.Errorf("got %+v", got)
	}

	// At 09:00 techs[0] (busy until 10:00) and bays[0] are taken, so the only
	// free pair is bays[1] with techs[1], whatever their load.
	got, err = f.q.BookAppointment(t.Context(), f.bookParams(at(9, 0), at(10, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if got.TechnicianID != f.techs[1] || got.BayID != f.bays[1] {
		t.Errorf("got bay %s technician %s, want the only free pair %s/%s", got.BayID, got.TechnicianID, f.bays[1], f.techs[1])
	}
}

func TestBookAppointment_NoRowWhenNothingIsFree(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Both bays busy: no bay.
	f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	f.mustBook(t, f.bays[1], f.techs[1], at(9, 0), at(10, 0))

	_, err := f.q.BookAppointment(t.Context(), f.bookParams(at(9, 30), at(10, 30)))
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("error = %v, want pgx.ErrNoRows", err)
	}
	free, err := f.q.HasFreeBay(t.Context(), db.HasFreeBayParams{DealershipID: f.dealership, StartsAt: at(9, 30), EndsAt: at(10, 30)})
	if err != nil || free {
		t.Errorf("HasFreeBay = %v, %v; want false", free, err)
	}
	// Touching the busy hour, both bays are free again.
	free, err = f.q.HasFreeBay(t.Context(), db.HasFreeBayParams{DealershipID: f.dealership, StartsAt: at(10, 0), EndsAt: at(11, 0)})
	if err != nil || !free {
		t.Errorf("HasFreeBay at 10:00 = %v, %v; want true", free, err)
	}
}

func TestBookAppointment_NoRowWhenNoTechnicianIsFree(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// A third bay is free, but both qualified technicians are busy.
	extraBay := uuid.New()
	if err := f.q.UpsertServiceBay(t.Context(), db.UpsertServiceBayParams{ID: extraBay, DealershipID: f.dealership, Name: "Bay", Active: true}); err != nil {
		t.Fatal(err)
	}
	f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	f.mustBook(t, f.bays[1], f.techs[1], at(9, 0), at(10, 0))

	_, err := f.q.BookAppointment(t.Context(), f.bookParams(at(9, 0), at(10, 0)))
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("error = %v, want pgx.ErrNoRows", err)
	}
	free, err := f.q.HasFreeBay(t.Context(), db.HasFreeBayParams{DealershipID: f.dealership, StartsAt: at(9, 0), EndsAt: at(10, 0)})
	if err != nil || !free {
		t.Errorf("HasFreeBay = %v, %v; want true (the shortage is technicians)", free, err)
	}
}

func TestTranslate_ExclusionViolationIsErrSlotTaken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	_, err := f.book(t, f.bays[0], f.techs[1], at(9, 0), at(10, 0))
	if !errors.Is(db.Translate(err), db.ErrSlotTaken) {
		t.Fatalf("Translate(%v) is not ErrSlotTaken", err)
	}

	other := errors.New("boom")
	if db.Translate(other) != other { //nolint:errorlint // identity: unrelated errors must come back unwrapped
		t.Error("Translate changed an unrelated error")
	}
	if !errors.Is(db.Translate(pgx.ErrNoRows), pgx.ErrNoRows) {
		t.Error("Translate changed pgx.ErrNoRows")
	}
}

func TestIdempotencyKeys_ClaimLookupAndExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	first := f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	second := f.mustBook(t, f.bays[1], f.techs[1], at(9, 0), at(10, 0))
	key := "key-" + uuid.NewString()
	now := time.Now()
	liveSince := now.Add(-24 * time.Hour)

	n, err := f.q.ClaimIdempotencyKey(ctx, db.ClaimIdempotencyKeyParams{Key: key, RequestHash: "h1", AppointmentID: first.ID, LiveSince: liveSince})
	if err != nil || n != 1 {
		t.Fatalf("first claim = %d, %v; want 1", n, err)
	}
	// A live key is never overwritten: 0 rows means another request owns it.
	n, err = f.q.ClaimIdempotencyKey(ctx, db.ClaimIdempotencyKeyParams{Key: key, RequestHash: "h2", AppointmentID: second.ID, LiveSince: liveSince})
	if err != nil || n != 0 {
		t.Fatalf("second claim = %d, %v; want 0", n, err)
	}
	got, err := f.q.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{Key: key, LiveSince: liveSince})
	if err != nil || got.RequestHash != "h1" || got.AppointmentID != first.ID {
		t.Fatalf("lookup = %+v, %v; want the first claim", got, err)
	}

	// Once expired (live_since after its creation), the key reads as absent
	// and can be claimed again.
	expiredBefore := now.Add(time.Hour)
	if _, err := f.q.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{Key: key, LiveSince: expiredBefore}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired lookup error = %v, want pgx.ErrNoRows", err)
	}
	n, err = f.q.ClaimIdempotencyKey(ctx, db.ClaimIdempotencyKeyParams{Key: key, RequestHash: "h3", AppointmentID: second.ID, LiveSince: expiredBefore})
	if err != nil || n != 1 {
		t.Fatalf("claim over an expired key = %d, %v; want 1", n, err)
	}

	// Cleanup removes it once it is older than live_since.
	deleted, err := f.q.DeleteExpiredIdempotencyKeys(ctx, now.Add(48*time.Hour))
	if err != nil || deleted < 1 {
		t.Fatalf("cleanup = %d, %v; want at least this key", deleted, err)
	}
	if _, err := f.q.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{Key: key, LiveSince: liveSince}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("after cleanup: error = %v, want pgx.ErrNoRows", err)
	}
}

func TestGetAppointmentDetails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	a := f.mustBook(t, f.bays[1], f.techs[0], at(9, 0), at(10, 0))
	got, err := f.q.GetAppointmentDetails(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DealershipID != f.dealership || got.DealershipName != "Test Centre" ||
		got.BayID != f.bays[1] || got.TechnicianID != f.techs[0] ||
		got.ServiceTypeID != f.serviceType || got.ServiceTypeDurationMinutes != 60 ||
		got.CustomerID != f.customer || got.VehicleID != f.vehicle || got.Status != "confirmed" {
		t.Errorf("details = %+v", got)
	}
	if _, err := f.q.GetAppointmentDetails(t.Context(), uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown id: error = %v, want pgx.ErrNoRows", err)
	}
}

func TestGetCustomerAndVehicle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	if id, err := f.q.GetCustomer(t.Context(), f.customer); err != nil || id != f.customer {
		t.Errorf("GetCustomer = %s, %v", id, err)
	}
	v, err := f.q.GetVehicle(t.Context(), f.vehicle)
	if err != nil || v.CustomerID != f.customer {
		t.Errorf("GetVehicle = %+v, %v; want owner %s", v, err, f.customer)
	}
	if _, err := f.q.GetVehicle(t.Context(), uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown vehicle: error = %v", err)
	}
}
