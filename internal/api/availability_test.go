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

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
)

type fakeAvailability struct {
	res availability.Result
	err error
	got availability.Query
}

func (f *fakeAvailability) Availability(_ context.Context, q availability.Query) (availability.Result, error) {
	f.got = q
	return f.res, f.err
}

var (
	testDealership  = uuid.MustParse("01990000-0000-7000-8000-000000000102")
	testServiceType = uuid.MustParse("01990000-0000-7000-8000-000000000401")
)

func availabilityURL(dealership, serviceType, date string) string {
	return "/v1/dealerships/" + dealership + "/availability?serviceTypeId=" + serviceType + "&date=" + date
}

func TestContract_Availability(t *testing.T) {
	t.Parallel()

	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	found := availability.Result{
		DealershipID:  testDealership,
		ServiceTypeID: testServiceType,
		Date:          availability.Date{Year: 2026, Month: time.October, Day: 20},
		Timezone:      "Europe/London",
		Duration:      time.Hour,
		// Not in UTC on purpose: the handler must normalise.
		Starts: []time.Time{
			time.Date(2026, 10, 20, 8, 0, 0, 0, london),
			time.Date(2026, 10, 20, 8, 15, 0, 0, london),
		},
	}
	empty := found
	empty.Starts = []time.Time{}

	tests := []struct {
		name       string
		url        string
		invalid    bool // the request breaks the contract on purpose
		fake       fakeAvailability
		wantStatus int
		wantBody   []string // substrings of the response body
	}{
		{
			name:       "free starts in UTC",
			url:        availabilityURL(testDealership.String(), testServiceType.String(), "2026-10-20"),
			fake:       fakeAvailability{res: found},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"availableStarts":["2026-10-20T07:00:00Z","2026-10-20T07:15:00Z"]`,
				`"date":"2026-10-20"`, `"timezone":"Europe/London"`, `"durationMinutes":60`,
			},
		},
		{
			name:       "no free starts is an empty list, not null",
			url:        availabilityURL(testDealership.String(), testServiceType.String(), "2026-10-20"),
			fake:       fakeAvailability{res: empty},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"availableStarts":[]`},
		},
		{
			name:       "unknown dealership",
			url:        availabilityURL(uuid.NewString(), testServiceType.String(), "2026-10-20"),
			fake:       fakeAvailability{err: availability.ErrDealershipNotFound},
			wantStatus: http.StatusNotFound,
			wantBody:   []string{`"title":"Dealership not found"`, `/problems/not-found"`},
		},
		{
			name:       "unknown service type",
			url:        availabilityURL(testDealership.String(), uuid.NewString(), "2026-10-20"),
			fake:       fakeAvailability{err: availability.ErrServiceTypeNotFound},
			wantStatus: http.StatusNotFound,
			wantBody:   []string{`"title":"Service type not found"`},
		},
		{
			name: "dealership id is not a uuid", invalid: true,
			url:        availabilityURL("district-7", testServiceType.String(), "2026-10-20"),
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`/problems/invalid-request"`, `dealershipId`},
		},
		{
			name: "service type id is not a uuid", invalid: true,
			url:        availabilityURL(testDealership.String(), "oil", "2026-10-20"),
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`serviceTypeId`},
		},
		{
			name: "date is not a calendar date", invalid: true,
			url:        availabilityURL(testDealership.String(), testServiceType.String(), "2026-02-30"),
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`date`},
		},
		{
			name: "date is missing", invalid: true,
			url:        "/v1/dealerships/" + testDealership.String() + "/availability?serviceTypeId=" + testServiceType.String(),
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`date`},
		},
	}

	c := newContract(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := tt.fake
			h := NewRouter(NewServer(&fake, nil, slog.New(slog.DiscardHandler)))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.url, nil)

			var rec *httptest.ResponseRecorder
			if tt.invalid {
				rec = c.doInvalid(t, h, req)
			} else {
				rec = c.do(t, h, req)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body %s does not contain %s", rec.Body, want)
				}
			}
			if tt.wantStatus == http.StatusOK {
				want := availability.Query{DealershipID: testDealership, ServiceTypeID: testServiceType, Date: fake.res.Date}
				if fake.got != want {
					t.Errorf("service got %+v, want %+v", fake.got, want)
				}
			}
		})
	}
}

// Unexpected errors are not part of the contract; they still answer in
// problem+json and do not leak the cause.
func TestAvailability_InternalError(t *testing.T) {
	t.Parallel()

	fake := &fakeAvailability{err: errors.New("connection refused to 10.0.0.5")}
	var logs strings.Builder
	h := NewRouter(NewServer(fake, nil, slog.New(slog.NewJSONHandler(&logs, nil))))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		availabilityURL(testDealership.String(), testServiceType.String(), "2026-10-20"), nil))

	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %q, want 500 application/problem+json", rec.Code, rec.Header().Get("Content-Type"))
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != http.StatusInternalServerError || p.Detail != nil || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Errorf("problem = %s, want a bare 500 without the cause", rec.Body)
	}
	if !strings.Contains(logs.String(), "connection refused") {
		t.Errorf("the cause was not logged: %s", logs.String())
	}
}
