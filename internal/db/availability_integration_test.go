//go:build integration

package db_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
)

func TestListActiveBayIDs_SkipsInactiveAndOtherDealerships(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	inactive := uuid.New()
	if err := f.q.UpsertServiceBay(ctx, db.UpsertServiceBayParams{
		ID: inactive, DealershipID: f.dealership, Name: "Closed bay", Active: false,
	}); err != nil {
		t.Fatal(err)
	}
	other := newFixture(t) // a second dealership's bays must not appear

	got, err := f.q.ListActiveBayIDs(ctx, f.dealership)
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{f.bays[0], f.bays[1]}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if !slices.Equal(got, want) {
		t.Errorf("bays = %v, want %v (not inactive %s or %v)", got, want, inactive, other.bays)
	}
}

func TestListQualifiedTechnicianIDs_FiltersQualificationAndActive(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	unqualified, inactive := uuid.New(), uuid.New()
	for _, tc := range []db.UpsertTechnicianParams{
		{ID: unqualified, DealershipID: f.dealership, Name: "Unqualified", Active: true},
		{ID: inactive, DealershipID: f.dealership, Name: "On leave", Active: false},
	} {
		if err := f.q.UpsertTechnician(ctx, tc); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.q.UpsertTechnicianQualification(ctx, db.UpsertTechnicianQualificationParams{
		TechnicianID: inactive, ServiceTypeID: f.serviceType,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := f.q.ListQualifiedTechnicianIDs(ctx, db.ListQualifiedTechnicianIDsParams{
		DealershipID: f.dealership, ServiceTypeID: f.serviceType,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{f.techs[0], f.techs[1]}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if !slices.Equal(got, want) {
		t.Errorf("technicians = %v, want %v", got, want)
	}
}

func TestListConfirmedAppointmentsInRange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()

	before := f.mustBook(t, f.bays[0], f.techs[0], at(7, 0), at(8, 0))   // touches the window start
	inside := f.mustBook(t, f.bays[0], f.techs[0], at(9, 0), at(10, 0))  // inside
	spanning := f.mustBook(t, f.bays[1], f.techs[1], at(7, 30), at(9, 0)) // crosses the window start
	cancelled := f.mustBook(t, f.bays[1], f.techs[1], at(10, 0), at(11, 0))
	if _, err := f.q.CancelAppointment(ctx, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	f.mustBook(t, f.bays[0], f.techs[0], at(12, 0), at(13, 0)) // starts at the window end

	got, err := f.q.ListConfirmedAppointmentsInRange(ctx, db.ListConfirmedAppointmentsInRangeParams{
		DealershipID: f.dealership, WindowStart: at(8, 0), WindowEnd: at(12, 0),
	})
	if err != nil {
		t.Fatal(err)
	}

	type row struct{ bay, tech uuid.UUID }
	var rows []row
	for _, r := range got {
		rows = append(rows, row{r.BayID, r.TechnicianID})
		if r.StartsAt.Equal(before.StartsAt) || r.StartsAt.Equal(cancelled.StartsAt) || r.StartsAt.Equal(at(12, 0)) {
			t.Errorf("unexpected appointment %s-%s in [08:00, 12:00)", r.StartsAt.Format("15:04"), r.EndsAt.Format("15:04"))
		}
	}
	want := []row{{spanning.BayID, spanning.TechnicianID}, {inside.BayID, inside.TechnicianID}}
	if !slices.Equal(rows, want) {
		t.Errorf("rows = %v, want the spanning and inside appointments", rows)
	}
}

func TestGetOpeningHours_ClosedDayHasNoRow(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, err := f.q.GetOpeningHours(t.Context(), db.GetOpeningHoursParams{DealershipID: f.dealership, Weekday: 0})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("closed day: error = %v, want pgx.ErrNoRows", err)
	}
}
