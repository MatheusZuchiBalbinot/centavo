-- Outbox and inbox (docs/domain.md, sections 13.7 and 13.8). The tables are
-- shared by every profile; the naive bank publishes with NOTIFY and applies
-- webhooks in the handler, so it never writes here. Deduplication by
-- event_id belongs to defense D5 and lives in its migration.

-- +goose Up
CREATE TYPE outbox_status AS ENUM ('pending', 'delivered', 'dead');
CREATE TYPE inbox_status AS ENUM ('pending', 'processed', 'conflict');

-- A global sequence: tokens only grow, so they work as fencing tokens.
CREATE SEQUENCE outbox_lease_token_seq;

CREATE TABLE outbox_messages (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    topic             text NOT NULL,
    aggregate_id      uuid NOT NULL,
    payload           jsonb NOT NULL,
    status            outbox_status NOT NULL DEFAULT 'pending',
    attempts          integer NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    lease_owner       text,
    lease_token       bigint,
    lease_expires_at  timestamptz,
    last_error        text,
    correlation_id    text NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    delivered_at      timestamptz,
    CONSTRAINT delivered_at_iff_delivered CHECK ((status = 'delivered') = (delivered_at IS NOT NULL)),
    CONSTRAINT outbox_one_message_per_aggregate UNIQUE (topic, aggregate_id)
);

CREATE INDEX outbox_ready ON outbox_messages (next_attempt_at) WHERE status = 'pending';

CREATE TABLE inbox_events (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source           text NOT NULL DEFAULT 'partner',
    event_id         text NOT NULL,
    event_type       text NOT NULL,
    end_to_end_id    text NOT NULL,
    payload          jsonb NOT NULL,
    status           inbox_status NOT NULL DEFAULT 'pending',
    outcome          text,
    attempts         integer NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    correlation_id   text NOT NULL,
    received_at      timestamptz NOT NULL DEFAULT now(),
    processed_at     timestamptz,
    CONSTRAINT processed_at_iff_done CHECK ((status = 'pending') = (processed_at IS NULL))
);

CREATE INDEX inbox_ready ON inbox_events (next_attempt_at) WHERE status = 'pending';

-- +goose Down
DROP TABLE inbox_events;
DROP TABLE outbox_messages;
DROP SEQUENCE outbox_lease_token_seq;
DROP TYPE inbox_status;
DROP TYPE outbox_status;
