#!/usr/bin/env bash
# Runs once, when the data directory is initialized. Creates the three roles,
# the database of the robust stack and the replication role of the replica
# profile.
set -euo pipefail

psql --set ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    --set migrator_password="$BANK_MIGRATOR_PASSWORD" \
    --set app_password="$BANK_APP_PASSWORD" \
    --set auditor_password="$BANK_AUDITOR_PASSWORD" \
    --set replicator_password="$REPLICATOR_PASSWORD" <<'SQL'
CREATE ROLE bank_migrator LOGIN PASSWORD :'migrator_password';
CREATE ROLE bank_app LOGIN PASSWORD :'app_password';
CREATE ROLE bank_auditor LOGIN PASSWORD :'auditor_password';
CREATE ROLE replicator LOGIN REPLICATION PASSWORD :'replicator_password';

ALTER ROLE bank_app SET statement_timeout = '5s';
ALTER ROLE bank_app SET lock_timeout = '2s';
ALTER ROLE bank_auditor SET default_transaction_read_only = on;

CREATE DATABASE bank_robust OWNER bank_migrator;
REVOKE ALL ON DATABASE bank_robust FROM PUBLIC;
GRANT CONNECT ON DATABASE bank_robust TO bank_app, bank_auditor;
SQL

# Replication connections match no "all" line of pg_hba.conf.
echo "host replication replicator all scram-sha-256" >> "$PGDATA/pg_hba.conf"
