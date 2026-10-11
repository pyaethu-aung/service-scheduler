//go:build integration

package api

// The concurrency test is the core proof of NFR1 (docs/system-design.md
// Section 12). It is protected: if it fails, report it; never loosen its
// assertions to make it pass. Run it alone with `make test-concurrency`.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/booking"
	"github.com/pyaethu-aung/service-scheduler/internal/db"
	"github.com/pyaethu-aung/service-scheduler/internal/dbtest"
)

const concurrentRequests = 50

// countingStore counts the lost races (23P01) the booking service retried.
type countingStore struct {
	*booking.PGStore
	slotTaken atomic.Int64
}

func (c *countingStore) Book(ctx context.Context, arg db.BookAppointmentParams, key, hash string, liveSince time.Time) (uuid.UUID, error) {
	id, err := c.PGStore.Book(ctx, arg, key, hash, liveSince)
	if errors.Is(err, db.ErrSlotTaken) {
		c.slotTaken.Add(1)
	}
	return id, err
}

// oneBayDealership creates a dealership with exactly one bay and five
// technicians qualified for a 60-minute service, open 08:00-18:00 UTC every
// day: the bay is the only scarce resource.
func oneBayDealership(t *testing.T, q *db.Queries) (dealership, serviceType, customer, vehicle uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	dealership, serviceType, customer, vehicle = uuid.New(), uuid.New(), uuid.New(), uuid.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	hour := func(h int64) pgtype.Time { return pgtype.Time{Microseconds: h * 3600 * 1_000_000, Valid: true} }

	must(q.UpsertDealership(ctx, db.UpsertDealershipParams{ID: dealership, Name: "One Bay Centre", Timezone: "UTC"}))
	for weekday := int16(0); weekday <= 6; weekday++ {
		must(q.UpsertOpeningHours(ctx, db.UpsertOpeningHoursParams{DealershipID: dealership, Weekday: weekday, OpensAt: hour(8), ClosesAt: hour(18)}))
	}
	must(q.UpsertServiceType(ctx, db.UpsertServiceTypeParams{ID: serviceType, Code: "RACE_" + serviceType.String(), Name: "Race", DurationMinutes: 60}))
	must(q.UpsertServiceBay(ctx, db.UpsertServiceBayParams{ID: uuid.New(), DealershipID: dealership, Name: "The only bay", Active: true}))
	for i := range 5 {
		tech := uuid.New()
		must(q.UpsertTechnician(ctx, db.UpsertTechnicianParams{ID: tech, DealershipID: dealership, Name: fmt.Sprintf("Tech %d", i), Active: true}))
		must(q.UpsertTechnicianQualification(ctx, db.UpsertTechnicianQualificationParams{TechnicianID: tech, ServiceTypeID: serviceType}))
	}
	must(q.UpsertCustomer(ctx, db.UpsertCustomerParams{ID: customer, Name: "Racer"}))
	must(q.UpsertVehicle(ctx, db.UpsertVehicleParams{ID: vehicle, CustomerID: customer, Vin: "RACE-" + vehicle.String(), Make: "Make", Model: "Model"}))
	return dealership, serviceType, customer, vehicle
}

// syncBuffer collects server logs from concurrent requests, shown only when
// the test fails.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// warm opens every pool connection up front, so the requests race inside
// PostgreSQL instead of queueing behind connection setup.
func warm(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	conns := make([]*pgxpool.Conn, 0, concurrentRequests)
	for range concurrentRequests {
		c, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatalf("warm pool: %v", err)
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		c.Release()
	}
}

func TestConcurrentBooking_OneFreeBay(t *testing.T) {
	cfg, err := pgxpool.ParseConfig(dbtest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = concurrentRequests // let every request hold a connection at once
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	warm(t, pool)

	q := db.New(pool)
	dealership, serviceType, customer, vehicle := oneBayDealership(t, q)

	longAgo := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	avail := availability.New(q, 15*time.Minute, longAgo)
	store := &countingStore{PGStore: booking.NewPGStore(pool)}
	logs := &syncBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("server logs:\n%s", logs.String())
		}
	})
	logger := slog.New(slog.NewTextHandler(logs, nil))
	h := NewRouter(NewServer(avail, booking.New(store, avail, 15*time.Minute, longAgo), logger))

	body := fmt.Sprintf(`{"dealershipId":%q,"customerId":%q,"vehicleId":%q,"serviceTypeId":%q,"startsAt":"2030-01-15T10:00:00Z"}`,
		dealership, customer, vehicle, serviceType)

	var (
		start   = make(chan struct{})
		wg      sync.WaitGroup
		results = make([]*httptest.ResponseRecorder, concurrentRequests)
	)
	for i := range concurrentRequests {
		wg.Go(func() {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/appointments", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", uuid.NewString()) // distinct requests, not retries
			rec := httptest.NewRecorder()
			<-start
			h.ServeHTTP(rec, req)
			results[i] = rec
		})
	}
	close(start)
	wg.Wait()

	var created, conflicts int
	for i, rec := range results {
		switch rec.Code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
			if !strings.Contains(rec.Body.String(), `"reason":"no_bay_available"`) {
				t.Errorf("request %d: 409 with an unexpected body: %s", i, rec.Body)
			}
		default:
			t.Errorf("request %d: unexpected status %d: %s", i, rec.Code, rec.Body)
		}
	}
	if created != 1 || conflicts != concurrentRequests-1 {
		t.Errorf("got %d x 201 and %d x 409, want exactly 1 and %d", created, conflicts, concurrentRequests-1)
	}

	rows, err := q.ListConfirmedAppointmentsInRange(t.Context(), db.ListConfirmedAppointmentsInRangeParams{
		DealershipID: dealership,
		WindowStart:  time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC),
		WindowEnd:    time.Date(2030, 1, 16, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("%d confirmed appointments in the database, want exactly 1", len(rows))
	}
	t.Logf("%d x 201, %d x 409; the exclusion constraints rejected %d racing inserts (23P01) that were retried",
		created, conflicts, store.slotTaken.Load())
}
