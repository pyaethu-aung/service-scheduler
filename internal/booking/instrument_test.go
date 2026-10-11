package booking

import (
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/observability"
)

type fakeRecorder struct {
	outcomes []string
	retries  int
}

func (f *fakeRecorder) Booking(outcome string) { f.outcomes = append(f.outcomes, outcome) }
func (f *fakeRecorder) ConflictRetry()         { f.retries++ }

func TestBook_RecordsOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setup       func(*fakeStore, *Request)
		wantOutcome string
		wantRetries int
	}{
		{"created", func(*fakeStore, *Request) {}, observability.OutcomeCreated, 0},
		{"invalid", func(_ *fakeStore, r *Request) { r.CustomerID = otherCustomer }, observability.OutcomeInvalid, 0},
		{"not found is invalid", func(_ *fakeStore, r *Request) { r.VehicleID = uuid.New() }, observability.OutcomeInvalid, 0},
		{"no capacity", func(f *fakeStore, _ *Request) { f.results = []bookResult{{err: pgx.ErrNoRows}} }, observability.OutcomeNoCapacity, 0},
		{"conflict exhausted", func(f *fakeStore, _ *Request) {
			f.results = []bookResult{{err: db.ErrSlotTaken}, {err: db.ErrSlotTaken}, {err: db.ErrSlotTaken}}
		}, observability.OutcomeConflictExhausted, 2},
		{"won after one retry", func(f *fakeStore, _ *Request) {
			f.results = []bookResult{{err: db.ErrSlotTaken}, {id: uuid.New()}}
		}, observability.OutcomeCreated, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store, rec, req := newFakeStore(), &fakeRecorder{}, validRequest()
			tt.setup(store, &req)
			_, _ = newService(store, &fakeAvailability{}).Instrument(slog.New(slog.DiscardHandler), rec).Book(t.Context(), req)
			if !slices.Equal(rec.outcomes, []string{tt.wantOutcome}) || rec.retries != tt.wantRetries {
				t.Errorf("outcomes %v retries %d, want [%s] and %d", rec.outcomes, rec.retries, tt.wantOutcome, tt.wantRetries)
			}
		})
	}
}

func TestBook_ReplayAndInternalErrors(t *testing.T) {
	t.Parallel()
	store, rec := newFakeStore(), &fakeRecorder{}
	svc := newService(store, &fakeAvailability{}).Instrument(slog.New(slog.DiscardHandler), rec)
	_, _ = svc.Book(t.Context(), validRequest())
	_, _ = svc.Book(t.Context(), validRequest())

	store.failOn = "GetDealership" // an internal error has no outcome
	if _, err := svc.Book(t.Context(), validRequest()); !errors.Is(err, errStore) {
		t.Fatal(err)
	}
	if !slices.Equal(rec.outcomes, []string{observability.OutcomeCreated, observability.OutcomeReplayed}) {
		t.Errorf("outcomes = %v", rec.outcomes)
	}
}

func TestBook_LogsIDsOnly(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	svc := newService(newFakeStore(), &fakeAvailability{}).Instrument(slog.New(slog.NewJSONHandler(&buf, nil)), &fakeRecorder{})
	res, err := svc.Book(t.Context(), validRequest())
	if err != nil {
		t.Fatal(err)
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &rec); err != nil {
		t.Fatalf("log line %q: %v", buf.String(), err)
	}
	want := map[string]any{
		"msg": "booking", "dealership_id": dealershipID.String(), "service_type_id": serviceTypeID.String(),
		"appointment_id": res.Appointment.ID.String(), "outcome": "created", "attempt": float64(1),
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("%s = %v, want %v", k, rec[k], v)
		}
	}
	for k := range rec {
		switch k {
		case "time", "level", "msg", "dealership_id", "service_type_id", "appointment_id", "outcome", "attempt":
		default:
			t.Errorf("unexpected log field %q: only IDs and the outcome may be logged", k)
		}
	}
}
