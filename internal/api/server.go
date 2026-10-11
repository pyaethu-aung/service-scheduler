// Package api is the HTTP layer: the router, middleware and the handlers
// that implement the interfaces generated from docs/openapi/service-scheduler.yaml.
// It calls the booking and availability services and never the store directly.
package api

import "context"

// Server implements the generated StrictServerInterface.
type Server struct{}

var _ StrictServerInterface = (*Server)(nil)

// GetHealthz is the liveness probe. It never touches the database.
func (s *Server) GetHealthz(_ context.Context, _ GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return GetHealthz200JSONResponse{Status: HealthStatusOk}, nil
}
