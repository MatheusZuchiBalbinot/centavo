-- Defense D5, inbox (docs/domain.md, sections 13.8 and 14): the cheap
-- deduplication, by event_id. The semantic one (another event_id with the
-- same effect) comes from the state machine and the ledger's unique indexes.

-- +goose Up
ALTER TABLE inbox_events ADD CONSTRAINT inbox_dedup UNIQUE (source, event_id);

-- +goose Down
ALTER TABLE inbox_events DROP CONSTRAINT inbox_dedup;
