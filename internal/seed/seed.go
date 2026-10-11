// Package seed holds the development and demo data loaded by `make seed`.
//
// Every row has a fixed ID, and every write is an upsert, so seeding is
// idempotent: running it again converges on the same data. The IDs are
// exported for tests and the .http request collection.
package seed

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pyaethu-aung/service-scheduler/internal/db"
)

// Dealerships. Ho Chi Minh City has no DST; London has both transitions,
// which exercises local-to-UTC conversion in the slot engine.
var (
	DealershipSaigon = uuid.MustParse("01990000-0000-7000-8000-000000000101")
	DealershipLondon = uuid.MustParse("01990000-0000-7000-8000-000000000102")
)

// Service types, with different durations.
var (
	ServiceOilChange    = uuid.MustParse("01990000-0000-7000-8000-000000000401")
	ServiceTyreRotation = uuid.MustParse("01990000-0000-7000-8000-000000000402")
	ServiceAirCon       = uuid.MustParse("01990000-0000-7000-8000-000000000403")
	ServiceBrakes       = uuid.MustParse("01990000-0000-7000-8000-000000000404")
)

// Customers and vehicles. CustomerAn owns two vehicles.
var (
	CustomerAn    = uuid.MustParse("01990000-0000-7000-8000-000000000501")
	CustomerMai   = uuid.MustParse("01990000-0000-7000-8000-000000000502")
	CustomerOlive = uuid.MustParse("01990000-0000-7000-8000-000000000503")
	CustomerSam   = uuid.MustParse("01990000-0000-7000-8000-000000000504")

	VehicleAnSedan   = uuid.MustParse("01990000-0000-7000-8000-000000000601")
	VehicleAnScooter = uuid.MustParse("01990000-0000-7000-8000-000000000602")
	VehicleMai       = uuid.MustParse("01990000-0000-7000-8000-000000000603")
	VehicleOlive     = uuid.MustParse("01990000-0000-7000-8000-000000000604")
	VehicleSam       = uuid.MustParse("01990000-0000-7000-8000-000000000605")
)

type dealership struct {
	id       uuid.UUID
	name     string
	timezone string
	// hours maps a weekday (0 = Sunday) to local opening and closing times;
	// a missing weekday is closed.
	hours map[int16][2]pgtype.Time
	bays  []bay
	techs []technician
}

type bay struct {
	id   uuid.UUID
	name string
}

type technician struct {
	id     uuid.UUID
	name   string
	skills []uuid.UUID
}

type serviceType struct {
	id       uuid.UUID
	code     string
	name     string
	duration int32
}

type customer struct {
	id           uuid.UUID
	name         string
	email, phone string
	vehicles     []vehicle
}

type vehicle struct {
	id               uuid.UUID
	vin, make, model string
}

var serviceTypes = []serviceType{
	{ServiceOilChange, "OIL_CHANGE", "Oil change", 60},
	{ServiceTyreRotation, "TYRE_ROTATION", "Tyre rotation", 45},
	{ServiceAirCon, "AIR_CON_SERVICE", "Air conditioning service", 90},
	{ServiceBrakes, "BRAKE_SERVICE", "Brake service", 120},
}

var dealerships = []dealership{
	{
		id:       DealershipSaigon,
		name:     "District 7 Service Centre",
		timezone: "Asia/Ho_Chi_Minh",
		hours: map[int16][2]pgtype.Time{
			1: {hm(8, 0), hm(17, 0)}, 2: {hm(8, 0), hm(17, 0)}, 3: {hm(8, 0), hm(17, 0)},
			4: {hm(8, 0), hm(17, 0)}, 5: {hm(8, 0), hm(17, 0)}, 6: {hm(8, 0), hm(12, 0)},
		},
		bays: []bay{
			{uuid.MustParse("01990000-0000-7000-8000-000000000201"), "Bay 1"},
			{uuid.MustParse("01990000-0000-7000-8000-000000000202"), "Bay 2"},
			{uuid.MustParse("01990000-0000-7000-8000-000000000203"), "Bay 3"},
		},
		techs: []technician{
			{uuid.MustParse("01990000-0000-7000-8000-000000000301"), "Nguyen Van An",
				[]uuid.UUID{ServiceOilChange, ServiceTyreRotation, ServiceAirCon, ServiceBrakes}},
			{uuid.MustParse("01990000-0000-7000-8000-000000000302"), "Tran Thi Binh",
				[]uuid.UUID{ServiceOilChange, ServiceTyreRotation}},
			{uuid.MustParse("01990000-0000-7000-8000-000000000303"), "Le Van Cuong",
				[]uuid.UUID{ServiceAirCon, ServiceBrakes}},
			{uuid.MustParse("01990000-0000-7000-8000-000000000304"), "Pham Thi Dung",
				[]uuid.UUID{ServiceOilChange, ServiceBrakes}},
		},
	},
	{
		id:       DealershipLondon,
		name:     "Islington Service Centre",
		timezone: "Europe/London",
		hours: map[int16][2]pgtype.Time{
			1: {hm(8, 0), hm(18, 0)}, 2: {hm(8, 0), hm(18, 0)}, 3: {hm(8, 0), hm(18, 0)},
			4: {hm(8, 0), hm(18, 0)}, 5: {hm(8, 0), hm(18, 0)}, 6: {hm(9, 0), hm(13, 0)},
		},
		bays: []bay{
			{uuid.MustParse("01990000-0000-7000-8000-000000000204"), "Bay A"},
			{uuid.MustParse("01990000-0000-7000-8000-000000000205"), "Bay B"},
			{uuid.MustParse("01990000-0000-7000-8000-000000000206"), "Bay C"},
		},
		techs: []technician{
			{uuid.MustParse("01990000-0000-7000-8000-000000000305"), "Alice Morgan",
				[]uuid.UUID{ServiceOilChange, ServiceTyreRotation, ServiceAirCon, ServiceBrakes}},
			{uuid.MustParse("01990000-0000-7000-8000-000000000306"), "Ben Carter",
				[]uuid.UUID{ServiceOilChange, ServiceTyreRotation}},
			{uuid.MustParse("01990000-0000-7000-8000-000000000307"), "Chloe Hughes",
				[]uuid.UUID{ServiceAirCon}},
			{uuid.MustParse("01990000-0000-7000-8000-000000000308"), "Daniel Price",
				[]uuid.UUID{ServiceOilChange, ServiceTyreRotation, ServiceBrakes}},
		},
	},
}

var customers = []customer{
	{CustomerAn, "Nguyen Minh An", "an@example.com", "+84 90 000 0001", []vehicle{
		{VehicleAnSedan, "SEEDVIN0000000001", "Toyota", "Vios"},
		{VehicleAnScooter, "SEEDVIN0000000002", "Honda", "City"},
	}},
	{CustomerMai, "Tran Thi Mai", "mai@example.com", "+84 90 000 0002", []vehicle{
		{VehicleMai, "SEEDVIN0000000003", "Mazda", "CX-5"},
	}},
	{CustomerOlive, "Olivia Bennett", "olivia@example.com", "+44 20 0000 0003", []vehicle{
		{VehicleOlive, "SEEDVIN0000000004", "Ford", "Focus"},
	}},
	{CustomerSam, "Samuel Okafor", "samuel@example.com", "+44 20 0000 0004", []vehicle{
		{VehicleSam, "SEEDVIN0000000005", "Volkswagen", "Golf"},
	}},
}

// step is one labelled upsert, so a failure names the row that broke.
type step struct {
	label string
	exec  func(context.Context, *db.Queries) error
}

// Run upserts every seed row. Callers run it inside a transaction so a
// failure leaves the database unchanged.
func Run(ctx context.Context, q *db.Queries) error {
	for _, s := range steps() {
		if err := s.exec(ctx, q); err != nil {
			return fmt.Errorf("seed %s: %w", s.label, err)
		}
	}
	return nil
}

// steps lists the upserts in dependency order: service types, then each
// dealership and its resources, then customers and their vehicles.
func steps() []step {
	var out []step
	add := func(label string, exec func(context.Context, *db.Queries) error) {
		out = append(out, step{label: label, exec: exec})
	}

	for _, st := range serviceTypes {
		add("service type "+st.code, func(ctx context.Context, q *db.Queries) error {
			return q.UpsertServiceType(ctx, db.UpsertServiceTypeParams{
				ID: st.id, Code: st.code, Name: st.name, DurationMinutes: st.duration,
			})
		})
	}

	for _, d := range dealerships {
		add("dealership "+d.name, func(ctx context.Context, q *db.Queries) error {
			return q.UpsertDealership(ctx, db.UpsertDealershipParams{ID: d.id, Name: d.name, Timezone: d.timezone})
		})
		for weekday, h := range d.hours {
			add(fmt.Sprintf("opening hours %s weekday %d", d.name, weekday), func(ctx context.Context, q *db.Queries) error {
				return q.UpsertOpeningHours(ctx, db.UpsertOpeningHoursParams{
					DealershipID: d.id, Weekday: weekday, OpensAt: h[0], ClosesAt: h[1],
				})
			})
		}
		for _, b := range d.bays {
			add("bay "+d.name+" "+b.name, func(ctx context.Context, q *db.Queries) error {
				return q.UpsertServiceBay(ctx, db.UpsertServiceBayParams{
					ID: b.id, DealershipID: d.id, Name: b.name, Active: true,
				})
			})
		}
		for _, t := range d.techs {
			add("technician "+t.name, func(ctx context.Context, q *db.Queries) error {
				return q.UpsertTechnician(ctx, db.UpsertTechnicianParams{
					ID: t.id, DealershipID: d.id, Name: t.name, Active: true,
				})
			})
			for _, st := range t.skills {
				add("qualification "+t.name+" "+st.String(), func(ctx context.Context, q *db.Queries) error {
					return q.UpsertTechnicianQualification(ctx, db.UpsertTechnicianQualificationParams{
						TechnicianID: t.id, ServiceTypeID: st,
					})
				})
			}
		}
	}

	for _, c := range customers {
		add("customer "+c.id.String(), func(ctx context.Context, q *db.Queries) error {
			return q.UpsertCustomer(ctx, db.UpsertCustomerParams{
				ID: c.id, Name: c.name, Email: text(c.email), Phone: text(c.phone),
			})
		})
		for _, v := range c.vehicles {
			add("vehicle "+v.vin, func(ctx context.Context, q *db.Queries) error {
				return q.UpsertVehicle(ctx, db.UpsertVehicleParams{
					ID: v.id, CustomerID: c.id, Vin: v.vin, Make: v.make, Model: v.model,
				})
			})
		}
	}
	return out
}

// hm is a local wall-clock time for opening hours.
func hm(hour, minute int64) pgtype.Time {
	return pgtype.Time{Microseconds: (hour*60 + minute) * 60 * 1_000_000, Valid: true}
}

func text(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
