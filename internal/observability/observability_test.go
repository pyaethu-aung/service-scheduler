package observability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestNewLogger_AddsTraceAndRequestIDs(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	logger := NewLogger(&buf).With("component", "test")

	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("t").Start(context.WithValue(t.Context(), middleware.RequestIDKey, "req-1"), "op")
	defer span.End()
	logger.InfoContext(ctx, "hello")
	logger.WithGroup("g").InfoContext(t.Context(), "plain")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["trace_id"] != span.SpanContext().TraceID().String() || first["request_id"] != "req-1" || first["component"] != "test" {
		t.Errorf("record = %v", first)
	}
	if strings.Contains(lines[1], "trace_id") || strings.Contains(lines[1], "request_id") {
		t.Errorf("a record without a span or request carried IDs: %s", lines[1])
	}
}

func TestSetupTracing(t *testing.T) {
	// Not parallel: it replaces the global propagator and provider.
	shutdown, err := SetupTracing(t.Context(), "")
	if err != nil || shutdown(t.Context()) != nil {
		t.Fatalf("no endpoint: %v", err)
	}
	// The exporter connects lazily, so an unreachable endpoint still sets up.
	shutdown, err = SetupTracing(t.Context(), "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestMetrics(t *testing.T) {
	t.Parallel()
	m := NewMetrics()
	m.ObserveHTTP("/v1/appointments", http.MethodPost, http.StatusCreated, 20*time.Millisecond)
	m.Booking(OutcomeCreated)
	m.Booking(OutcomeNoCapacity)
	m.ConflictRetry()
	m.SlotsReturned(37)
	m.Register(NewPoolCollector(func() fakeStats { return fakeStats{acquired: 3, max: 10} }))

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`http_server_request_duration_seconds_count{method="POST",route="/v1/appointments",status="201"} 1`,
		`service_scheduler_bookings_total{outcome="created"} 1`,
		`service_scheduler_bookings_total{outcome="conflict_exhausted"} 0`,
		`service_scheduler_booking_conflict_retries_total 1`,
		`service_scheduler_availability_slots_returned_count 1`,
		`db_pool_acquired_conns 3`,
		`db_pool_max_conns 10`,
		`db_pool_acquire_duration_seconds_total 0.5`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}

	var none *Metrics // nil records nothing and does not panic
	none.ObserveHTTP("/", http.MethodGet, 200, time.Second)
	none.Booking(OutcomeInvalid)
	none.ConflictRetry()
	none.SlotsReturned(1)
}

type fakeStats struct{ acquired, max int32 }

func (f fakeStats) AcquiredConns() int32           { return f.acquired }
func (f fakeStats) IdleConns() int32               { return 1 }
func (f fakeStats) TotalConns() int32              { return f.acquired + 1 }
func (f fakeStats) MaxConns() int32                { return f.max }
func (f fakeStats) AcquireCount() int64            { return 42 }
func (f fakeStats) EmptyAcquireCount() int64       { return 2 }
func (f fakeStats) AcquireDuration() time.Duration { return 500 * time.Millisecond }

func TestQueryName(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"-- name: BookAppointment :one\nWITH free_bay AS (...)": "BookAppointment",
		"  -- name: GetVehicle :one\nSELECT id":                 "GetVehicle",
		"begin":                                                 "BEGIN",
		"select pg_advisory_xact_lock($1) -- name":              "SELECT",
		"": "query",
	}
	for sql, want := range tests {
		if got := QueryName(sql); got != want {
			t.Errorf("QueryName(%q) = %q, want %q", sql, got, want)
		}
	}
}

func TestQueryTracer_SpansNamedByQuery(t *testing.T) {
	t.Parallel()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	qt := QueryTracer{Tracer: tp.Tracer("t")}

	ctx := qt.TraceQueryStart(t.Context(), nil, pgx.TraceQueryStartData{SQL: "-- name: GetCustomer :one\nSELECT id FROM customers WHERE id = $1"})
	qt.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("boom")})
	ctx = qt.TraceQueryStart(t.Context(), nil, pgx.TraceQueryStartData{SQL: "commit"})
	qt.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("ended spans = %d, want 2", len(spans))
	}
	if spans[0].Name() != "GetCustomer" || spans[0].Status().Code != codes.Error || spans[0].SpanKind() != trace.SpanKindClient {
		t.Errorf("first span = %q %v %v", spans[0].Name(), spans[0].Status(), spans[0].SpanKind())
	}
	if spans[1].Name() != "COMMIT" || spans[1].Status().Code == codes.Error {
		t.Errorf("second span = %q %v", spans[1].Name(), spans[1].Status())
	}
	for _, s := range spans {
		for _, a := range s.Attributes() {
			if strings.Contains(a.Value.AsString(), "customers") {
				t.Errorf("span %s carries SQL text: %v", s.Name(), a)
			}
		}
	}
}

func TestQueryTracer_ZeroValueUsesGlobalProvider(t *testing.T) {
	t.Parallel()
	ctx := QueryTracer{}.TraceQueryStart(t.Context(), nil, pgx.TraceQueryStartData{SQL: "select 1"})
	QueryTracer{}.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
}
