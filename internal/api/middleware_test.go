package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/pyaethu-aung/service-scheduler/internal/booking"
	"github.com/pyaethu-aung/service-scheduler/internal/observability"
)

func TestContract_APIKey(t *testing.T) {
	t.Parallel()
	c := newContract(t)
	fake := &fakeBooking{res: booking.Result{Appointment: bookedAppointment("confirmed")}}
	h := NewRouter(NewServer(nil, fake, nil), Options{APIKey: "s3cret"})
	path := "/v1/appointments/" + uuid.NewString()

	tests := []struct {
		name   string
		path   string
		key    string
		status int
	}{
		{"missing key", path, "", http.StatusForbidden},
		{"wrong key", path, "s3cret-not", http.StatusForbidden},
		{"prefix of the key", path, "s3cre", http.StatusForbidden},
		{"right key", path, "s3cret", http.StatusOK},
		{"health needs no key", "/healthz", "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil)
			if tt.key != "" {
				req.Header.Set("X-API-Key", tt.key)
			}
			rec := c.do(t, h, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			if tt.status == http.StatusForbidden && !strings.Contains(rec.Body.String(), `/problems/forbidden"`) {
				t.Errorf("403 body = %s", rec.Body)
			}
		})
	}
}

func TestContract_OversizedBookingBodyIs422(t *testing.T) {
	t.Parallel()
	c := newContract(t)
	h := newTestRouter(NewServer(nil, &fakeBooking{}, nil))

	big := `{"dealershipId":"` + strings.Repeat("x", DefaultMaxBodyBytes) + `"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/appointments", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-1")
	rec := c.doInvalid(t, h, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"name":"body"`) ||
		!strings.Contains(rec.Body.String(), "too large") {
		t.Fatalf("got %d %s, want 422 naming the body", rec.Code, rec.Body)
	}
}

type deadlineBooking struct {
	fakeBooking
	deadline time.Duration
}

func (d *deadlineBooking) Get(ctx context.Context, id uuid.UUID) (booking.Appointment, error) {
	if dl, ok := ctx.Deadline(); ok {
		d.deadline = time.Until(dl)
	}
	return d.fakeBooking.Get(ctx, id)
}

func TestRequestDeadline(t *testing.T) {
	t.Parallel()
	fake := &deadlineBooking{fakeBooking: fakeBooking{res: booking.Result{Appointment: bookedAppointment("confirmed")}}}
	h := NewRouter(NewServer(nil, fake, nil), Options{APIKey: "k", RequestTimeout: 3 * time.Second})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/appointments/"+uuid.NewString(), nil)
	req.Header.Set("X-API-Key", "k")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if fake.deadline <= 0 || fake.deadline > 3*time.Second {
		t.Errorf("handler saw a deadline %s away, want within 3s", fake.deadline)
	}
}

func TestObserve_LogsAndMetricsUseRouteTemplates(t *testing.T) {
	t.Parallel()
	var logs strings.Builder
	metrics := observability.NewMetrics()
	fake := &fakeBooking{res: booking.Result{Appointment: bookedAppointment("confirmed")}}
	h := NewRouter(NewServer(nil, fake, observability.NewLogger(&logs)), Options{APIKey: "k", Metrics: metrics})

	id := uuid.NewString()
	for _, path := range []string{"/v1/appointments/" + id, "/no/such/route"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.Header.Set("X-API-Key", "k")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	// Rejected by the API-key check before routing: still labelled by route.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/appointments/"+id+"/cancel", nil))

	var first map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(logs.String(), "\n", 2)[0]), &first); err != nil {
		t.Fatalf("access log %q: %v", logs.String(), err)
	}
	if first["msg"] != "request" || first["route"] != "/v1/appointments/{appointmentId}" ||
		first["status"] != float64(200) || first["request_id"] == nil || first["trace_id"] != nil && first["trace_id"] == "" {
		t.Errorf("access log = %v", first)
	}
	if strings.Contains(logs.String(), id) {
		t.Error("the raw appointment id leaked into the access log")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)) // no key needed
	body := rec.Body.String()
	for _, want := range []string{
		`http_server_request_duration_seconds_count{method="GET",route="/v1/appointments/{appointmentId}",status="200"} 1`,
		`route="unmatched",status="404"`,
		`http_server_request_duration_seconds_count{method="POST",route="/v1/appointments/{appointmentId}/cancel",status="403"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
	if strings.Contains(body, id) {
		t.Error("the raw appointment id leaked into a metric label")
	}
}

type panickingBooking struct{ fakeBooking }

func (*panickingBooking) Get(context.Context, uuid.UUID) (booking.Appointment, error) {
	panic("boom")
}

func TestRecoverer(t *testing.T) {
	t.Parallel()
	var logs strings.Builder
	h := NewRouter(NewServer(nil, &panickingBooking{}, slog.New(slog.NewJSONHandler(&logs, nil))), Options{APIKey: "k"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/appointments/"+uuid.NewString(), nil)
	req.Header.Set("X-API-Key", "k")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "panic: boom") {
		t.Errorf("got %d, logs %s; want a logged 500", rec.Code, logs.String())
	}
}

type fakeReadiness struct{ database, migration error }

func (f fakeReadiness) Ready(context.Context) (database, migration error) {
	return f.database, f.migration
}

func TestContract_Readyz(t *testing.T) {
	t.Parallel()
	c := newContract(t)
	tests := []struct {
		name      string
		readiness ReadinessChecker
		status    int
		want      string
	}{
		{"ready", fakeReadiness{}, http.StatusOK, `{"checks":{"database":"ok","migrations":"ok"},"status":"ready"}`},
		{"behind on migrations", fakeReadiness{migration: errors.New("at version 3, this build expects 6")}, http.StatusServiceUnavailable, `"migrations":"at version 3, this build expects 6"`},
		{"database down", fakeReadiness{database: errors.New("ping: refused"), migration: errors.New("not checked")}, http.StatusServiceUnavailable, `"database":"ping: refused"`},
		{"not configured", nil, http.StatusServiceUnavailable, `"status":"not_ready"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := NewServer(nil, nil, nil)
			if tt.readiness != nil {
				srv = srv.WithReadiness(tt.readiness)
			}
			h := NewRouter(srv, Options{APIKey: "k"}) // /readyz needs no key
			rec := c.do(t, h, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("got %d %s, want %d containing %s", rec.Code, rec.Body, tt.status, tt.want)
			}
		})
	}
}

// Not parallel: it replaces the global tracer provider for its duration.
func TestObserve_ProbesAreCountedButNotTracedOrLogged(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	var logs strings.Builder
	metrics := observability.NewMetrics()
	fake := &fakeBooking{res: booking.Result{Appointment: bookedAppointment("confirmed")}}
	srv := NewServer(nil, fake, observability.NewLogger(&logs)).WithReadiness(fakeReadiness{})
	h := NewRouter(srv, Options{APIKey: "k", Metrics: metrics})

	for _, path := range []string{"/metrics", "/healthz", "/readyz", "/v1/appointments/" + uuid.NewString()} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.Header.Set("X-API-Key", "k")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	var names []string
	for _, s := range recorder.Ended() {
		names = append(names, s.Name())
	}
	if len(names) != 1 || names[0] != "GET /v1/appointments/{appointmentId}" {
		t.Errorf("spans = %v, want only the appointment request", names)
	}
	if n := strings.Count(logs.String(), `"msg":"request"`); n != 1 || !strings.Contains(logs.String(), `"route":"/v1/appointments/{appointmentId}"`) {
		t.Errorf("access log lines = %d (%s), want only the appointment request", n, logs.String())
	}

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	for _, route := range []string{"/metrics", "/healthz", "/readyz"} {
		want := `http_server_request_duration_seconds_count{method="GET",route="` + route + `",status="200"} 1`
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("probe %s not counted: missing %s", route, want)
		}
	}
}
