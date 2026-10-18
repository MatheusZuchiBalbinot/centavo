-- Reconciliation with the partner's statement. The uniqueness that keeps an
-- entry from being reconciled twice (partner_seq, one pending item per missing
-- transfer) is the second line of the fencing defense and lives in its
-- migration.

-- +goose Up
CREATE TYPE divergence_kind AS ENUM (
    'matched', 'missing_settlement', 'missing_return', 'missing_incoming',
    'duplicate_at_partner', 'amount_mismatch', 'unknown_entry', 'missing_at_partner'
);

CREATE TABLE reconciliation_cursor (
    singleton         boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    last_partner_seq  bigint NOT NULL DEFAULT 0
);

INSERT INTO reconciliation_cursor DEFAULT VALUES;

CREATE TABLE reconciliation_runs (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_partner_seq  bigint NOT NULL,
    to_partner_seq    bigint NOT NULL,
    fencing_token     bigint NOT NULL,
    started_at        timestamptz NOT NULL DEFAULT now(),
    finished_at       timestamptz,
    CONSTRAINT run_range_valid CHECK (to_partner_seq >= from_partner_seq)
);

CREATE TABLE reconciliation_items (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id              bigint NOT NULL REFERENCES reconciliation_runs (id),
    partner_seq         bigint,
    end_to_end_id       text NOT NULL,
    partner_entry_kind  text,
    amount_cents        bigint NOT NULL,
    divergence          divergence_kind NOT NULL,
    action              text NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT item_has_partner_seq_unless_missing
        CHECK ((divergence = 'missing_at_partner') = (partner_seq IS NULL))
);

-- The classification counts what was already booked for an end_to_end_id.
CREATE INDEX reconciliation_items_by_e2e ON reconciliation_items (end_to_end_id, partner_entry_kind);

ALTER TABLE ledger_transactions
    ADD COLUMN reconciliation_item_id bigint REFERENCES reconciliation_items (id);

ALTER TABLE ledger_transactions DROP CONSTRAINT ledger_tx_has_cause;

ALTER TABLE ledger_transactions ADD CONSTRAINT ledger_tx_has_cause CHECK (
    (kind IN ('deposit', 'internal_transfer') AND operation_id IS NOT NULL)
    OR (kind = 'pix_out_reserve' AND external_transfer_id IS NOT NULL AND operation_id IS NOT NULL)
    OR (kind IN ('pix_out_settle', 'pix_out_refund', 'pix_out_return') AND external_transfer_id IS NOT NULL)
    OR (kind = 'pix_in_credit' AND incoming_transfer_id IS NOT NULL)
    OR (kind = 'reconciliation_adjustment' AND reconciliation_item_id IS NOT NULL)
    OR (kind = 'correction' AND reverses_id IS NOT NULL)
);

-- +goose Down
ALTER TABLE ledger_transactions DROP CONSTRAINT ledger_tx_has_cause;
ALTER TABLE ledger_transactions ADD CONSTRAINT ledger_tx_has_cause CHECK (
    (kind IN ('deposit', 'internal_transfer') AND operation_id IS NOT NULL)
    OR (kind = 'pix_out_reserve' AND external_transfer_id IS NOT NULL AND operation_id IS NOT NULL)
    OR (kind IN ('pix_out_settle', 'pix_out_refund', 'pix_out_return') AND external_transfer_id IS NOT NULL)
    OR (kind = 'pix_in_credit' AND incoming_transfer_id IS NOT NULL)
    OR (kind = 'correction' AND reverses_id IS NOT NULL)
);
ALTER TABLE ledger_transactions DROP COLUMN reconciliation_item_id;
DROP TABLE reconciliation_items;
DROP TABLE reconciliation_runs;
DROP TABLE reconciliation_cursor;
DROP TYPE divergence_kind;
