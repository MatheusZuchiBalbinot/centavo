-- Defense atomic_ledger, atomic_ledger: every ledger transaction sums to zero
-- and has entries, checked at commit by deferred constraint triggers, because
-- entries are inserted one by one.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION assert_ledger_transaction_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    total bigint;
BEGIN
    SELECT coalesce(sum(amount_cents), 0) INTO total
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id;

    IF total <> 0 THEN
        RAISE EXCEPTION 'ledger transaction % is unbalanced (sum = %)', NEW.transaction_id, total
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_entries_balanced
    AFTER INSERT ON ledger_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_ledger_transaction_balanced();

-- +goose StatementBegin
CREATE FUNCTION assert_ledger_transaction_has_entries() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM ledger_entries WHERE transaction_id = NEW.id) THEN
        RAISE EXCEPTION 'ledger transaction % has no entries', NEW.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER ledger_transactions_have_entries
    AFTER INSERT ON ledger_transactions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_ledger_transaction_has_entries();

-- +goose Down
DROP TRIGGER ledger_transactions_have_entries ON ledger_transactions;
DROP FUNCTION assert_ledger_transaction_has_entries();
DROP TRIGGER ledger_entries_balanced ON ledger_entries;
DROP FUNCTION assert_ledger_transaction_balanced();
