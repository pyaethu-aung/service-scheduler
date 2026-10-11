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

	"github.com/pyaethu-aung/service-scheduler/internal/booking"
)

type fakeBooking struct {
	res booking.Result
	err error
	got booking.Request
}

func (f *fakeBooking) Book(_ context.Context, req booking.Request) (booking.Result, error) {
	f.got = req
	return f.res, f.err
}

var (
	testCustomer = uuid.MustParse("01990000-0000-7000-8000-000000000501")
	testVehicle  = uuid.MustParse("01990000-0000-7000-8000-000000000601")
)

func bookingBody() string {
	return `{"dealershipId":"` + testDealership.String() + `","customerId":"` + testCustomer.String() +
		`","vehicleId":"` + testVehicle.String() + `","serviceTypeId":"` + testServiceType.String() +
		`","startsAt":"2026-10-20T09:00:00+07:00"}`
}

func bookedAppointment(status string) booking.Appointment {
	return booking.Appointment{
		ID:          uuid.MustParse("0199f0b4-0000-7000-8000-000000000001"),
		Status:      status,
		StartsAt:    time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC),
		EndsAt:      time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC),
		Dealership:  booking.Ref{ID: testDealership, Name: "District 7 Service Centre"},
		ServiceType: booking.ServiceTypeRef{ID: testServiceType, Code: "OIL_CHANGE", DurationMinutes: 60},
		Bay:         booking.Ref{ID: uuid.New(), Name: "Bay 2"},
		Technician:  booking.Ref{ID: uuid.New(), Name: "Nguyen Van An"},
		CustomerID:  testCustomer,
		VehicleID:   testVehicle,
	}
}

func TestContract_BookAppointment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        string // Idempotency-Key header; "-" omits it
		body       string
		invalid    bool // the request breaks the contract on purpose
		fake       fakeBooking
		wantStatus int
		wantBody   []string
	}{
		{
			name: "booked", key: "key-1", body: bookingBody(),
			fake:       fakeBooking{res: booking.Result{Appointment: bookedAppointment("confirmed")}},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"status":"confirmed"`, `"startsAt":"2026-10-20T02:00:00Z"`, `"bay":{"id":`, `"code":"OIL_CHANGE"`},
		},
		{
			name: "replay returns the current state", key: "key-1", body: bookingBody(),
			fake:       fakeBooking{res: booking.Result{Appointment: bookedAppointment("cancelled"), Replayed: true}},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"status":"cancelled"`},
		},
		{
			name: "unknown vehicle", key: "key-1", body: bookingBody(),
			fake:       fakeBooking{err: &booking.NotFoundError{Resource: "vehicle", ID: testVehicle}},
			wantStatus: http.StatusNotFound,
			wantBody:   []string{`"title":"Vehicle not found"`},
		},
		{
			name: "no bay", key: "key-1", body: bookingBody(),
			fake: fakeBooking{err: &booking.UnavailableError{Reason: booking.ReasonNoBay, Suggested: []time.Time{
				time.Date(2026, 10, 20, 10, 0, 0, 0, time.FixedZone("ICT", 7*3600)),
			}}},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"reason":"no_bay_available"`, `"suggestedStarts":["2026-10-20T03:00:00Z"]`, `/problems/slot-unavailable"`},
		},
		{
			name: "no technician, no suggestions", key: "key-1", body: bookingBody(),
			fake:       fakeBooking{err: &booking.UnavailableError{Reason: booking.ReasonNoTechnician, Suggested: []time.Time{}}},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"reason":"no_technician_available"`, `"suggestedStarts":[]`},
		},
		{
			name: "booking rules broken", key: "key-1", body: bookingBody(),
			fake: fakeBooking{err: &booking.ValidationError{Params: []booking.InvalidParam{
				{Name: "startsAt", Reason: "is in the past"}, {Name: "vehicleId", Reason: "vehicle does not belong to the customer"},
			}}},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   []string{`"invalidParams":[{"name":"startsAt","reason":"is in the past"},{"name":"vehicleId"`, `/problems/validation-failed"`},
		},
		{
			name: "missing Idempotency-Key is 422, not 400", key: "-", body: bookingBody(), invalid: true,
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   []string{`"name":"Idempotency-Key"`},
		},
		{
			name: "Idempotency-Key too long", key: strings.Repeat("k", 256), body: bookingBody(), invalid: true,
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   []string{`"name":"Idempotency-Key","reason":"must be at most 255 characters"`},
		},
		{
			name: "invalid JSON", key: "key-1", body: `{"dealershipId":`, invalid: true,
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   []string{`"name":"body"`},
		},
		{
			name: "wrong field type", key: "key-1", body: `{"startsAt": 42}`, invalid: true,
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   []string{`"name":"body"`},
		},
		{
			name: "malformed uuid", key: "key-1", invalid: true,
			body:       strings.Replace(bookingBody(), testVehicle.String(), "not-a-uuid", 1),
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   []string{`"invalidParams"`},
		},
	}

	c := newContract(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := tt.fake
			h := NewRouter(NewServer(nil, &fake, slog.New(slog.DiscardHandler)))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/appointments", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.key != "-" {
				req.Header.Set("Idempotency-Key", tt.key)
			}

			var rec *httptest.ResponseRecorder
			if tt.invalid {
				rec = c.doInvalid(t, h, req)
			} else {
				rec = c.do(t, h, req)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body %s does not contain %s", rec.Body, want)
				}
			}
			if !tt.invalid {
				want := booking.Request{
					IdempotencyKey: "key-1", DealershipID: testDealership, CustomerID: testCustomer,
					VehicleID: testVehicle, ServiceTypeID: testServiceType,
				}
				got := fake.got
				if got.StartsAt.IsZero() || !got.StartsAt.Equal(time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)) {
					t.Errorf("startsAt = %s", got.StartsAt)
				}
				got.StartsAt = time.Time{}
				if got != want {
					t.Errorf("service got %+v, want %+v", got, want)
				}
			}
		})
	}
}

func TestBookAppointment_InternalError(t *testing.T) {
	t.Parallel()
	h := NewRouter(NewServer(nil, &fakeBooking{err: errors.New("pool exhausted")}, slog.New(slog.DiscardHandler)))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/appointments", strings.NewReader(bookingBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "pool") {
		t.Fatalf("got %d %s, want a bare 500 problem", rec.Code, rec.Body)
	}
}

func TestParamName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want string
	}{
		{&RequiredHeaderError{ParamName: "Idempotency-Key"}, "Idempotency-Key"},
		{&InvalidParamFormatError{ParamName: "date"}, "date"},
		{&RequiredParamError{ParamName: "serviceTypeId"}, "serviceTypeId"},
		{&json.UnmarshalTypeError{Field: "startsAt"}, "startsAt"},
		{errors.New("unexpected EOF"), "body"},
	}
	for _, tt := range tests {
		if got := paramName(tt.err); got != tt.want {
			t.Errorf("paramName(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
