-- name: BookAppointment :one
-- Verbatim from docs/system-design.md Section 5.2: picks a free bay and the
-- least-loaded free qualified technician and inserts in one statement. No
-- row means none is free; a concurrent winner surfaces as 23P01 from the
-- exclusion constraints. day_start and day_end bound the local day used to
-- rank technicians by booked time.
WITH free_bay AS (
    SELECT b.id
    FROM service_bays b
    WHERE b.dealership_id = @dealership_id AND b.active
      AND NOT EXISTS (
          SELECT 1 FROM appointments a
          WHERE a.bay_id = b.id AND a.status = 'confirmed'
            AND a.period && tstzrange(@starts_at, @ends_at, '[)'))
    ORDER BY b.id
    LIMIT 1
),
free_tech AS (
    SELECT t.id
    FROM technicians t
    JOIN technician_qualifications q
      ON q.technician_id = t.id AND q.service_type_id = @service_type_id
    WHERE t.dealership_id = @dealership_id AND t.active
      AND NOT EXISTS (
          SELECT 1 FROM appointments a
          WHERE a.technician_id = t.id AND a.status = 'confirmed'
            AND a.period && tstzrange(@starts_at, @ends_at, '[)'))
    ORDER BY (
        SELECT coalesce(sum(upper(a.period) - lower(a.period)), interval '0')
        FROM appointments a
        WHERE a.technician_id = t.id AND a.status = 'confirmed'
          AND a.period && tstzrange(@day_start, @day_end, '[)')
    ), t.id
    LIMIT 1
)
INSERT INTO appointments (dealership_id, customer_id, vehicle_id, service_type_id,
                          bay_id, technician_id, starts_at, ends_at)
SELECT @dealership_id, @customer_id, @vehicle_id, @service_type_id,
       free_bay.id, free_tech.id, @starts_at, @ends_at
FROM free_bay, free_tech
RETURNING *;

-- name: HasFreeBay :one
-- Same predicate as BookAppointment's free_bay, to explain a 409.
SELECT EXISTS (
    SELECT 1
    FROM service_bays b
    WHERE b.dealership_id = @dealership_id AND b.active
      AND NOT EXISTS (
          SELECT 1 FROM appointments a
          WHERE a.bay_id = b.id AND a.status = 'confirmed'
            AND a.period && tstzrange(sqlc.arg(starts_at)::timestamptz, sqlc.arg(ends_at)::timestamptz, '[)'))
) AS free;

-- name: GetCustomer :one
SELECT id FROM customers WHERE id = @id;

-- name: GetVehicle :one
SELECT id, customer_id FROM vehicles WHERE id = @id;

-- name: GetIdempotencyKey :one
-- Keys created before live_since have expired and are treated as absent.
SELECT key, request_hash, appointment_id, created_at
FROM idempotency_keys
WHERE key = @key AND created_at >= @live_since;

-- name: ClaimIdempotencyKey :execrows
-- Records the key in the booking transaction. An expired row with the same
-- key is replaced; a live one is kept and 0 rows are affected, which means a
-- concurrent request with the same key won.
INSERT INTO idempotency_keys (key, request_hash, appointment_id)
VALUES (@key, @request_hash, @appointment_id)
ON CONFLICT (key) DO UPDATE
SET request_hash = EXCLUDED.request_hash,
    appointment_id = EXCLUDED.appointment_id,
    created_at = now()
WHERE idempotency_keys.created_at < @live_since;

-- name: DeleteExpiredIdempotencyKeys :execrows
DELETE FROM idempotency_keys WHERE created_at < @live_since;

-- name: GetAppointmentDetails :one
SELECT a.id, a.status, a.starts_at, a.ends_at, a.customer_id, a.vehicle_id,
       d.id AS dealership_id, d.name AS dealership_name,
       st.id AS service_type_id, st.code AS service_type_code,
       st.duration_minutes AS service_type_duration_minutes,
       b.id AS bay_id, b.name AS bay_name,
       t.id AS technician_id, t.name AS technician_name
FROM appointments a
JOIN dealerships d ON d.id = a.dealership_id
JOIN service_types st ON st.id = a.service_type_id
JOIN service_bays b ON b.id = a.bay_id
JOIN technicians t ON t.id = a.technician_id
WHERE a.id = @id;
