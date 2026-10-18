-- Leases of the singleton loops. The token grows whenever the lease changes
-- hands or expires, so a holder that lost it can be told apart from the current
-- one. Every time comes from now(), the database's clock.

-- +goose Up
CREATE TABLE worker_leases (
    resource    text PRIMARY KEY,
    holder      text,
    token       bigint NOT NULL DEFAULT 0,
    expires_at  timestamptz,
    CONSTRAINT holder_iff_expiry CHECK ((holder IS NULL) = (expires_at IS NULL))
);

INSERT INTO worker_leases (resource) VALUES ('reconciler'), ('sweeper');

-- +goose Down
DROP TABLE worker_leases;
