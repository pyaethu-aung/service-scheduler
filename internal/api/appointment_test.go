package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/pyaethu-aung/service-scheduler/internal/booking"
)

func TestContract_GetAndCancelAppointment(t *testing.T) {
	t.Parallel()
	id := bookedAppointment("confirmed").ID

	tests := []struct {
		name       string
		method     string
		path       string
		invalid    bool
		fake       fakeBooking
		wantStatus int
		wantBody   string
	}{
		{
			name: "get", method: http.MethodGet, path: "/v1/appointments/" + id.String(),
			fake:       fakeBooking{res: booking.Result{Appointment: bookedAppointment("confirmed")}},
			wantStatus: http.StatusOK, wantBody: `"status":"confirmed"`,
		},
		{
			name: "cancel", method: http.MethodPost, path: "/v1/appointments/" + id.String() + "/cancel",
			fake:       fakeBooking{res: booking.Result{Appointment: bookedAppointment("cancelled")}},
			wantStatus: http.StatusOK, wantBody: `"status":"cancelled"`,
		},
		{
			name: "get unknown", method: http.MethodGet, path: "/v1/appointments/" + uuid.NewString(),
			fake:       fakeBooking{err: &booking.NotFoundError{Resource: "appointment", ID: id}},
			wantStatus: http.StatusNotFound, wantBody: `"title":"Appointment not found"`,
		},
		{
			name: "cancel unknown", method: http.MethodPost, path: "/v1/appointments/" + uuid.NewString() + "/cancel",
			fake:       fakeBooking{err: &booking.NotFoundError{Resource: "appointment", ID: id}},
			wantStatus: http.StatusNotFound, wantBody: `/problems/not-found"`,
		},
		{
			name: "get with a non-UUID id is 404", method: http.MethodGet, path: "/v1/appointments/abc", invalid: true,
			wantStatus: http.StatusNotFound, wantBody: `"title":"Appointment not found"`,
		},
		{
			name: "cancel with a non-UUID id is 404", method: http.MethodPost, path: "/v1/appointments/abc/cancel", invalid: true,
			wantStatus: http.StatusNotFound, wantBody: `"title":"Appointment not found"`,
		},
	}

	c := newContract(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := tt.fake
			h := NewRouter(NewServer(nil, &fake, slog.New(slog.DiscardHandler)))
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, nil)

			var rec *httptest.ResponseRecorder
			if tt.invalid {
				rec = c.doInvalid(t, h, req)
			} else {
				rec = c.do(t, h, req)
			}
			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("got %d %s, want %d containing %s", rec.Code, rec.Body, tt.wantStatus, tt.wantBody)
			}
		})
	}
}

func TestGetAppointment_InternalError(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/v1/appointments/" + uuid.NewString(), "/v1/appointments/" + uuid.NewString() + "/cancel"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/cancel") {
			method = http.MethodPost
		}
		h := NewRouter(NewServer(nil, &fakeBooking{err: errors.New("db down")}, slog.New(slog.DiscardHandler)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, nil))
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "db down") {
			t.Errorf("%s %s: got %d %s, want a bare 500", method, path, rec.Code, rec.Body)
		}
	}
}
