-- name: CreateAppointment :one
-- Inserts an appointment on an explicit bay and technician. The exclusion
-- constraints reject any overlap with a confirmed appointment (23P01).
INSERT INTO appointments (dealership_id, customer_id, vehicle_id, service_type_id,
                          bay_id, technician_id, starts_at, ends_at)
VALUES (@dealership_id, @customer_id, @vehicle_id, @service_type_id,
        @bay_id, @technician_id, @starts_at, @ends_at)
RETURNING *;

-- name: CancelAppointment :one
-- Moves the row out of the partial exclusion constraints, which frees its
-- bay and technician. Cancelling twice keeps the first cancelled_at.
UPDATE appointments
SET status = 'cancelled',
    cancelled_at = coalesce(cancelled_at, now()),
    updated_at = now()
WHERE id = @id
RETURNING *;
