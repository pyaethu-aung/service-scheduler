-- +goose Up
-- +goose StatementBegin

CREATE TABLE dealerships (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    name        text        NOT NULL,
    -- IANA zone name, e.g. Asia/Ho_Chi_Minh. Opening hours are local to it.
    timezone    text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- One open-close range per local weekday; a missing row means closed.
CREATE TABLE opening_hours (
    dealership_id  uuid        NOT NULL REFERENCES dealerships (id) ON DELETE RESTRICT,
    weekday        smallint    NOT NULL CHECK (weekday BETWEEN 0 AND 6), -- 0 = Sunday
    opens_at       time        NOT NULL,
    closes_at      time        NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (dealership_id, weekday),
    CHECK (closes_at > opens_at)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS opening_hours;
DROP TABLE IF EXISTS dealerships;

-- +goose StatementEnd
