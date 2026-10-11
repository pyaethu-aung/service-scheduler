//go:build integration

package availability_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/dbtest"
	"github.com/pyaethu-aung/service-scheduler/internal/seed"
	"github.com/pyaethu-aung/service-scheduler/internal/slots"
)

// Seed bays and technicians at the London dealership (see internal/seed).
var (
	londonBays  = []uuid.UUID{uuid.MustParse("01990000-0000-7000-8000-000000000204"), uuid.MustParse("01990000-0000-7000-8000-000000000205"), uuid.MustParse("01990000-0000-7000-8000-000000000206")}
	aliceMorgan = uuid.MustParse("01990000-0000-7000-8000-000000000305")
	benCarter   = uuid.MustParse("01990000-0000-7000-8000-000000000306")
	chloeHughes = uuid.MustParse("01990000-0000-7000-8000-000000000307")
	longAgo     = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
)

// store returns queries over the shared, seeded database.
func store(t *testing.T) *db.Queries {
	t.Helper()
	return db.New(dbtest.SeededPool(t))
}

func book(t *testing.T, q *db.Queries, bay, tech uuid.UUID, start, end time.Time) {
	t.Helper()
	if _, err := q.CreateAppointment(t.Context(), db.CreateAppointmentParams{
		DealershipID:  seed.DealershipLondon,
		CustomerID:    seed.CustomerOlive,
		VehicleID:     seed.VehicleOlive,
		ServiceTypeID: seed.ServiceOilChange,
		BayID:         bay,
		TechnicianID:  tech,
		StartsAt:      start,
		EndsAt:        end,
	}); err != nil {
		t.Fatalf("book: %v", err)
	}
}

func z(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAvailability_FullDay(t *testing.T) {
	t.Parallel()
	svc := availability.New(store(t), 15*time.Minute, longAgo)

	res, err := svc.Availability(t.Context(), availability.Query{
		DealershipID: seed.DealershipLondon, ServiceTypeID: seed.ServiceOilChange,
		Date: slots.Date{Year: 2026, Month: time.October, Day: 20}, // Tuesday, 08:00-18:00 BST
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Timezone != "Europe/London" || res.Duration != time.Hour {
		t.Errorf("timezone %q duration %s, want Europe/London and 1h", res.Timezone, res.Duration)
	}
	if len(res.Starts) != 37 || !res.Starts[0].Equal(z("2026-10-20T07:00:00Z")) || !res.Starts[36].Equal(z("2026-10-20T16:00:00Z")) {
		t.Fatalf("starts = %d from %v, want 37 from 07:00Z to 16:00Z", len(res.Starts), res.Starts)
	}
}

func TestAvailability_AllBaysBusy(t *testing.T) {
	t.Parallel()
	q := store(t)
	date := slots.Date{Year: 2026, Month: time.October, Day: 21}

	// Every bay is busy 09:00-10:00 BST (08:00-09:00Z), with three different
	// technicians, so a 60-minute service cannot overlap that hour.
	book(t, q, londonBays[0], aliceMorgan, z("2026-10-21T08:00:00Z"), z("2026-10-21T09:00:00Z"))
	book(t, q, londonBays[1], benCarter, z("2026-10-21T08:00:00Z"), z("2026-10-21T09:00:00Z"))
	book(t, q, londonBays[2], chloeHughes, z("2026-10-21T08:00:00Z"), z("2026-10-21T09:00:00Z"))

	res, err := availability.New(q, 15*time.Minute, longAgo).Availability(t.Context(), availability.Query{
		DealershipID: seed.DealershipLondon, ServiceTypeID: seed.ServiceOilChange, Date: date,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Starts) != 37-7 {
		t.Errorf("got %d starts, want 30 (07:15Z to 08:45Z blocked)", len(res.Starts))
	}
	for _, s := range res.Starts {
		if s.After(z("2026-10-21T07:00:00Z")) && s.Before(z("2026-10-21T09:00:00Z")) {
			t.Errorf("start %s overlaps the fully booked hour", s.Format("15:04Z"))
		}
	}
	// Touching starts stay available.
	for _, want := range []time.Time{z("2026-10-21T07:00:00Z"), z("2026-10-21T09:00:00Z")} {
		if !slices.ContainsFunc(res.Starts, want.Equal) {
			t.Errorf("touching start %s missing", want.Format("15:04Z"))
		}
	}
}

func TestAvailability_QualifiedTechniciansBusy(t *testing.T) {
	t.Parallel()
	q := store(t)
	date := slots.Date{Year: 2026, Month: time.October, Day: 22}

	// Only Alice and Chloe can do air conditioning in London. Both are busy
	// 10:00-11:00 BST (09:00-10:00Z); bay C stays free throughout.
	book(t, q, londonBays[0], aliceMorgan, z("2026-10-22T09:00:00Z"), z("2026-10-22T10:00:00Z"))
	book(t, q, londonBays[1], chloeHughes, z("2026-10-22T09:00:00Z"), z("2026-10-22T10:00:00Z"))
	svc := availability.New(q, 15*time.Minute, longAgo)

	airCon, err := svc.Availability(t.Context(), availability.Query{
		DealershipID: seed.DealershipLondon, ServiceTypeID: seed.ServiceAirCon, Date: date,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range airCon.Starts {
		if s.After(z("2026-10-22T07:30:00Z")) && s.Before(z("2026-10-22T10:00:00Z")) {
			t.Errorf("air-con start %s overlaps both qualified technicians' bookings", s.Format("15:04Z"))
		}
	}

	// Ben and Daniel can still change oil at the same time.
	oil, err := svc.Availability(t.Context(), availability.Query{
		DealershipID: seed.DealershipLondon, ServiceTypeID: seed.ServiceOilChange, Date: date,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(oil.Starts, z("2026-10-22T09:00:00Z").Equal) {
		t.Error("oil change at 09:00Z should be free with Ben or Daniel in bay C")
	}
}

func TestAvailability_EmptyResults(t *testing.T) {
	t.Parallel()
	q := store(t)

	// A service type with no qualified technician anywhere.
	orphan := uuid.New()
	if err := q.UpsertServiceType(t.Context(), db.UpsertServiceTypeParams{
		ID: orphan, Code: "ORPHAN_" + orphan.String(), Name: "Nobody can do this", DurationMinutes: 30,
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		now  time.Time
		q    availability.Query
	}{
		{
			name: "closed on Sunday",
			now:  longAgo(),
			q:    availability.Query{DealershipID: seed.DealershipSaigon, ServiceTypeID: seed.ServiceOilChange, Date: slots.Date{Year: 2026, Month: time.October, Day: 18}},
		},
		{
			name: "past date",
			now:  z("2026-10-21T00:00:00Z"),
			q:    availability.Query{DealershipID: seed.DealershipLondon, ServiceTypeID: seed.ServiceOilChange, Date: slots.Date{Year: 2026, Month: time.October, Day: 20}},
		},
		{
			name: "nobody qualified",
			now:  longAgo(),
			q:    availability.Query{DealershipID: seed.DealershipLondon, ServiceTypeID: orphan, Date: slots.Date{Year: 2026, Month: time.October, Day: 20}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := availability.New(q, 15*time.Minute, func() time.Time { return tt.now }).Availability(t.Context(), tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if res.Starts == nil || len(res.Starts) != 0 {
				t.Errorf("starts = %#v, want an empty, non-nil list", res.Starts)
			}
		})
	}
}

func TestAvailability_TodayHidesPastStarts(t *testing.T) {
	t.Parallel()
	now := z("2026-10-20T12:07:00Z") // 13:07 BST
	res, err := availability.New(store(t), 15*time.Minute, func() time.Time { return now }).Availability(t.Context(), availability.Query{
		DealershipID: seed.DealershipLondon, ServiceTypeID: seed.ServiceOilChange,
		Date: slots.Date{Year: 2026, Month: time.October, Day: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Starts) == 0 || !res.Starts[0].Equal(z("2026-10-20T12:15:00Z")) {
		t.Fatalf("first start = %v, want 12:15Z", res.Starts)
	}
}

func TestAvailability_Errors(t *testing.T) {
	t.Parallel()
	q := store(t)

	badZone := uuid.New()
	if err := q.UpsertDealership(t.Context(), db.UpsertDealershipParams{ID: badZone, Name: "Nowhere", Timezone: "Mars/Olympus_Mons"}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	day := slots.Date{Year: 2026, Month: time.October, Day: 20}
	tests := []struct {
		name    string
		ctx     context.Context
		q       availability.Query
		wantIs  error
		wantAny bool
	}{
		{name: "unknown dealership", ctx: t.Context(), q: availability.Query{DealershipID: uuid.New(), ServiceTypeID: seed.ServiceOilChange, Date: day}, wantIs: availability.ErrDealershipNotFound},
		{name: "unknown service type", ctx: t.Context(), q: availability.Query{DealershipID: seed.DealershipLondon, ServiceTypeID: uuid.New(), Date: day}, wantIs: availability.ErrServiceTypeNotFound},
		{name: "invalid stored timezone", ctx: t.Context(), q: availability.Query{DealershipID: badZone, ServiceTypeID: seed.ServiceOilChange, Date: day}, wantAny: true},
		{name: "database error", ctx: cancelled, q: availability.Query{DealershipID: seed.DealershipLondon, ServiceTypeID: seed.ServiceOilChange, Date: day}, wantAny: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := availability.New(q, 15*time.Minute, longAgo).Availability(tt.ctx, tt.q)
			switch {
			case tt.wantIs != nil && !errors.Is(err, tt.wantIs):
				t.Errorf("error = %v, want %v", err, tt.wantIs)
			case tt.wantAny && (err == nil || errors.Is(err, availability.ErrDealershipNotFound) || errors.Is(err, availability.ErrServiceTypeNotFound)):
				t.Errorf("error = %v, want an internal error", err)
			}
		})
	}
}

type countingRecorder struct{ got []int }

func (c *countingRecorder) SlotsReturned(n int) { c.got = append(c.got, n) }

func TestAvailability_RecordsSlotsReturned(t *testing.T) {
	t.Parallel()
	rec := &countingRecorder{}
	svc := availability.New(store(t), 15*time.Minute, longAgo).Instrument(rec)

	if _, err := svc.Availability(t.Context(), availability.Query{
		DealershipID: seed.DealershipLondon, ServiceTypeID: seed.ServiceOilChange,
		Date: slots.Date{Year: 2026, Month: time.October, Day: 27},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Availability(t.Context(), availability.Query{DealershipID: uuid.New(), ServiceTypeID: seed.ServiceOilChange}); err == nil {
		t.Fatal("unknown dealership answered")
	}
	if len(rec.got) != 1 || rec.got[0] != 37 {
		t.Errorf("recorded %v, want one observation of 37 (errors are not recorded)", rec.got)
	}
}
