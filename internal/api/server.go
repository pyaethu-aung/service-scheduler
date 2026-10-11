// Package api is the HTTP layer: the router, middleware and the handlers
// that implement the interfaces generated from docs/openapi/service-scheduler.yaml.
// It calls the booking and availability services and never the store directly.
package api

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/booking"
)

// AvailabilityService answers availability queries (internal/availability).
type AvailabilityService interface {
	Availability(ctx context.Context, q availability.Query) (availability.Result, error)
}

// BookingService books, fetches and cancels appointments (internal/booking).
type BookingService interface {
	Book(ctx context.Context, req booking.Request) (booking.Result, error)
	Get(ctx context.Context, id uuid.UUID) (booking.Appointment, error)
	Cancel(ctx context.Context, id uuid.UUID) (booking.Appointment, error)
}

// ReadinessChecker reports whether the database answers and is fully migrated
// (internal/db). Both errors nil means ready.
type ReadinessChecker interface {
	Ready(ctx context.Context) (database, migration error)
}

// Server implements the generated StrictServerInterface.
type Server struct {
	availability AvailabilityService
	booking      BookingService
	readiness    ReadinessChecker
	logger       *slog.Logger
}

var _ StrictServerInterface = (*Server)(nil)

// NewServer returns a Server backed by the given services.
func NewServer(avail AvailabilityService, book BookingService, logger *slog.Logger) *Server {
	if logger == nil {
		logger = discardLogger
	}
	return &Server{availability: avail, booking: book, logger: logger}
}

// WithReadiness sets the checks behind /readyz.
func (s *Server) WithReadiness(r ReadinessChecker) *Server {
	s.readiness = r
	return s
}

// GetReadyz is the readiness probe: 200 when the database answers and is
// migrated to this build's latest migration, 503 otherwise.
func (s *Server) GetReadyz(ctx context.Context, _ GetReadyzRequestObject) (GetReadyzResponseObject, error) {
	if s.readiness == nil {
		return GetReadyz503JSONResponse{Status: ReadinessStatusNotReady, Checks: ReadinessChecks{
			Database: "not configured", Migrations: "not configured",
		}}, nil
	}
	dbErr, migErr := s.readiness.Ready(ctx)
	checks := ReadinessChecks{Database: checkResult(dbErr), Migrations: checkResult(migErr)}
	if dbErr != nil || migErr != nil {
		s.logger.WarnContext(ctx, "not ready", "database", checks.Database, "migrations", checks.Migrations)
		return GetReadyz503JSONResponse{Status: ReadinessStatusNotReady, Checks: checks}, nil //nolint:nilerr // a failed check is a 503 body, not an error
	}
	return GetReadyz200JSONResponse{Status: ReadinessStatusReady, Checks: checks}, nil
}

func checkResult(err error) string {
	if err != nil {
		return err.Error()
	}
	return "ok"
}

// GetHealthz is the liveness probe. It never touches the database.
func (s *Server) GetHealthz(_ context.Context, _ GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return GetHealthz200JSONResponse{Status: HealthStatusOk}, nil
}
