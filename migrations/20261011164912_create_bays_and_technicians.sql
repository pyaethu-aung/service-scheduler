-- +goose Up
-- +goose StatementBegin

CREATE TABLE service_bays (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    dealership_id  uuid        NOT NULL REFERENCES dealerships (id) ON DELETE RESTRICT,
    name           text        NOT NULL,
    active         boolean     NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX service_bays_dealership_id_idx ON service_bays (dealership_id);

CREATE TABLE technicians (
    id             uuid        PRIMARY KEY DEFAULT uuidv7(),
    dealership_id  uuid        NOT NULL REFERENCES dealerships (id) ON DELETE RESTRICT,
    name           text        NOT NULL,
    active         boolean     NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX technicians_dealership_id_idx ON technicians (dealership_id);

-- A technician is qualified for a service type when a row exists (A2).
CREATE TABLE technician_qualifications (
    technician_id    uuid        NOT NULL REFERENCES technicians (id) ON DELETE RESTRICT,
    service_type_id  uuid        NOT NULL REFERENCES service_types (id) ON DELETE RESTRICT,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (technician_id, service_type_id)
);

-- The primary key serves lookups by technician; this serves "who is
-- qualified for this service type".
CREATE INDEX technician_qualifications_service_type_id_idx
    ON technician_qualifications (service_type_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS technician_qualifications;
DROP TABLE IF EXISTS technicians;
DROP TABLE IF EXISTS service_bays;

-- +goose StatementEnd
