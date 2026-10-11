package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pyaethu-aung/service-scheduler/internal/booking"
)

// maxIdempotencyKeyLength mirrors the contract's maxLength.
const maxIdempotencyKeyLength = 255

// BookAppointment books an appointment, or replays an idempotent retry.
func (s *Server) BookAppointment(ctx context.Context, req BookAppointmentRequestObject) (BookAppointmentResponseObject, error) {
	if len(req.Params.IdempotencyKey) > maxIdempotencyKeyLength {
		return BookAppointment422ApplicationProblemPlusJSONResponse{ValidationFailedApplicationProblemPlusJSONResponse(
			validationProblem(InvalidParam{
				Name:   "Idempotency-Key",
				Reason: fmt.Sprintf("must be at most %d characters", maxIdempotencyKeyLength),
			}))}, nil
	}
	if req.Body == nil {
		return BookAppointment422ApplicationProblemPlusJSONResponse{ValidationFailedApplicationProblemPlusJSONResponse(
			validationProblem(InvalidParam{Name: "body", Reason: "is required"}))}, nil
	}

	res, err := s.booking.Book(ctx, booking.Request{
		IdempotencyKey: req.Params.IdempotencyKey,
		DealershipID:   req.Body.DealershipId,
		CustomerID:     req.Body.CustomerId,
		VehicleID:      req.Body.VehicleId,
		ServiceTypeID:  req.Body.ServiceTypeId,
		StartsAt:       req.Body.StartsAt,
	})

	var (
		invalid     *booking.ValidationError
		notFound    *booking.NotFoundError
		unavailable *booking.UnavailableError
	)
	switch {
	case errors.As(err, &invalid):
		params := make([]InvalidParam, len(invalid.Params))
		for i, p := range invalid.Params {
			params[i] = InvalidParam{Name: p.Name, Reason: p.Reason}
		}
		return BookAppointment422ApplicationProblemPlusJSONResponse{ValidationFailedApplicationProblemPlusJSONResponse(
			validationProblem(params...))}, nil
	case errors.As(err, &notFound):
		return BookAppointment404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(
			newProblem(http.StatusNotFound, "not-found", capitalize(notFound.Resource)+" not found",
				fmt.Sprintf("no %s with id %s", notFound.Resource, notFound.ID)))}, nil
	case errors.As(err, &unavailable):
		return BookAppointment409ApplicationProblemPlusJSONResponse(slotUnavailable(unavailable)), nil
	case err != nil:
		return nil, fmt.Errorf("book: %w", err)
	}

	if res.Replayed {
		return BookAppointment200JSONResponse(appointmentResponse(res.Appointment)), nil
	}
	return BookAppointment201JSONResponse(appointmentResponse(res.Appointment)), nil
}

// GetAppointment returns an appointment in its current state.
func (s *Server) GetAppointment(ctx context.Context, req GetAppointmentRequestObject) (GetAppointmentResponseObject, error) {
	a, err := s.booking.Get(ctx, req.AppointmentId)
	if p, ok := appointmentNotFound(err); ok {
		return GetAppointment404ApplicationProblemPlusJSONResponse{p}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get appointment: %w", err)
	}
	return GetAppointment200JSONResponse(appointmentResponse(a)), nil
}

// CancelAppointment cancels an appointment; cancelling again is a no-op
// that returns the same state.
func (s *Server) CancelAppointment(ctx context.Context, req CancelAppointmentRequestObject) (CancelAppointmentResponseObject, error) {
	a, err := s.booking.Cancel(ctx, req.AppointmentId)
	if p, ok := appointmentNotFound(err); ok {
		return CancelAppointment404ApplicationProblemPlusJSONResponse{p}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cancel appointment: %w", err)
	}
	return CancelAppointment200JSONResponse(appointmentResponse(a)), nil
}

func appointmentNotFound(err error) (NotFoundApplicationProblemPlusJSONResponse, bool) {
	var nf *booking.NotFoundError
	if !errors.As(err, &nf) {
		return NotFoundApplicationProblemPlusJSONResponse{}, false
	}
	return NotFoundApplicationProblemPlusJSONResponse(newProblem(http.StatusNotFound, "not-found",
		"Appointment not found", fmt.Sprintf("no appointment with id %s", nf.ID))), true
}

func appointmentResponse(a booking.Appointment) Appointment {
	return Appointment{
		Id:          a.ID,
		Status:      AppointmentStatus(a.Status),
		StartsAt:    a.StartsAt.UTC(),
		EndsAt:      a.EndsAt.UTC(),
		Dealership:  DealershipRef{Id: a.Dealership.ID, Name: a.Dealership.Name},
		ServiceType: ServiceTypeRef{Id: a.ServiceType.ID, Code: a.ServiceType.Code, DurationMinutes: a.ServiceType.DurationMinutes},
		Bay:         ResourceRef{Id: a.Bay.ID, Name: a.Bay.Name},
		Technician:  ResourceRef{Id: a.Technician.ID, Name: a.Technician.Name},
		CustomerId:  a.CustomerID,
		VehicleId:   a.VehicleID,
	}
}

func slotUnavailable(e *booking.UnavailableError) SlotUnavailableProblem {
	reason := SlotUnavailableProblemReasonNoBayAvailable
	detail := "no service bay is free for the whole requested time"
	if e.Reason == booking.ReasonNoTechnician {
		reason = SlotUnavailableProblemReasonNoTechnicianAvailable
		detail = "no qualified technician is free for the whole requested time"
	}
	starts := make([]time.Time, len(e.Suggested))
	for i, t := range e.Suggested {
		starts[i] = t.UTC()
	}
	return SlotUnavailableProblem{
		Type:            problemBase + "slot-unavailable",
		Title:           "Requested time is not available",
		Status:          SlotUnavailableProblemStatusN409,
		Detail:          &detail,
		Reason:          reason,
		SuggestedStarts: starts,
	}
}

func validationProblem(params ...InvalidParam) ValidationProblem {
	return ValidationProblem{
		Type:          problemBase + "validation-failed",
		Title:         "The booking request is invalid",
		Status:        ValidationProblemStatusN422,
		InvalidParams: params,
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
