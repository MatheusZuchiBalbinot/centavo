-- Defense idempotency, idempotency: a pending key only exists inside the
-- transaction that owns it. If the code ever forgets to complete it, the commit
-- fails instead of leaving a key stuck.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION assert_no_pending_idempotency_key() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM idempotency_keys WHERE key = NEW.key AND status = 'pending') THEN
        RAISE EXCEPTION 'idempotency key % committed while pending', NEW.key
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER idempotency_keys_never_commit_pending
    AFTER INSERT OR UPDATE ON idempotency_keys
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_no_pending_idempotency_key();

-- +goose Down
DROP TRIGGER idempotency_keys_never_commit_pending ON idempotency_keys;
DROP FUNCTION assert_no_pending_idempotency_key();
