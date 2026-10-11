// Package observability sets up the service's logs, traces and metrics
// (docs/system-design.md Section 9): slog JSON logs carrying trace and
// request IDs, OpenTelemetry spans exported over OTLP, and the Prometheus
// metrics served on /metrics.
package observability

import (
	"context"
	"io"
	"log/slog"

	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"
)

// NewLogger returns a JSON logger that adds trace_id and request_id from
// the context to every record logged with a *Context method.
func NewLogger(w io.Writer) *slog.Logger {
	return slog.New(contextHandler{slog.NewJSONHandler(w, nil)})
}

type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
	}
	if id := middleware.GetReqID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
