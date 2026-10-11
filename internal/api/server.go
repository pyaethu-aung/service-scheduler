// Package api is the HTTP layer: the router, middleware and the handlers
// that implement the interfaces generated from docs/openapi/service-scheduler.yaml.
// It calls the booking and availability services and never the store directly.
package api

import (
	"context"
	"log/slog"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/booking"
)

// AvailabilityService answers availability queries (internal/availability).
type AvailabilityService interface {
	Availability(ctx context.Context, q availability.Query) (availability.Result, error)
}

// BookingService books appointments (internal/booking).
type BookingService interface {
	Book(ctx context.Context, req booking.Request) (booking.Result, error)
}

// Server implements the generated StrictServerInterface.
type Server struct {
	availability AvailabilityService
	booking      BookingService
	logger       *slog.Logger
}

var _ StrictServerInterface = (*Server)(nil)

// NewServer returns a Server backed by the given services.
func NewServer(avail AvailabilityService, book BookingService, logger *slog.Logger) *Server {
	return &Server{availability: avail, booking: book, logger: logger}
}

// GetHealthz is the liveness probe. It never touches the database.
func (s *Server) GetHealthz(_ context.Context, _ GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return GetHealthz200JSONResponse{Status: HealthStatusOk}, nil
}
