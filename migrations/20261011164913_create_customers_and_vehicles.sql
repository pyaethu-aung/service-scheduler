-- +goose Up
-- +goose StatementBegin

CREATE TABLE customers (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    name        text        NOT NULL,
    email       text,
    phone       text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE vehicles (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    customer_id  uuid        NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    vin          text        NOT NULL UNIQUE,
    make         text        NOT NULL,
    model        text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX vehicles_customer_id_idx ON vehicles (customer_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS vehicles;
DROP TABLE IF EXISTS customers;

-- +goose StatementEnd
