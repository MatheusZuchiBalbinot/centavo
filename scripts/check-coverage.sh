#!/usr/bin/env bash
# Fails when a coverage profile's total falls below a floor. The floors sit a
# few points under today's numbers: they stop a change that drops tests, not
# one that adds a little untested glue.
set -euo pipefail

if [[ $# -ne 2 ]]; then
    echo "usage: $0 <coverage profile> <floor percent>" >&2
    exit 2
fi

profile=$1
floor=$2

total=$(go tool cover -func="$profile" | awk '/^total:/ { sub("%", "", $NF); print $NF }')

if awk -v total="$total" -v floor="$floor" 'BEGIN { exit !(total < floor) }'; then
    echo "coverage ${total}% is below the ${floor}% floor" >&2
    exit 1
fi

echo "ok: coverage ${total}% (floor ${floor}%)"
