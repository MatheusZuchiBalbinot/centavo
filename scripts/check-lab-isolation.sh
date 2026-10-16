#!/usr/bin/env bash
# The lab (load generator, chaos bot and checker) must never import code from
# the system under test; otherwise the checker could inherit the bank's bugs.
set -euo pipefail

cd "$(dirname "$0")/../lab"

forbidden=$(go list -deps ./... | grep -E '^centavo/(bank|partner)(/|$)' || true)

if [[ -n "$forbidden" ]]; then
    echo "lab imports packages from bank or partner:" >&2
    echo "$forbidden" >&2
    exit 1
fi

echo "ok: lab does not import bank or partner"
