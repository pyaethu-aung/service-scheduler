package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
)

// GetAvailability lists free start times for a service type on a local date.
func (s *Server) GetAvailability(ctx context.Context, req GetAvailabilityRequestObject) (GetAvailabilityResponseObject, error) {
	day := req.Params.Date.Time
	res, err := s.availability.Availability(ctx, availability.Query{
		DealershipID:  req.DealershipId,
		ServiceTypeID: req.Params.ServiceTypeId,
		Date:          availability.Date{Year: day.Year(), Month: day.Month(), Day: day.Day()},
	})
	switch {
	case errors.Is(err, availability.ErrDealershipNotFound):
		return GetAvailability404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(
			newProblem(http.StatusNotFound, "not-found", "Dealership not found",
				fmt.Sprintf("no dealership with id %s", req.DealershipId)))}, nil
	case errors.Is(err, availability.ErrServiceTypeNotFound):
		return GetAvailability404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(
			newProblem(http.StatusNotFound, "not-found", "Service type not found",
				fmt.Sprintf("no service type with id %s", req.Params.ServiceTypeId)))}, nil
	case err != nil:
		return nil, fmt.Errorf("availability: %w", err)
	}

	starts := make([]time.Time, len(res.Starts))
	for i, t := range res.Starts {
		starts[i] = t.UTC()
	}
	return GetAvailability200JSONResponse{
		DealershipId:    res.DealershipID,
		ServiceTypeId:   res.ServiceTypeID,
		Date:            openapi_types.Date{Time: time.Date(res.Date.Year, res.Date.Month, res.Date.Day, 0, 0, 0, 0, time.UTC)},
		Timezone:        res.Timezone,
		DurationMinutes: int(res.Duration / time.Minute),
		AvailableStarts: starts,
	}, nil
}
