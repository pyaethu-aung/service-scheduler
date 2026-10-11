package booking

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pyaethu-aung/service-scheduler/internal/availability"
	"github.com/pyaethu-aung/service-scheduler/internal/db"
)

var (
	dealershipID  = uuid.MustParse("01990000-0000-7000-8000-000000000101")
	customerID    = uuid.MustParse("01990000-0000-7000-8000-000000000501")
	otherCustomer = uuid.MustParse("01990000-0000-7000-8000-000000000502")
	vehicleID     = uuid.MustParse("01990000-0000-7000-8000-000000000601")
	serviceTypeID = uuid.MustParse("01990000-0000-7000-8000-000000000401")
	errStore      = errors.New("store unavailable")
)

// now is Monday 2026-10-12 00:00 UTC; the Saigon site opens 08:00-17:00
// (UTC+7) Monday to Saturday.
var now = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

func z(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func hm(h int64) pgtype.Time { return pgtype.Time{Microseconds: h * 3600 * 1_000_000, Valid: true} }

type bookResult struct {
	id  uuid.UUID
	err error
}

type fakeStore struct {
	vehicleOwner uuid.UUID
	keys         map[string]db.IdempotencyKey
	freeBay      bool
	results      []bookResult // returned by successive Book calls
	bookCalls    int
	booked       []db.BookAppointmentParams
	failOn       string // method name that returns errStore
	missing      map[uuid.UUID]bool
	cancelled    map[uuid.UUID]bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		vehicleOwner: customerID,
		keys:         map[string]db.IdempotencyKey{},
		missing:      map[uuid.UUID]bool{},
		cancelled:    map[uuid.UUID]bool{},
	}
}

func (f *fakeStore) fail(method string) error {
	if f.failOn == method {
		return errStore
	}
	return nil
}

func (f *fakeStore) GetDealership(_ context.Context, id uuid.UUID) (db.GetDealershipRow, error) {
	if err := f.fail("GetDealership"); err != nil {
		return db.GetDealershipRow{}, err
	}
	if id != dealershipID {
		return db.GetDealershipRow{}, pgx.ErrNoRows
	}
	return db.GetDealershipRow{ID: id, Name: "District 7", Timezone: "Asia/Ho_Chi_Minh"}, nil
}

func (f *fakeStore) GetCustomer(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	if id != customerID && id != otherCustomer {
		return uuid.Nil, pgx.ErrNoRows
	}
	return id, nil
}

func (f *fakeStore) GetVehicle(_ context.Context, id uuid.UUID) (db.GetVehicleRow, error) {
	if id != vehicleID {
		return db.GetVehicleRow{}, pgx.ErrNoRows
	}
	return db.GetVehicleRow{ID: id, CustomerID: f.vehicleOwner}, nil
}

func (f *fakeStore) GetServiceType(_ context.Context, id uuid.UUID) (db.GetServiceTypeRow, error) {
	if id != serviceTypeID {
		return db.GetServiceTypeRow{}, pgx.ErrNoRows
	}
	return db.GetServiceTypeRow{ID: id, Code: "OIL_CHANGE", Name: "Oil change", DurationMinutes: 60}, nil
}

func (f *fakeStore) GetOpeningHours(_ context.Context, arg db.GetOpeningHoursParams) (db.GetOpeningHoursRow, error) {
	if err := f.fail("GetOpeningHours"); err != nil {
		return db.GetOpeningHoursRow{}, err
	}
	if arg.Weekday == 0 {
		return db.GetOpeningHoursRow{}, pgx.ErrNoRows
	}
	return db.GetOpeningHoursRow{OpensAt: hm(8), ClosesAt: hm(17)}, nil
}

func (f *fakeStore) GetIdempotencyKey(_ context.Context, arg db.GetIdempotencyKeyParams) (db.IdempotencyKey, error) {
	if err := f.fail("GetIdempotencyKey"); err != nil {
		return db.IdempotencyKey{}, err
	}
	k, ok := f.keys[arg.Key]
	if !ok || k.CreatedAt.Before(arg.LiveSince) {
		return db.IdempotencyKey{}, pgx.ErrNoRows
	}
	return k, nil
}

func (f *fakeStore) GetAppointmentDetails(_ context.Context, id uuid.UUID) (db.GetAppointmentDetailsRow, error) {
	if err := f.fail("GetAppointmentDetails"); err != nil {
		return db.GetAppointmentDetailsRow{}, err
	}
	if f.missing[id] {
		return db.GetAppointmentDetailsRow{}, pgx.ErrNoRows
	}
	status := "confirmed"
	if f.cancelled[id] {
		status = "cancelled"
	}
	return db.GetAppointmentDetailsRow{
		ID: id, Status: status,
		StartsAt: z("2026-10-20T02:00:00Z"), EndsAt: z("2026-10-20T03:00:00Z"),
		CustomerID: customerID, VehicleID: vehicleID,
		DealershipID: dealershipID, DealershipName: "District 7",
		ServiceTypeID: serviceTypeID, ServiceTypeCode: "OIL_CHANGE", ServiceTypeDurationMinutes: 60,
		BayName: "Bay 2", TechnicianName: "Nguyen Van An",
	}, nil
}

func (f *fakeStore) HasFreeBay(context.Context, db.HasFreeBayParams) (bool, error) {
	if err := f.fail("HasFreeBay"); err != nil {
		return false, err
	}
	return f.freeBay, nil
}

func (f *fakeStore) CancelAppointment(_ context.Context, id uuid.UUID) (db.Appointment, error) {
	if err := f.fail("CancelAppointment"); err != nil {
		return db.Appointment{}, err
	}
	if f.missing[id] {
		return db.Appointment{}, pgx.ErrNoRows
	}
	f.cancelled[id] = true
	return db.Appointment{ID: id, Status: "cancelled"}, nil
}

func (f *fakeStore) DeleteExpiredIdempotencyKeys(_ context.Context, liveSince time.Time) (int64, error) {
	if err := f.fail("DeleteExpiredIdempotencyKeys"); err != nil {
		return 0, err
	}
	var n int64
	for k, v := range f.keys {
		if v.CreatedAt.Before(liveSince) {
			delete(f.keys, k)
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) Book(_ context.Context, arg db.BookAppointmentParams, key, hash string, _ time.Time) (uuid.UUID, error) {
	f.bookCalls++
	f.booked = append(f.booked, arg)
	r := bookResult{id: uuid.New()}
	if len(f.results) > 0 {
		r, f.results = f.results[0], f.results[1:]
	}
	if r.err == nil {
		f.keys[key] = db.IdempotencyKey{Key: key, RequestHash: hash, AppointmentID: r.id, CreatedAt: now}
	}
	return r.id, r.err
}

type fakeAvailability struct {
	starts []time.Time
	err    error
	got    availability.Query
}

func (f *fakeAvailability) Availability(_ context.Context, q availability.Query) (availability.Result, error) {
	f.got = q
	return availability.Result{Starts: f.starts}, f.err
}

func newService(store *fakeStore, avail *fakeAvailability) *Service {
	s := New(store, avail, 15*time.Minute, func() time.Time { return now })
	s.pause = func(context.Context, int) error { return nil }
	return s
}

func validRequest() Request {
	return Request{
		IdempotencyKey: "key-1",
		DealershipID:   dealershipID,
		CustomerID:     customerID,
		VehicleID:      vehicleID,
		ServiceTypeID:  serviceTypeID,
		StartsAt:       z("2026-10-20T09:00:00+07:00"), // Tuesday 09:00 local
	}
}

func TestBook_Success(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	res, err := newService(store, &fakeAvailability{}).Book(t.Context(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Replayed || res.Appointment.Bay.Name != "Bay 2" || res.Appointment.ServiceType.DurationMinutes != 60 {
		t.Errorf("result = %+v", res)
	}
	if store.bookCalls != 1 {
		t.Fatalf("Book called %d times, want 1", store.bookCalls)
	}
	p := store.booked[0]
	// Stored in UTC, for the whole duration, ranked within the local day.
	if !p.StartsAt.Equal(z("2026-10-20T02:00:00Z")) || p.StartsAt.Location() != time.UTC || !p.EndsAt.Equal(z("2026-10-20T03:00:00Z")) {
		t.Errorf("interval = [%s, %s)", p.StartsAt, p.EndsAt)
	}
	if p.DayStart != z("2026-10-19T17:00:00Z") || p.DayEnd != z("2026-10-20T17:00:00Z") {
		t.Errorf("day = [%v, %v), want the local day in UTC", p.DayStart, p.DayEnd)
	}
}

func TestBook_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Request, *fakeStore)
		want   map[string]string // param name -> substring of reason
	}{
		{
			name:   "every field missing",
			mutate: func(r *Request, _ *fakeStore) { *r = Request{IdempotencyKey: "  "} },
			want: map[string]string{
				"Idempotency-Key": "required", "dealershipId": "required", "customerId": "required",
				"vehicleId": "required", "serviceTypeId": "required", "startsAt": "required",
			},
		},
		{
			name:   "vehicle belongs to someone else",
			mutate: func(r *Request, _ *fakeStore) { r.CustomerID = otherCustomer },
			want:   map[string]string{"vehicleId": "does not belong"},
		},
		{
			name:   "in the past",
			mutate: func(r *Request, _ *fakeStore) { r.StartsAt = z("2026-10-09T09:00:00+07:00") },
			want:   map[string]string{"startsAt": "in the past"},
		},
		{
			name:   "closed on Sunday",
			mutate: func(r *Request, _ *fakeStore) { r.StartsAt = z("2026-10-18T09:00:00+07:00") },
			want:   map[string]string{"startsAt": "closed on 2026-10-18"},
		},
		{
			name:   "off the grid",
			mutate: func(r *Request, _ *fakeStore) { r.StartsAt = z("2026-10-20T09:05:00+07:00") },
			want:   map[string]string{"startsAt": "15-minute grid"},
		},
		{
			name:   "ends after closing",
			mutate: func(r *Request, _ *fakeStore) { r.StartsAt = z("2026-10-20T16:15:00+07:00") },
			want:   map[string]string{"startsAt": "between 08:00 and 17:00"},
		},
		{
			name:   "before opening",
			mutate: func(r *Request, _ *fakeStore) { r.StartsAt = z("2026-10-20T07:45:00+07:00") },
			want:   map[string]string{"startsAt": "between 08:00 and 17:00"},
		},
		{
			name: "several rules at once",
			mutate: func(r *Request, _ *fakeStore) {
				r.CustomerID = otherCustomer
				r.StartsAt = z("2026-10-20T09:05:00+07:00")
			},
			want: map[string]string{"vehicleId": "does not belong", "startsAt": "grid"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := newFakeStore()
			req := validRequest()
			tt.mutate(&req, store)

			_, err := newService(store, &fakeAvailability{}).Book(t.Context(), req)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error = %v, want a ValidationError", err)
			}
			if len(ve.Params) != len(tt.want) {
				t.Errorf("params = %+v, want %d", ve.Params, len(tt.want))
			}
			for _, p := range ve.Params {
				if want, ok := tt.want[p.Name]; !ok || !strings.Contains(p.Reason, want) {
					t.Errorf("param %s: %q, want it to mention %q", p.Name, p.Reason, want)
				}
			}
			if store.bookCalls != 0 {
				t.Errorf("Book called on an invalid request")
			}
			if !strings.Contains(err.Error(), "invalid booking request") {
				t.Errorf("Error() = %q", err)
			}
		})
	}
}

func TestBook_NotFound(t *testing.T) {
	t.Parallel()
	tests := []struct {
		resource string
		mutate   func(*Request)
	}{
		{"dealership", func(r *Request) { r.DealershipID = uuid.New() }},
		{"customer", func(r *Request) { r.CustomerID = uuid.New() }},
		{"vehicle", func(r *Request) { r.VehicleID = uuid.New() }},
		{"service type", func(r *Request) { r.ServiceTypeID = uuid.New() }},
	}
	for _, tt := range tests {
		t.Run(tt.resource, func(t *testing.T) {
			t.Parallel()
			req := validRequest()
			tt.mutate(&req)
			_, err := newService(newFakeStore(), &fakeAvailability{}).Book(t.Context(), req)
			var nf *NotFoundError
			if !errors.As(err, &nf) || nf.Resource != tt.resource {
				t.Fatalf("error = %v, want %s not found", err, tt.resource)
			}
			if !strings.Contains(nf.Error(), tt.resource+" ") {
				t.Errorf("Error() = %q", nf)
			}
		})
	}
}

func TestBook_Idempotency(t *testing.T) {
	t.Parallel()

	t.Run("same key and body replays without booking", func(t *testing.T) {
		t.Parallel()
		store := newFakeStore()
		svc := newService(store, &fakeAvailability{})
		first, err := svc.Book(t.Context(), validRequest())
		if err != nil {
			t.Fatal(err)
		}
		// The same instant in another offset is the same request.
		again := validRequest()
		again.StartsAt = again.StartsAt.UTC()
		second, err := svc.Book(t.Context(), again)
		if err != nil {
			t.Fatal(err)
		}
		if !second.Replayed || second.Appointment.ID != first.Appointment.ID || store.bookCalls != 1 {
			t.Errorf("replay = %+v after %d bookings, want the first appointment and no new booking", second, store.bookCalls)
		}
	})

	t.Run("same key with a different body is rejected", func(t *testing.T) {
		t.Parallel()
		store := newFakeStore()
		svc := newService(store, &fakeAvailability{})
		if _, err := svc.Book(t.Context(), validRequest()); err != nil {
			t.Fatal(err)
		}
		other := validRequest()
		other.StartsAt = z("2026-10-20T10:00:00+07:00")
		_, err := svc.Book(t.Context(), other)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Params[0].Name != "Idempotency-Key" || store.bookCalls != 1 {
			t.Fatalf("error = %v after %d bookings, want an Idempotency-Key ValidationError", err, store.bookCalls)
		}
	})

	t.Run("an expired key books again", func(t *testing.T) {
		t.Parallel()
		store := newFakeStore()
		store.keys["key-1"] = db.IdempotencyKey{Key: "key-1", RequestHash: "stale", AppointmentID: uuid.New(), CreatedAt: now.Add(-KeyTTL - time.Second)}
		res, err := newService(store, &fakeAvailability{}).Book(t.Context(), validRequest())
		if err != nil || res.Replayed || store.bookCalls != 1 {
			t.Fatalf("result %+v, %v after %d bookings, want a fresh booking", res, err, store.bookCalls)
		}
	})

	t.Run("a concurrent request with the same key wins", func(t *testing.T) {
		t.Parallel()
		store := newFakeStore()
		winner := uuid.New()
		store.results = []bookResult{{err: ErrKeyClaimed}}
		// The winner committed the key just before our claim.
		svc := newService(store, &fakeAvailability{})
		svc.store = &claimAfterLookup{fakeStore: store, key: db.IdempotencyKey{
			Key: "key-1", RequestHash: requestHash(validRequest()), AppointmentID: winner, CreatedAt: now,
		}}
		res, err := svc.Book(t.Context(), validRequest())
		if err != nil || !res.Replayed || res.Appointment.ID != winner {
			t.Fatalf("result %+v, %v; want a replay of the winner", res, err)
		}
	})
}

// claimAfterLookup makes the key appear only after the first lookup, the way
// a concurrent request with the same key commits between our check and our
// claim.
type claimAfterLookup struct {
	*fakeStore
	key     db.IdempotencyKey
	lookups int
}

func (c *claimAfterLookup) GetIdempotencyKey(context.Context, db.GetIdempotencyKeyParams) (db.IdempotencyKey, error) {
	c.lookups++
	if c.lookups == 1 {
		return db.IdempotencyKey{}, pgx.ErrNoRows
	}
	return c.key, nil
}

func TestBook_RetriesLostRaces(t *testing.T) {
	t.Parallel()

	t.Run("succeeds on the third attempt", func(t *testing.T) {
		t.Parallel()
		store := newFakeStore()
		booked := uuid.New()
		store.results = []bookResult{{err: db.ErrSlotTaken}, {err: db.ErrSlotTaken}, {id: booked}}
		var pauses []int
		svc := newService(store, &fakeAvailability{})
		svc.pause = func(_ context.Context, attempt int) error { pauses = append(pauses, attempt); return nil }

		res, err := svc.Book(t.Context(), validRequest())
		if err != nil || res.Appointment.ID != booked {
			t.Fatalf("result %+v, %v", res, err)
		}
		if store.bookCalls != 3 || !slices.Equal(pauses, []int{1, 2}) {
			t.Errorf("%d attempts with pauses %v, want 3 attempts and pauses after 1 and 2", store.bookCalls, pauses)
		}
	})

	t.Run("gives up after three attempts with a 409", func(t *testing.T) {
		t.Parallel()
		store := newFakeStore()
		store.results = []bookResult{{err: db.ErrSlotTaken}, {err: db.ErrSlotTaken}, {err: db.ErrSlotTaken}, {id: uuid.New()}}
		store.freeBay = true
		_, err := newService(store, &fakeAvailability{}).Book(t.Context(), validRequest())
		var ue *UnavailableError
		if !errors.As(err, &ue) || ue.Reason != ReasonNoTechnician {
			t.Fatalf("error = %v, want UnavailableError(no_technician_available)", err)
		}
		if store.bookCalls != MaxAttempts {
			t.Errorf("Book called %d times, want exactly %d", store.bookCalls, MaxAttempts)
		}
	})

	t.Run("a cancelled context stops the retries", func(t *testing.T) {
		t.Parallel()
		store := newFakeStore()
		store.results = []bookResult{{err: db.ErrSlotTaken}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		svc := newService(store, &fakeAvailability{})
		svc.pause = jitterPause
		if _, err := svc.Book(ctx, validRequest()); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
}

func TestBook_Unavailable(t *testing.T) {
	t.Parallel()
	free := []time.Time{
		z("2026-10-20T01:00:00Z"), z("2026-10-20T01:15:00Z"), // 08:00, 08:15 local
		z("2026-10-20T02:30:00Z"), z("2026-10-20T03:00:00Z"), z("2026-10-20T05:00:00Z"),
	}

	tests := []struct {
		name       string
		freeBay    bool
		wantReason string
	}{
		{"no bay", false, ReasonNoBay},
		{"no technician", true, ReasonNoTechnician},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := newFakeStore()
			store.results = []bookResult{{err: pgx.ErrNoRows}}
			store.freeBay = tt.freeBay
			avail := &fakeAvailability{starts: free}

			_, err := newService(store, avail).Book(t.Context(), validRequest())
			var ue *UnavailableError
			if !errors.As(err, &ue) || ue.Reason != tt.wantReason {
				t.Fatalf("error = %v, want reason %s", err, tt.wantReason)
			}
			// Requested 02:00Z: nearest are 02:30 (30m), 01:15 (45m), and a
			// tie at 60m between 01:00 and 03:00 that goes to the earlier.
			want := []time.Time{z("2026-10-20T01:00:00Z"), z("2026-10-20T01:15:00Z"), z("2026-10-20T02:30:00Z")}
			if !slices.EqualFunc(ue.Suggested, want, time.Time.Equal) {
				t.Errorf("suggested = %v, want %v", ue.Suggested, want)
			}
			if avail.got.Date.Day != 20 || avail.got.ServiceTypeID != serviceTypeID || store.bookCalls != 1 {
				t.Errorf("availability asked for %+v after %d bookings", avail.got, store.bookCalls)
			}
			if !strings.Contains(ue.Error(), tt.wantReason) {
				t.Errorf("Error() = %q", ue)
			}
		})
	}
}

func TestNearest(t *testing.T) {
	t.Parallel()
	target := z("2026-10-20T10:00:00Z")
	starts := []time.Time{z("2026-10-20T09:00:00Z"), z("2026-10-20T10:00:00Z"), z("2026-10-20T11:00:00Z")}

	if got := nearest(starts, target, 3); !slices.EqualFunc(got, []time.Time{starts[0], starts[2]}, time.Time.Equal) {
		t.Errorf("nearest = %v, want both neighbours without the target", got)
	}
	if got := nearest(nil, target, 3); len(got) != 0 {
		t.Errorf("nearest(nil) = %v", got)
	}
}

func TestBook_StoreErrors(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"GetDealership", "GetOpeningHours", "GetIdempotencyKey", "GetAppointmentDetails", "HasFreeBay", "Book"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			store := newFakeStore()
			store.failOn = method
			if method == "Book" {
				store.results = []bookResult{{err: errStore}}
			}
			if method == "HasFreeBay" {
				store.results = []bookResult{{err: pgx.ErrNoRows}}
			}
			_, err := newService(store, &fakeAvailability{}).Book(t.Context(), validRequest())
			if !errors.Is(err, errStore) {
				t.Fatalf("error = %v, want it to wrap the store error", err)
			}
		})
	}

	t.Run("availability", func(t *testing.T) {
		t.Parallel()
		store := newFakeStore()
		store.results = []bookResult{{err: pgx.ErrNoRows}}
		_, err := newService(store, &fakeAvailability{err: errStore}).Book(t.Context(), validRequest())
		if !errors.Is(err, errStore) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestGetAndCancel(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	svc := newService(store, &fakeAvailability{})
	id, unknown := uuid.New(), uuid.New()
	store.missing[unknown] = true

	got, err := svc.Get(t.Context(), id)
	if err != nil || got.Status != "confirmed" || got.ID != id {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	first, err := svc.Cancel(t.Context(), id)
	if err != nil || first.Status != "cancelled" {
		t.Fatalf("first cancel = %+v, %v", first, err)
	}
	second, err := svc.Cancel(t.Context(), id)
	if err != nil || second != first {
		t.Errorf("second cancel = %+v, %v; want the same state %+v", second, err, first)
	}

	for name, call := range map[string]func() error{
		"get":    func() error { _, err := svc.Get(t.Context(), unknown); return err },
		"cancel": func() error { _, err := svc.Cancel(t.Context(), unknown); return err },
	} {
		var nf *NotFoundError
		if err := call(); !errors.As(err, &nf) || nf.Resource != "appointment" || nf.ID != unknown {
			t.Errorf("%s unknown: error = %v, want appointment not found", name, err)
		}
	}

	store.failOn = "CancelAppointment"
	if _, err := svc.Cancel(t.Context(), id); !errors.Is(err, errStore) {
		t.Errorf("cancel store error = %v", err)
	}
}
