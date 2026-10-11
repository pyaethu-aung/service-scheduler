-- Read path for availability (docs/system-design.md Section 5.1).

-- name: GetDealership :one
SELECT id, name, timezone
FROM dealerships
WHERE id = @id;

-- name: GetOpeningHours :one
-- No row means the dealership is closed on that weekday (0 = Sunday).
SELECT opens_at, closes_at
FROM opening_hours
WHERE dealership_id = @dealership_id AND weekday = @weekday;

-- name: GetServiceType :one
SELECT id, code, name, duration_minutes
FROM service_types
WHERE id = @id;

-- name: ListActiveBayIDs :many
SELECT id
FROM service_bays
WHERE dealership_id = @dealership_id AND active
ORDER BY id;

-- name: ListQualifiedTechnicianIDs :many
SELECT t.id
FROM technicians t
JOIN technician_qualifications q
  ON q.technician_id = t.id AND q.service_type_id = @service_type_id
WHERE t.dealership_id = @dealership_id AND t.active
ORDER BY t.id;

-- name: ListConfirmedAppointmentsInRange :many
-- Confirmed appointments at the dealership overlapping [window_start,
-- window_end). Served by the partial GiST index on (dealership_id, period).
SELECT bay_id, technician_id, starts_at, ends_at
FROM appointments
WHERE dealership_id = @dealership_id
  AND status = 'confirmed'
  AND period && tstzrange(sqlc.arg(window_start)::timestamptz, sqlc.arg(window_end)::timestamptz, '[)')
ORDER BY starts_at, bay_id;
