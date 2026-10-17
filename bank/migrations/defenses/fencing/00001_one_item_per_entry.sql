-- Defense D11, fencing, second line (docs/domain.md, section 13.10; BR14):
-- an entry of the partner's statement is reconciled once, even when a
-- zombie or a skewed worker holds a lease it lost, and a transfer missing
-- at the partner gets one pending item. The first line is the token checked
-- in the writing transaction.

-- +goose Up
ALTER TABLE reconciliation_items ADD CONSTRAINT reconciliation_one_item_per_entry UNIQUE (partner_seq);

CREATE UNIQUE INDEX recon_one_missing_at_partner
    ON reconciliation_items (end_to_end_id) WHERE divergence = 'missing_at_partner';

-- +goose Down
DROP INDEX recon_one_missing_at_partner;
ALTER TABLE reconciliation_items DROP CONSTRAINT reconciliation_one_item_per_entry;
