// Package booking books appointments (FR1 to FR3, docs/system-design.md
// Section 5.2): it validates the request, replays idempotent retries, claims
// a bay and a technician atomically with bounded retries on lost races, and
// explains a refusal with nearby free start times.
//
// The exclusion constraints in PostgreSQL carry the no-double-booking
// guarantee; this package only picks likely-free resources and handles the
// one error that says a concurrent request got there first.
package booking

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/slots"
)

const (
	// MaxAttempts bounds the claim-and-insert attempts per request (Section 5.2).
	MaxAttempts = 3
	// KeyTTL is how long an Idempotency-Key replays its booking (Section 10).
	KeyTTL = 24 * time.Hour
	// maxSuggestions caps suggestedStarts on a 409.
	maxSuggestions = 3
)

// Request is a booking request. StartsAt may carry any offset.
type Request struct {
	IdempotencyKey string
	DealershipID   uuid.UUID
	CustomerID     uuid.UUID
	VehicleID      uuid.UUID
	ServiceTypeID  uuid.UUID
	StartsAt       time.Time
}

// Ref names a related resource.
type Ref struct {
	ID   uuid.UUID
	Name string
}

// ServiceTypeRef summarises the booked service type.
type ServiceTypeRef struct {
	ID              uuid.UUID
	Code            string
	DurationMinutes int
}

// Appointment is a booked appointment as the API returns it. Times are UTC.
type Appointment struct {
	ID          uuid.UUID
	Status      string
	StartsAt    time.Time
	EndsAt      time.Time
	Dealership  Ref
	ServiceType ServiceTypeRef
	Bay         Ref
	Technician  Ref
	CustomerID  uuid.UUID
	VehicleID   uuid.UUID
}

// Result is the outcome of Book. Replayed is true when the Idempotency-Key
// had already booked this request; Appointment is then its current state.
type Result struct {
	Appointment Appointment
	Replayed    bool
}

// InvalidParam names one invalid request parameter.
type InvalidParam struct {
	Name   string
	Reason string
}

// ValidationError is a request that breaks a booking rule (422).
type ValidationError struct {
	Params []InvalidParam
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Params))
	for i, p := range e.Params {
		parts[i] = p.Name + ": " + p.Reason
	}
	return "invalid booking request: " + strings.Join(parts, "; ")
}

// NotFoundError is a request that references a missing resource (404).
type NotFoundError struct {
	Resource string // "dealership", "customer", "vehicle" or "service type"
	ID       uuid.UUID
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %s not found", e.Resource, e.ID)
}

// Reasons a slot is unavailable.
const (
	ReasonNoBay        = "no_bay_available"
	ReasonNoTechnician = "no_technician_available"
)

// UnavailableError is a request for a time with no free bay or no free
// qualified technician (409). Suggested holds up to three free starts on the
// same local day nearest the requested one, ascending.
type UnavailableError struct {
	Reason    string
	Suggested []time.Time
}

func (e *UnavailableError) Error() string {
	return "requested time is not available: " + e.Reason
}

// ErrKeyClaimed is returned by Store.Book when a concurrent request claimed
// the same Idempotency-Key first; the store has rolled back.
var ErrKeyClaimed = errors.New("idempotency key claimed by a concurrent request")

// Store is the data access booking needs. Book runs BookAppointment and
// ClaimIdempotencyKey in one transaction, returning pgx.ErrNoRows when
// nothing is free, db.ErrSlotTaken when a concurrent insert won, and
// ErrKeyClaimed when a concurrent request owns the key.
type Store interface {
	GetDealership(ctx context.Context, id uuid.UUID) (db.GetDealershipRow, error)
	GetCustomer(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	GetVehicle(ctx context.Context, id uuid.UUID) (db.GetVehicleRow, error)
	GetServiceType(ctx context.Context, id uuid.UUID) (db.GetServiceTypeRow, error)
	GetOpeningHours(ctx context.Context, arg db.GetOpeningHoursParams) (db.GetOpeningHoursRow, error)
	GetIdempotencyKey(ctx context.Context, arg db.GetIdempotencyKeyParams) (db.IdempotencyKey, error)
	GetAppointmentDetails(ctx context.Context, id uuid.UUID) (db.GetAppointmentDetailsRow, error)
	HasFreeBay(ctx context.Context, arg db.HasFreeBayParams) (bool, error)
	CancelAppointment(ctx context.Context, id uuid.UUID) (db.Appointment, error)
	DeleteExpiredIdempotencyKeys(ctx context.Context, liveSince time.Time) (int64, error)
	Book(ctx context.Context, arg db.BookAppointmentParams, key, requestHash string, liveSince time.Time) (uuid.UUID, error)
}

// Availability computes free starts; it supplies 409 suggestions.
type Availability interface {
	Availability(ctx context.Context, q availability.Query) (availability.Result, error)
}

// Service books appointments.
type Service struct {
	store        Store
	availability Availability
	granularity  time.Duration
	now          func() time.Time
	// pause waits between attempts; tests replace it.
	pause func(ctx context.Context, attempt int) error
}

// New returns a Service. granularity is the start-time grid step (A7) and
// now is the clock.
func New(store Store, avail Availability, granularity time.Duration, now func() time.Time) *Service {
	return &Service{store: store, availability: avail, granularity: granularity, now: now, pause: jitterPause}
}

// Book validates req and books it, or replays it when its Idempotency-Key
// already booked the same request.
func (s *Service) Book(ctx context.Context, req Request) (Result, error) {
	if err := checkRequired(req); err != nil {
		return Result{}, err
	}
	v, err := s.validate(ctx, req)
	if err != nil {
		return Result{}, err
	}

	hash := requestHash(req)
	liveSince := s.now().Add(-KeyTTL)
	if res, ok, err := s.replay(ctx, req.IdempotencyKey, hash, liveSince); ok || err != nil {
		return res, err
	}

	params := db.BookAppointmentParams{
		DealershipID:  req.DealershipID,
		CustomerID:    req.CustomerID,
		VehicleID:     req.VehicleID,
		ServiceTypeID: req.ServiceTypeID,
		StartsAt:      v.start,
		EndsAt:        v.start.Add(v.duration),
		DayStart:      v.day.Start,
		DayEnd:        v.day.End,
	}
	for attempt := 1; ; attempt++ {
		id, err := s.store.Book(ctx, params, req.IdempotencyKey, hash, liveSince)
		switch {
		case err == nil:
			a, err := s.details(ctx, id)
			return Result{Appointment: a}, err
		case errors.Is(err, ErrKeyClaimed):
			res, ok, err := s.replay(ctx, req.IdempotencyKey, hash, liveSince)
			if !ok && err == nil {
				err = fmt.Errorf("idempotency key %q claimed but not found", req.IdempotencyKey)
			}
			return res, err
		case errors.Is(err, pgx.ErrNoRows):
			return Result{}, s.unavailable(ctx, req, v)
		case errors.Is(err, db.ErrSlotTaken):
			// A concurrent booking won between our read and our insert. The
			// next attempt, in a new transaction, sees it and picks again.
			if attempt == MaxAttempts {
				return Result{}, s.unavailable(ctx, req, v)
			}
			if err := s.pause(ctx, attempt); err != nil {
				return Result{}, err
			}
		default:
			return Result{}, fmt.Errorf("book: %w", err)
		}
	}
}

// Get returns the appointment in its current state.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Appointment, error) {
	return s.details(ctx, id)
}

// Cancel cancels the appointment, releasing its bay and technician at once
// (FR5, Section 5.4): the row leaves the partial exclusion constraints and is
// kept for history. Cancelling again changes nothing and returns the same
// state, including the first cancellation time.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (Appointment, error) {
	_, err := s.store.CancelAppointment(ctx, id)
	if err := found(err, "appointment", id); err != nil {
		return Appointment{}, err
	}
	return s.details(ctx, id)
}

// validated holds what validation derived from the request.
type validated struct {
	loc      *time.Location
	start    time.Time
	duration time.Duration
	date     slots.Date
	day      slots.Interval
}

func checkRequired(req Request) error {
	var invalid []InvalidParam
	required := func(name string, missing bool) {
		if missing {
			invalid = append(invalid, InvalidParam{Name: name, Reason: "is required"})
		}
	}
	required("Idempotency-Key", strings.TrimSpace(req.IdempotencyKey) == "")
	required("dealershipId", req.DealershipID == uuid.Nil)
	required("customerId", req.CustomerID == uuid.Nil)
	required("vehicleId", req.VehicleID == uuid.Nil)
	required("serviceTypeId", req.ServiceTypeID == uuid.Nil)
	required("startsAt", req.StartsAt.IsZero())
	if len(invalid) > 0 {
		return &ValidationError{Params: invalid}
	}
	return nil
}

// validate checks the referenced rows exist (404) and the booking rules
// hold (422): the vehicle is the customer's, and the start is in the future,
// on the grid, with the whole service inside one local day's opening hours.
func (s *Service) validate(ctx context.Context, req Request) (validated, error) {
	dealership, err := s.store.GetDealership(ctx, req.DealershipID)
	if err := found(err, "dealership", req.DealershipID); err != nil {
		return validated{}, err
	}
	_, err = s.store.GetCustomer(ctx, req.CustomerID)
	if err := found(err, "customer", req.CustomerID); err != nil {
		return validated{}, err
	}
	vehicle, err := s.store.GetVehicle(ctx, req.VehicleID)
	if err := found(err, "vehicle", req.VehicleID); err != nil {
		return validated{}, err
	}
	serviceType, err := s.store.GetServiceType(ctx, req.ServiceTypeID)
	if err := found(err, "service type", req.ServiceTypeID); err != nil {
		return validated{}, err
	}
	loc, err := time.LoadLocation(dealership.Timezone)
	if err != nil {
		return validated{}, fmt.Errorf("dealership %s timezone: %w", dealership.ID, err)
	}

	v := validated{
		loc:      loc,
		start:    req.StartsAt.UTC(),
		duration: time.Duration(serviceType.DurationMinutes) * time.Minute,
	}
	v.date = slots.DateOf(v.start, loc)
	v.day = slots.DayBounds(v.date, loc)

	var invalid []InvalidParam
	if vehicle.CustomerID != req.CustomerID {
		invalid = append(invalid, InvalidParam{Name: "vehicleId", Reason: "vehicle does not belong to the customer"})
	}
	if reason, err := s.startProblem(ctx, req.DealershipID, v); err != nil {
		return validated{}, err
	} else if reason != "" {
		invalid = append(invalid, InvalidParam{Name: "startsAt", Reason: reason})
	}
	if len(invalid) > 0 {
		return validated{}, &ValidationError{Params: invalid}
	}
	return v, nil
}

// startProblem returns why v.start is not bookable, or "" when it is.
func (s *Service) startProblem(ctx context.Context, dealershipID uuid.UUID, v validated) (string, error) {
	if v.start.Before(s.now()) {
		return "is in the past", nil
	}
	hours, err := s.store.GetOpeningHours(ctx, db.GetOpeningHoursParams{
		DealershipID: dealershipID,
		Weekday:      int16(v.date.Weekday()), //nolint:gosec // time.Weekday is 0..6
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Sprintf("the dealership is closed on %s", dateString(v.date)), nil
	}
	if err != nil {
		return "", fmt.Errorf("load opening hours: %w", err)
	}
	opens, closes := clock(hours.OpensAt), clock(hours.ClosesAt)
	if !slots.OnGrid(v.start, v.date, v.loc, slots.Hours{Opens: opens, Closes: closes}, v.duration, s.granularity) {
		return fmt.Sprintf("must be on the %d-minute grid, with the whole %d-minute service between %s and %s local time on %s",
			int(s.granularity/time.Minute), int(v.duration/time.Minute), hhmm(opens), hhmm(closes), dateString(v.date)), nil
	}
	return "", nil
}

// replay returns the booking an Idempotency-Key already made, ok=false when
// the key is unknown or expired, and a ValidationError when the key was used
// for a different request.
func (s *Service) replay(ctx context.Context, key, hash string, liveSince time.Time) (Result, bool, error) {
	k, err := s.store.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{Key: key, LiveSince: liveSince})
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("load idempotency key: %w", err)
	}
	if k.RequestHash != hash {
		return Result{}, false, &ValidationError{Params: []InvalidParam{{
			Name: "Idempotency-Key", Reason: "already used for a different booking request",
		}}}
	}
	a, err := s.details(ctx, k.AppointmentID)
	if err != nil {
		return Result{}, false, err
	}
	return Result{Appointment: a, Replayed: true}, true, nil
}

// unavailable explains a refusal: which resource ran out, and the free
// starts nearest the requested one on the same local day.
func (s *Service) unavailable(ctx context.Context, req Request, v validated) error {
	free, err := s.store.HasFreeBay(ctx, db.HasFreeBayParams{
		DealershipID: req.DealershipID, StartsAt: v.start, EndsAt: v.start.Add(v.duration),
	})
	if err != nil {
		return fmt.Errorf("check free bays: %w", err)
	}
	reason := ReasonNoBay
	if free {
		reason = ReasonNoTechnician
	}

	res, err := s.availability.Availability(ctx, availability.Query{
		DealershipID: req.DealershipID, ServiceTypeID: req.ServiceTypeID, Date: v.date,
	})
	if err != nil {
		return fmt.Errorf("suggest starts: %w", err)
	}
	return &UnavailableError{Reason: reason, Suggested: nearest(res.Starts, v.start, maxSuggestions)}
}

// nearest returns up to n of starts closest to target, excluding target,
// ties going to the earlier start, in ascending order.
func nearest(starts []time.Time, target time.Time, n int) []time.Time {
	candidates := make([]time.Time, 0, len(starts))
	for _, s := range starts {
		if !s.Equal(target) {
			candidates = append(candidates, s)
		}
	}
	distance := func(t time.Time) time.Duration { return t.Sub(target).Abs() }
	slices.SortFunc(candidates, func(a, b time.Time) int {
		if c := cmp.Compare(distance(a), distance(b)); c != 0 {
			return c
		}
		return a.Compare(b)
	})
	candidates = candidates[:min(n, len(candidates))]
	slices.SortFunc(candidates, time.Time.Compare)
	return candidates
}

func (s *Service) details(ctx context.Context, id uuid.UUID) (Appointment, error) {
	d, err := s.store.GetAppointmentDetails(ctx, id)
	if err := found(err, "appointment", id); err != nil {
		return Appointment{}, err
	}
	return Appointment{
		ID:         d.ID,
		Status:     d.Status,
		StartsAt:   d.StartsAt.UTC(),
		EndsAt:     d.EndsAt.UTC(),
		Dealership: Ref{ID: d.DealershipID, Name: d.DealershipName},
		ServiceType: ServiceTypeRef{
			ID: d.ServiceTypeID, Code: d.ServiceTypeCode, DurationMinutes: int(d.ServiceTypeDurationMinutes),
		},
		Bay:        Ref{ID: d.BayID, Name: d.BayName},
		Technician: Ref{ID: d.TechnicianID, Name: d.TechnicianName},
		CustomerID: d.CustomerID,
		VehicleID:  d.VehicleID,
	}, nil
}

// requestHash fingerprints the booking a key stands for, so a key reused
// for a different request is detected. Equivalent start times in different
// offsets hash the same.
func requestHash(req Request) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		req.DealershipID.String(),
		req.CustomerID.String(),
		req.VehicleID.String(),
		req.ServiceTypeID.String(),
		req.StartsAt.UTC().Format(time.RFC3339Nano),
	}, "|")))
	return hex.EncodeToString(sum[:])
}

// jitterPause sleeps a small random time that grows with the attempt, so
// racing requests spread out instead of colliding again.
func jitterPause(ctx context.Context, attempt int) error {
	d := time.Duration(attempt)*5*time.Millisecond + rand.N(10*time.Millisecond) //nolint:gosec // jitter, not security
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func found(err error, resource string, id uuid.UUID) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return &NotFoundError{Resource: resource, ID: id}
	}
	if err != nil {
		return fmt.Errorf("load %s: %w", resource, err)
	}
	return nil
}

func clock(t pgtype.Time) time.Duration {
	return time.Duration(t.Microseconds) * time.Microsecond
}

func hhmm(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

func dateString(d slots.Date) string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}
