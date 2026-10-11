package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/pyaethu-aung/service-scheduler/internal/observability"
)

// Defaults for Options (Section 11).
const (
	// DefaultMaxBodyBytes caps request bodies; a booking is under 300 bytes.
	DefaultMaxBodyBytes = 64 << 10
	// DefaultRequestTimeout bounds each request; its context reaches every
	// query, so a stuck database call cannot hold a request open.
	DefaultRequestTimeout = 10 * time.Second
)

// apiKeyHeader carries the shared API key on /v1 routes.
const apiKeyHeader = "X-API-Key" //nolint:gosec // a header name, not a credential

// requireAPIKey rejects /v1 requests without the right X-API-Key with 403.
// Keys are compared as SHA-256 digests in constant time, so neither the
// contents nor the length leak through timing. Health and metrics stay open.
func requireAPIKey(key string) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(key))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v1/") {
				got := sha256.Sum256([]byte(r.Header.Get(apiKeyHeader)))
				if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
					writeProblem(w, http.StatusForbidden, newProblem(http.StatusForbidden, "forbidden",
						"Forbidden", "the "+apiKeyHeader+" header is missing or wrong"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// limitBody caps the request body; reading past the limit fails, which the
// handlers report like any malformed body.
func limitBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// deadline gives every request a context deadline.
func deadline(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// observe wraps each request in a server span (continuing an incoming W3C
// trace), then writes one access log line and records the request duration
// by route template once the router has matched the request.
func observe(logger *slog.Logger, metrics *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := observability.Tracer().Start(ctx, r.Method, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(ctx))

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			route := routeOf(r)
			elapsed := time.Since(start)

			span.SetName(r.Method + " " + route)
			span.SetAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", status),
			)
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
			metrics.ObserveHTTP(route, r.Method, status, elapsed)
			logger.InfoContext(ctx, "request",
				"method", r.Method, "route", route, "status", status,
				"duration_ms", float64(elapsed.Microseconds())/1000)
		})
	}
}

// routeOf returns the matched route template, never the raw path, so IDs
// stay out of metric labels and logs.
func routeOf(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
}

// recoverer turns a panic into a 500 problem and logs it.
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // sentinel value from net/http, re-panicked as is
						panic(v)
					}
					internalError(logger)(w, r, fmt.Errorf("panic: %v", v))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
