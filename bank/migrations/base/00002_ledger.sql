-- Accounts, balances and the ledger. This is the shared base schema:
-- constraints that belong to a defense (balance floor and unique account_seq
-- for balance_locking, zero-sum and non-empty triggers for atomic_ledger) live
-- in their own defense migrations, so the naive profile runs on the same tables
-- without them. Columns that link ledger transactions to transfers, incoming
-- PIX and reconciliation arrive with the migrations that create those tables.

-- +goose Up
CREATE TYPE account_kind AS ENUM ('customer', 'system');

CREATE TYPE ledger_tx_kind AS ENUM (
    'deposit', 'internal_transfer',
    'pix_out_reserve', 'pix_out_settle', 'pix_out_refund', 'pix_out_return',
    'pix_in_credit', 'reconciliation_adjustment', 'correction'
);

CREATE TABLE accounts (
    id           uuid PRIMARY KEY,
    kind         account_kind NOT NULL,
    system_code  text UNIQUE,
    holder_name  text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT accounts_system_code_iff_system
        CHECK ((kind = 'system') = (system_code IS NOT NULL))
);

CREATE TABLE account_balances (
    account_id         uuid PRIMARY KEY REFERENCES accounts (id),
    balance_cents      bigint NOT NULL DEFAULT 0,
    min_balance_cents  bigint,
    last_entry_seq     bigint NOT NULL DEFAULT 0,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_transactions (
    id              uuid PRIMARY KEY,
    kind            ledger_tx_kind NOT NULL,
    operation_id    uuid,
    reverses_id     uuid REFERENCES ledger_transactions (id),
    client_ref      text,
    correlation_id  text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ledger_tx_has_cause CHECK (
        (kind IN ('deposit', 'internal_transfer') AND operation_id IS NOT NULL)
        OR (kind = 'correction' AND reverses_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX ledger_tx_one_per_operation
    ON ledger_transactions (operation_id) WHERE operation_id IS NOT NULL;
CREATE UNIQUE INDEX ledger_tx_one_correction
    ON ledger_transactions (reverses_id) WHERE reverses_id IS NOT NULL;
CREATE INDEX ledger_tx_by_client_ref ON ledger_transactions (client_ref);

CREATE TABLE ledger_entries (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id       uuid NOT NULL REFERENCES ledger_transactions (id),
    account_id           uuid NOT NULL REFERENCES accounts (id),
    amount_cents         bigint NOT NULL,
    account_seq          bigint NOT NULL,
    balance_after_cents  bigint NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entry_amount_not_zero CHECK (amount_cents <> 0),
    CONSTRAINT entry_seq_positive CHECK (account_seq > 0)
);

-- Keyset pagination of the statement. balance_locking adds the unique
-- constraint on the same columns; until then two writers can claim the same
-- position.
CREATE INDEX ledger_entries_by_account_seq ON ledger_entries (account_id, account_seq);
CREATE INDEX ledger_entries_by_transaction ON ledger_entries (transaction_id);

-- +goose StatementBegin
CREATE FUNCTION forbid_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME
        USING ERRCODE = 'insufficient_privilege';
END $$;
-- +goose StatementEnd

CREATE TRIGGER ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER ledger_entries_no_truncate
    BEFORE TRUNCATE ON ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER ledger_transactions_append_only
    BEFORE UPDATE OR DELETE ON ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TRIGGER ledger_transactions_no_truncate
    BEFORE TRUNCATE ON ledger_transactions
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_mutation();

REVOKE ALL ON ledger_entries, ledger_transactions FROM bank_app;
GRANT SELECT, INSERT ON ledger_entries, ledger_transactions TO bank_app;

-- The chart of system accounts. No floor: they mirror the outside world and go
-- negative when money enters the bank.
INSERT INTO accounts (id, kind, system_code) VALUES
    ('00000000-0000-7000-8000-000000000001', 'system', 'sys_external_cash'),
    ('00000000-0000-7000-8000-000000000002', 'system', 'sys_pix_out_transit'),
    ('00000000-0000-7000-8000-000000000003', 'system', 'sys_partner_settlement'),
    ('00000000-0000-7000-8000-000000000004', 'system', 'sys_reconciliation_suspense');

INSERT INTO account_balances (account_id, min_balance_cents)
SELECT id, NULL FROM accounts WHERE kind = 'system';

-- +goose Down
DROP TABLE ledger_entries;
DROP TABLE ledger_transactions;
DROP TABLE account_balances;
DROP TABLE accounts;
DROP FUNCTION forbid_mutation();
DROP TYPE ledger_tx_kind;
DROP TYPE account_kind;
