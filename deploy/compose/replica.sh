#!/bin/sh
# Starts the hot standby of the replica profile. On an empty volume it first
# clones the primary through Toxiproxy's replication link, so the lab can
# slow the WAL stream down; application_name=replica is what
# synchronous_standby_names names in the sync profile.
set -eu

if [ ! -s "$PGDATA/PG_VERSION" ]; then
    until pg_basebackup --pgdata="$PGDATA" --write-recovery-conf --wal-method=stream \
        --dbname="host=toxiproxy port=15436 user=replicator password=$REPLICATOR_PASSWORD application_name=replica"; do
        echo "waiting for the primary to clone it" >&2
        rm -rf "$PGDATA"
        sleep 2
    done

    chmod 700 "$PGDATA"
fi

exec postgres -c config_file=/etc/postgresql/postgresql.conf
