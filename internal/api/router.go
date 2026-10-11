package api

import (
	"cmp"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/pyaethu-aung/service-scheduler/internal/observability"
)

// Options configures the router's hardening and observability.
type Options struct {
	// APIKey is required on every /v1 route (Section 11). It must be set.
	APIKey string
	// MaxBodyBytes caps request bodies; zero means DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// RequestTimeout bounds each request; zero means DefaultRequestTimeout.
	RequestTimeout time.Duration
	// Metrics, when set, records request metrics and serves /metrics.
	Metrics *observability.Metrics
}

// NewRouter returns the service's HTTP handler with middleware applied and
// every generated route mounted.
func NewRouter(srv *Server, opts Options) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(observe(srv.logger, opts.Metrics))
	r.Use(recoverer(srv.logger))
	r.Use(deadline(cmp.Or(opts.RequestTimeout, DefaultRequestTimeout)))
	r.Use(limitBody(cmp.Or(opts.MaxBodyBytes, DefaultMaxBodyBytes)))
	r.Use(requireAPIKey(opts.APIKey))

	if opts.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", opts.Metrics.Handler())
	}

	strict := NewStrictHandlerWithOptions(srv, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestError,
		ResponseErrorHandlerFunc: internalError(srv.logger),
	})
	HandlerWithOptions(strict, ChiServerOptions{
		BaseRouter:       r,
		ErrorHandlerFunc: requestError, // parameter binding errors
	})
	return r
}

// discardLogger is the logger for a Server built without one.
var discardLogger = slog.New(slog.DiscardHandler)
