// Package availability answers "which start times are free on this date?"
// (FR4, docs/system-design.md Section 5.1). It loads the data from the store
// and leaves every time and overlap decision to the slot engine.
package availability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/observability"
	"github.com/pyaethu-aung/service-scheduler/internal/slots"
)

// Errors the HTTP layer maps to 404.
var (
	ErrDealershipNotFound  = errors.New("dealership not found")
	ErrServiceTypeNotFound = errors.New("service type not found")
)

// Date is a local calendar date at the dealership.
type Date = slots.Date

// Query asks for the free starts of one service type on one local date.
type Query struct {
	DealershipID  uuid.UUID
	ServiceTypeID uuid.UUID
	Date          Date
}

// Result is the answer to a Query. Starts are in UTC, ascending, and never
// nil.
type Result struct {
	DealershipID  uuid.UUID
	ServiceTypeID uuid.UUID
	Date          Date
	Timezone      string
	Duration      time.Duration
	Starts        []time.Time
}

// Service computes availability.
type Service struct {
	store       db.Querier
	granularity time.Duration
	now         func() time.Time
	recorder    Recorder
}

// Recorder receives the slots-returned metric (Section 9).
type Recorder interface {
	SlotsReturned(n int)
}

type noRecorder struct{}

func (noRecorder) SlotsReturned(int) {}

// New returns a Service. granularity is the start-time grid step (A7); now
// is the clock, so past starts can be hidden.
func New(store db.Querier, granularity time.Duration, now func() time.Time) *Service {
	return &Service{store: store, granularity: granularity, now: now, recorder: noRecorder{}}
}

// Instrument sends the slots-returned metric to recorder.
func (s *Service) Instrument(recorder Recorder) *Service {
	s.recorder = recorder
	return s
}

// Availability returns the free starts for q. A closed day, a past date or
// a service type nobody here is qualified for gives an empty list.
func (s *Service) Availability(ctx context.Context, q Query) (Result, error) {
	ctx, span := observability.Tracer().Start(ctx, "availability.Availability", trace.WithAttributes(
		attribute.String("dealership_id", q.DealershipID.String()),
		attribute.String("service_type_id", q.ServiceTypeID.String()),
	))
	defer span.End()

	res, err := s.availability(ctx, q)
	if err == nil {
		s.recorder.SlotsReturned(len(res.Starts))
		span.SetAttributes(attribute.Int("slots_returned", len(res.Starts)))
	}
	return res, err
}

func (s *Service) availability(ctx context.Context, q Query) (Result, error) {
	dealership, err := s.store.GetDealership(ctx, q.DealershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrDealershipNotFound
	}
	if err != nil {
		return Result{}, fmt.Errorf("load dealership: %w", err)
	}
	serviceType, err := s.store.GetServiceType(ctx, q.ServiceTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrServiceTypeNotFound
	}
	if err != nil {
		return Result{}, fmt.Errorf("load service type: %w", err)
	}
	loc, err := time.LoadLocation(dealership.Timezone)
	if err != nil {
		return Result{}, fmt.Errorf("dealership %s timezone: %w", dealership.ID, err)
	}

	res := Result{
		DealershipID:  dealership.ID,
		ServiceTypeID: serviceType.ID,
		Date:          q.Date,
		Timezone:      dealership.Timezone,
		Duration:      time.Duration(serviceType.DurationMinutes) * time.Minute,
		Starts:        []time.Time{},
	}

	hours, err := s.store.GetOpeningHours(ctx, db.GetOpeningHoursParams{
		DealershipID: dealership.ID,
		Weekday:      int16(q.Date.Weekday()), //nolint:gosec // time.Weekday is 0..6
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return res, nil // closed that day
	}
	if err != nil {
		return Result{}, fmt.Errorf("load opening hours: %w", err)
	}

	candidates := slots.Candidates(q.Date, loc,
		slots.Hours{Opens: clock(hours.OpensAt), Closes: clock(hours.ClosesAt)},
		res.Duration, s.granularity)
	candidates = slots.NotBefore(candidates, s.now())
	if len(candidates) == 0 {
		return res, nil
	}

	bays, err := s.store.ListActiveBayIDs(ctx, dealership.ID)
	if err != nil {
		return Result{}, fmt.Errorf("list bays: %w", err)
	}
	technicians, err := s.store.ListQualifiedTechnicianIDs(ctx, db.ListQualifiedTechnicianIDsParams{
		DealershipID: dealership.ID, ServiceTypeID: serviceType.ID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("list technicians: %w", err)
	}
	if len(bays) == 0 || len(technicians) == 0 {
		return res, nil
	}

	rows, err := s.store.ListConfirmedAppointmentsInRange(ctx, db.ListConfirmedAppointmentsInRangeParams{
		DealershipID: dealership.ID,
		WindowStart:  candidates[0],
		WindowEnd:    candidates[len(candidates)-1].Add(res.Duration),
	})
	if err != nil {
		return Result{}, fmt.Errorf("list appointments: %w", err)
	}
	busy := make([]slots.Busy, len(rows))
	for i, r := range rows {
		busy[i] = slots.Busy{
			BayID:        r.BayID,
			TechnicianID: r.TechnicianID,
			Interval:     slots.Interval{Start: r.StartsAt, End: r.EndsAt},
		}
	}

	res.Starts = slots.Free(candidates, res.Duration, bays, technicians, busy)
	return res, nil
}

// clock converts a PostgreSQL time of day to an offset from midnight.
func clock(t pgtype.Time) time.Duration {
	return time.Duration(t.Microseconds) * time.Microsecond
}
