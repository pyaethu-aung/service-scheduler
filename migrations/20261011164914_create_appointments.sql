-- +goose Up
-- +goose StatementBegin

-- Verbatim from docs/system-design.md Section 4.1. The two exclusion
-- constraints are the double-booking guarantee: never weaken or drop them.
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE appointments (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    dealership_id    uuid NOT NULL REFERENCES dealerships (id),
    customer_id      uuid NOT NULL REFERENCES customers (id),
    vehicle_id       uuid NOT NULL REFERENCES vehicles (id),
    service_type_id  uuid NOT NULL REFERENCES service_types (id),
    bay_id           uuid NOT NULL REFERENCES service_bays (id),
    technician_id    uuid NOT NULL REFERENCES technicians (id),
    starts_at        timestamptz NOT NULL,
    ends_at          timestamptz NOT NULL,
    period           tstzrange GENERATED ALWAYS AS (tstzrange(starts_at, ends_at, '[)')) STORED,
    status           text NOT NULL DEFAULT 'confirmed'
                     CHECK (status IN ('confirmed', 'cancelled')),
    cancelled_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),

    CONSTRAINT appointments_no_bay_overlap
        EXCLUDE USING gist (bay_id WITH =, period WITH &&)
        WHERE (status = 'confirmed'),

    CONSTRAINT appointments_no_technician_overlap
        EXCLUDE USING gist (technician_id WITH =, period WITH &&)
        WHERE (status = 'confirmed')
);

CREATE INDEX appointments_dealership_period_idx
    ON appointments USING gist (dealership_id, period)
    WHERE status = 'confirmed';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS appointments;
DROP EXTENSION IF EXISTS btree_gist;

-- +goose StatementEnd
