-- +goose Up
-- +goose StatementBegin

-- As drawn in docs/system-design.md Section 4. Rows are written once, in the
-- booking transaction, and never updated, so there is no updated_at.
CREATE TABLE idempotency_keys (
    key             text        PRIMARY KEY,
    request_hash    text        NOT NULL,
    appointment_id  uuid        NOT NULL REFERENCES appointments (id) ON DELETE RESTRICT,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS idempotency_keys;

-- +goose StatementEnd
