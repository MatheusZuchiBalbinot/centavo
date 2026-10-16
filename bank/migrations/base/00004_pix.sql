-- External transfers (PIX out), their state machine and incoming PIX
-- (docs/domain.md, sections 13.5 and 13.6). The constraints that belong to
-- defense D7 (allowed transitions as a foreign key, one ledger transaction
-- per step, refund or return but not both) live in its own migration.

-- +goose Up
CREATE TYPE external_transfer_status AS ENUM ('created', 'sent', 'confirmed', 'failed', 'reversed');

CREATE TABLE external_transfers (
    id               uuid PRIMARY KEY,
    end_to_end_id    text NOT NULL UNIQUE,
    operation_id     uuid NOT NULL UNIQUE,
    client_ref       text NOT NULL,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    amount_cents     bigint NOT NULL,
    destination_key  text NOT NULL,
    status           external_transfer_status NOT NULL DEFAULT 'created',
    status_seq       integer NOT NULL DEFAULT 1,
    failure_code     text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT external_transfer_amount_positive CHECK (amount_cents > 0),
    CONSTRAINT failure_code_iff_failed CHECK ((status = 'failed') = (failure_code IS NOT NULL))
);

CREATE INDEX external_transfers_in_flight
    ON external_transfers (updated_at) WHERE status IN ('created', 'sent');

CREATE TABLE external_transfer_allowed_transitions (
    from_status  external_transfer_status NOT NULL,
    to_status    external_transfer_status NOT NULL,
    PRIMARY KEY (from_status, to_status)
);

INSERT INTO external_transfer_allowed_transitions (from_status, to_status) VALUES
    ('created', 'sent'), ('created', 'confirmed'), ('created', 'failed'),
    ('sent', 'confirmed'), ('sent', 'failed'),
    ('confirmed', 'reversed');

CREATE TABLE incoming_transfers (
    id               uuid PRIMARY KEY,
    end_to_end_id    text NOT NULL UNIQUE,
    account_id       uuid NOT NULL REFERENCES accounts (id),
    amount_cents     bigint NOT NULL,
    source_event_id  text NOT NULL,
    received_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT incoming_amount_positive CHECK (amount_cents > 0)
);

ALTER TABLE ledger_transactions
    ADD COLUMN external_transfer_id uuid REFERENCES external_transfers (id),
    ADD COLUMN incoming_transfer_id uuid REFERENCES incoming_transfers (id);

ALTER TABLE ledger_transactions DROP CONSTRAINT ledger_tx_has_cause;

ALTER TABLE ledger_transactions ADD CONSTRAINT ledger_tx_has_cause CHECK (
    (kind IN ('deposit', 'internal_transfer') AND operation_id IS NOT NULL)
    OR (kind = 'pix_out_reserve' AND external_transfer_id IS NOT NULL AND operation_id IS NOT NULL)
    OR (kind IN ('pix_out_settle', 'pix_out_refund', 'pix_out_return') AND external_transfer_id IS NOT NULL)
    OR (kind = 'pix_in_credit' AND incoming_transfer_id IS NOT NULL)
    OR (kind = 'correction' AND reverses_id IS NOT NULL)
);

-- Always on, in every profile: an incoming PIX is credited once (BR12).
CREATE UNIQUE INDEX ledger_tx_one_per_incoming
    ON ledger_transactions (incoming_transfer_id) WHERE incoming_transfer_id IS NOT NULL;
CREATE INDEX ledger_tx_by_external_transfer
    ON ledger_transactions (external_transfer_id) WHERE external_transfer_id IS NOT NULL;

CREATE TABLE external_transfer_transitions (
    id                     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transfer_id            uuid NOT NULL REFERENCES external_transfers (id),
    seq                    integer NOT NULL,
    from_status            external_transfer_status,
    to_status              external_transfer_status NOT NULL,
    cause                  text NOT NULL,
    ledger_transaction_id  uuid REFERENCES ledger_transactions (id),
    correlation_id         text NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT transition_unique_seq UNIQUE (transfer_id, seq),
    CONSTRAINT transition_starts_at_created
        CHECK (from_status IS NOT NULL OR (to_status = 'created' AND seq = 1))
);

CREATE TRIGGER external_transfer_transitions_append_only
    BEFORE UPDATE OR DELETE ON external_transfer_transitions
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER external_transfer_transitions_no_truncate
    BEFORE TRUNCATE ON external_transfer_transitions
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_mutation();

REVOKE ALL ON external_transfer_transitions FROM bank_app;
GRANT SELECT, INSERT ON external_transfer_transitions TO bank_app;

-- +goose Down
DROP TABLE external_transfer_transitions;
DROP INDEX ledger_tx_by_external_transfer;
DROP INDEX ledger_tx_one_per_incoming;
ALTER TABLE ledger_transactions DROP CONSTRAINT ledger_tx_has_cause;
ALTER TABLE ledger_transactions DROP COLUMN incoming_transfer_id, DROP COLUMN external_transfer_id;
ALTER TABLE ledger_transactions ADD CONSTRAINT ledger_tx_has_cause CHECK (
    (kind IN ('deposit', 'internal_transfer') AND operation_id IS NOT NULL)
    OR (kind = 'correction' AND reverses_id IS NOT NULL)
);
DROP TABLE incoming_transfers;
DROP TABLE external_transfer_allowed_transitions;
DROP TABLE external_transfers;
DROP TYPE external_transfer_status;
