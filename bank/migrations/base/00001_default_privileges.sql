-- Privileges for every table bank_migrator creates from now on (docs/domain.md, section 13).
-- Append-only tables (ledger, transitions) revoke UPDATE and DELETE from bank_app in their own
-- migrations; bank_auditor stays read-only.

-- +goose Up
ALTER DEFAULT PRIVILEGES FOR ROLE bank_migrator IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO bank_app;

ALTER DEFAULT PRIVILEGES FOR ROLE bank_migrator IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO bank_app;

ALTER DEFAULT PRIVILEGES FOR ROLE bank_migrator IN SCHEMA public
    GRANT SELECT ON TABLES TO bank_auditor;

-- +goose Down
ALTER DEFAULT PRIVILEGES FOR ROLE bank_migrator IN SCHEMA public
    REVOKE SELECT ON TABLES FROM bank_auditor;

ALTER DEFAULT PRIVILEGES FOR ROLE bank_migrator IN SCHEMA public
    REVOKE USAGE, SELECT ON SEQUENCES FROM bank_app;

ALTER DEFAULT PRIVILEGES FOR ROLE bank_migrator IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM bank_app;
