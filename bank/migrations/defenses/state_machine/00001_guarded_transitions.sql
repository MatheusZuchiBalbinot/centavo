-- Defense D7, state_machine (docs/domain.md, sections 13.3, 13.5 and 14):
-- the state machine exists in the database too. An invalid transition fails
-- at the INSERT, and a transfer gets at most one reservation, settlement and
-- refund or return, however many duplicate webhooks arrive.

-- +goose Up
ALTER TABLE external_transfer_transitions
    ADD CONSTRAINT transition_is_allowed
    FOREIGN KEY (from_status, to_status)
    REFERENCES external_transfer_allowed_transitions (from_status, to_status);

CREATE UNIQUE INDEX ledger_tx_one_per_transfer_step
    ON ledger_transactions (external_transfer_id, kind) WHERE external_transfer_id IS NOT NULL;

CREATE UNIQUE INDEX ledger_tx_one_refund_or_return
    ON ledger_transactions (external_transfer_id) WHERE kind IN ('pix_out_refund', 'pix_out_return');

-- +goose Down
DROP INDEX ledger_tx_one_refund_or_return;
DROP INDEX ledger_tx_one_per_transfer_step;
ALTER TABLE external_transfer_transitions DROP CONSTRAINT transition_is_allowed;
