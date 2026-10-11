//go:build integration

package db_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/dbtest"
)

// fixture is one dealership with two bays, two technicians qualified for one
// service type, and a customer with a vehicle. Every test builds its own, so
// tests never share resources and can run in parallel.
type fixture struct {
	q           *db.Queries
	dealership  uuid.UUID
	serviceType uuid.UUID
	customer    uuid.UUID
	vehicle     uuid.UUID
	bays        [2]uuid.UUID
	techs       [2]uuid.UUID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	q := db.New(dbtest.Pool(t))
	f := fixture{
		q:           q,
		dealership:  uuid.New(),
		serviceType: uuid.New(),
		customer:    uuid.New(),
		vehicle:     uuid.New(),
		bays:        [2]uuid.UUID{uuid.New(), uuid.New()},
		techs:       [2]uuid.UUID{uuid.New(), uuid.New()},
	}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	must(q.UpsertDealership(ctx, db.UpsertDealershipParams{ID: f.dealership, Name: "Test Centre", Timezone: "UTC"}))
	must(q.UpsertServiceType(ctx, db.UpsertServiceTypeParams{
		ID: f.serviceType, Code: "TEST_" + f.serviceType.String(), Name: "Test service", DurationMinutes: 60,
	}))
	for i := range f.bays {
		must(q.UpsertServiceBay(ctx, db.UpsertServiceBayParams{ID: f.bays[i], DealershipID: f.dealership, Name: "Bay", Active: true}))
		must(q.UpsertTechnician(ctx, db.UpsertTechnicianParams{ID: f.techs[i], DealershipID: f.dealership, Name: "Tech", Active: true}))
		must(q.UpsertTechnicianQualification(ctx, db.UpsertTechnicianQualificationParams{
			TechnicianID: f.techs[i], ServiceTypeID: f.serviceType,
		}))
	}
	must(q.UpsertCustomer(ctx, db.UpsertCustomerParams{ID: f.customer, Name: "Test Customer"}))
	must(q.UpsertVehicle(ctx, db.UpsertVehicleParams{
		ID: f.vehicle, CustomerID: f.customer, Vin: "VIN-" + f.vehicle.String(), Make: "Make", Model: "Model",
	}))
	return f
}

// book inserts a confirmed appointment on an explicit bay and technician.
func (f fixture) book(t *testing.T, bay, tech uuid.UUID, start, end time.Time) (db.Appointment, error) {
	t.Helper()
	return f.q.CreateAppointment(t.Context(), db.CreateAppointmentParams{
		DealershipID:  f.dealership,
		CustomerID:    f.customer,
		VehicleID:     f.vehicle,
		ServiceTypeID: f.serviceType,
		BayID:         bay,
		TechnicianID:  tech,
		StartsAt:      start,
		EndsAt:        end,
	})
}

func (f fixture) mustBook(t *testing.T, bay, tech uuid.UUID, start, end time.Time) db.Appointment {
	t.Helper()
	a, err := f.book(t, bay, tech, start, end)
	if err != nil {
		t.Fatalf("book %s-%s: %v", start.Format("15:04"), end.Format("15:04"), err)
	}
	return a
}

// at returns 2026-10-20 hh:mm UTC.
func at(hh, mm int) time.Time {
	return time.Date(2026, 10, 20, hh, mm, 0, 0, time.UTC)
}

// requireExclusionViolation fails unless err is 23P01 raised by constraint.
func requireExclusionViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want a PostgreSQL 23P01 from %s", err, constraint)
	}
	if pgErr.Code != "23P01" || pgErr.ConstraintName != constraint {
		t.Fatalf("got %s from %q, want 23P01 from %q (%s)", pgErr.Code, pgErr.ConstraintName, constraint, pgErr.Message)
	}
}

func TestExclusion_SameBayOverlapRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	// Different technician, same bay, 30 minutes of overlap.
	_, err := f.book(t, f.bays[0], f.techs[1], at(9, 30), at(10, 30))
	requireExclusionViolation(t, err, "appointments_no_bay_overlap")
}

func TestExclusion_SameTechnicianOverlapRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	// Different bay, same technician, fully contained in the first booking.
	_, err := f.book(t, f.bays[1], f.techs[0], at(9, 15), at(9, 45))
	requireExclusionViolation(t, err, "appointments_no_technician_overlap")
}

func TestExclusion_BackToBackIntervalsAllowed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Same bay and technician; [09:00, 10:00) and [10:00, 11:00) only touch.
	first := f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	second := f.mustBook(t, f.bays[0], f.techs[0], at(10, 0), at(11, 0))
	// And the slot before, touching the first from the other side.
	f.mustBook(t, f.bays[0], f.techs[0], at(8, 0), at(9, 0))

	// The generated period is half-open: lower bound included, upper excluded.
	for _, a := range []db.Appointment{first, second} {
		p := a.Period
		if p.LowerType != pgtype.Inclusive || p.UpperType != pgtype.Exclusive {
			t.Errorf("period bounds = %c%c, want [)", p.LowerType, p.UpperType)
		}
		if !p.Lower.Time.Equal(a.StartsAt) || !p.Upper.Time.Equal(a.EndsAt) {
			t.Errorf("period = [%s, %s), want [%s, %s)", p.Lower.Time, p.Upper.Time, a.StartsAt, a.EndsAt)
		}
	}
}

func TestExclusion_DifferentBayAndTechnicianMayOverlap(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	f.mustBook(t, f.bays[1], f.techs[1], at(9, 0), at(10, 0))
}

func TestExclusion_CancelledAppointmentDoesNotBlock(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	original := f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	if _, err := f.book(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0)); err == nil {
		t.Fatal("double booking accepted while the original is confirmed")
	}

	cancelled, err := f.q.CancelAppointment(t.Context(), original.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != "cancelled" || cancelled.CancelledAt == nil {
		t.Fatalf("after cancel: status=%q cancelled_at=%v, want cancelled with a timestamp", cancelled.Status, cancelled.CancelledAt)
	}

	// Same bay, same technician, same period: the cancelled row is outside
	// the partial constraints, so the slot is free again.
	rebooked := f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))
	if rebooked.Status != "confirmed" {
		t.Fatalf("rebooked status = %q, want confirmed", rebooked.Status)
	}

	// The new confirmed row blocks again.
	_, err = f.book(t, f.bays[0], f.techs[1], at(9, 30), at(10, 30))
	requireExclusionViolation(t, err, "appointments_no_bay_overlap")

	// Cancelling twice keeps the original cancellation time.
	again, err := f.q.CancelAppointment(t.Context(), original.ID)
	if err != nil {
		t.Fatalf("second cancel: %v", err)
	}
	if again.CancelledAt == nil || !again.CancelledAt.Equal(*cancelled.CancelledAt) {
		t.Errorf("second cancel moved cancelled_at from %v to %v", cancelled.CancelledAt, again.CancelledAt)
	}
}
