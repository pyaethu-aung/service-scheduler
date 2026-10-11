-- Reference data upserts. The seed command uses them so `make seed` can run
-- any number of times and converge on the same data.

-- name: UpsertDealership :exec
INSERT INTO dealerships (id, name, timezone)
VALUES (@id, @name, @timezone)
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name, timezone = EXCLUDED.timezone, updated_at = now();

-- name: UpsertOpeningHours :exec
INSERT INTO opening_hours (dealership_id, weekday, opens_at, closes_at)
VALUES (@dealership_id, @weekday, @opens_at, @closes_at)
ON CONFLICT (dealership_id, weekday) DO UPDATE
SET opens_at = EXCLUDED.opens_at, closes_at = EXCLUDED.closes_at, updated_at = now();

-- name: UpsertServiceType :exec
INSERT INTO service_types (id, code, name, duration_minutes)
VALUES (@id, @code, @name, @duration_minutes)
ON CONFLICT (id) DO UPDATE
SET code = EXCLUDED.code, name = EXCLUDED.name,
    duration_minutes = EXCLUDED.duration_minutes, updated_at = now();

-- name: UpsertServiceBay :exec
INSERT INTO service_bays (id, dealership_id, name, active)
VALUES (@id, @dealership_id, @name, @active)
ON CONFLICT (id) DO UPDATE
SET dealership_id = EXCLUDED.dealership_id, name = EXCLUDED.name,
    active = EXCLUDED.active, updated_at = now();

-- name: UpsertTechnician :exec
INSERT INTO technicians (id, dealership_id, name, active)
VALUES (@id, @dealership_id, @name, @active)
ON CONFLICT (id) DO UPDATE
SET dealership_id = EXCLUDED.dealership_id, name = EXCLUDED.name,
    active = EXCLUDED.active, updated_at = now();

-- name: UpsertTechnicianQualification :exec
INSERT INTO technician_qualifications (technician_id, service_type_id)
VALUES (@technician_id, @service_type_id)
ON CONFLICT (technician_id, service_type_id) DO NOTHING;

-- name: UpsertCustomer :exec
INSERT INTO customers (id, name, email, phone)
VALUES (@id, @name, @email, @phone)
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name, email = EXCLUDED.email, phone = EXCLUDED.phone, updated_at = now();

-- name: UpsertVehicle :exec
INSERT INTO vehicles (id, customer_id, vin, make, model)
VALUES (@id, @customer_id, @vin, @make, @model)
ON CONFLICT (id) DO UPDATE
SET customer_id = EXCLUDED.customer_id, vin = EXCLUDED.vin,
    make = EXCLUDED.make, model = EXCLUDED.model, updated_at = now();

-- name: CountReferenceData :one
SELECT
    (SELECT count(*) FROM dealerships)               AS dealerships,
    (SELECT count(*) FROM opening_hours)             AS opening_hours,
    (SELECT count(*) FROM service_types)             AS service_types,
    (SELECT count(*) FROM service_bays)              AS service_bays,
    (SELECT count(*) FROM technicians)               AS technicians,
    (SELECT count(*) FROM technician_qualifications) AS technician_qualifications,
    (SELECT count(*) FROM customers)                 AS customers,
    (SELECT count(*) FROM vehicles)                  AS vehicles;
