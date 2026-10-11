package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter returns the service's HTTP handler with middleware applied and
// every generated route mounted.
func NewRouter(srv *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	strict := NewStrictHandlerWithOptions(srv, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: internalError(srv.logger),
	})
	HandlerWithOptions(strict, ChiServerOptions{
		BaseRouter:       r,
		ErrorHandlerFunc: badRequest, // parameter binding errors
	})
	return r
}
