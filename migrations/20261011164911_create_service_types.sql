-- +goose Up
-- +goose StatementBegin

CREATE TABLE service_types (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    code              text        NOT NULL UNIQUE,
    name              text        NOT NULL,
    duration_minutes  integer     NOT NULL CHECK (duration_minutes > 0),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS service_types;

-- +goose StatementEnd
