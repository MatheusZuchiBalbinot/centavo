-- Idempotency keys (docs/domain.md, section 13.4). The table is shared by
-- every profile; the naive bank never writes to it. The trigger that refuses
-- to commit a pending key belongs to defense D3 and lives in its migration.
-- response_body is bytea, not jsonb: a retry must get the first answer byte
-- for byte, and jsonb would reorder its keys.

-- +goose Up
CREATE TYPE idempotency_status AS ENUM ('pending', 'completed', 'voided');

CREATE TABLE idempotency_keys (
    key              text PRIMARY KEY,
    endpoint         text,
    request_hash     bytea,
    status           idempotency_status NOT NULL,
    operation_id     uuid,
    response_status  smallint,
    response_body    bytea,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    CONSTRAINT idempotency_key_format CHECK (key ~ '^[A-Za-z0-9._:-]{8,128}$'),
    CONSTRAINT completed_has_response CHECK ((status = 'completed') = (response_status IS NOT NULL)),
    CONSTRAINT request_bound_unless_voided
        CHECK ((status = 'voided') = (request_hash IS NULL AND endpoint IS NULL))
);

CREATE INDEX idempotency_keys_by_expiry ON idempotency_keys (expires_at);

-- +goose Down
DROP TABLE idempotency_keys;
DROP TYPE idempotency_status;
