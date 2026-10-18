-- Defense balance_locking, balance_locking: the safety net under the row locks.
-- If a lock is ever skipped by a bug, the commit fails instead of overdrawing
-- an account or giving two entries the same position.

-- +goose Up
ALTER TABLE account_balances
    ADD CONSTRAINT balance_respects_floor
    CHECK (min_balance_cents IS NULL OR balance_cents >= min_balance_cents);

ALTER TABLE ledger_entries
    ADD CONSTRAINT entry_unique_seq_per_account UNIQUE (account_id, account_seq);

-- The unique constraint's index serves the statement's keyset pagination.
DROP INDEX ledger_entries_by_account_seq;

-- +goose Down
CREATE INDEX ledger_entries_by_account_seq ON ledger_entries (account_id, account_seq);

ALTER TABLE ledger_entries DROP CONSTRAINT entry_unique_seq_per_account;

ALTER TABLE account_balances DROP CONSTRAINT balance_respects_floor;
