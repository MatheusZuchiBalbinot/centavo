#!/usr/bin/env bash
# the safety rule that chaos code exists only in chaoslab builds: chaos code
# exists only in chaoslab builds. Production binaries must not contain the
# chaoslab sentinel; chaoslab binaries must, which proves the check can see it
# at all.
set -euo pipefail

readonly SENTINEL="centavo:chaoslab-build"
readonly MODULES=(bank partner)

root="$(cd "$(dirname "$0")/.." && pwd)"
out_dir="$(mktemp -d)"
trap 'rm -rf "$out_dir"' EXIT

for module in "${MODULES[@]}"; do
    (cd "$root/$module" && CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags "-s -w" -o "$out_dir/$module-production" "./cmd/$module")
    (cd "$root/$module" && CGO_ENABLED=0 GOWORK=off go build -trimpath -ldflags "-s -w" -tags chaoslab -o "$out_dir/$module-chaoslab" "./cmd/$module")

    if grep -q "$SENTINEL" "$out_dir/$module-production"; then
        echo "$module: production binary contains chaos code" >&2
        exit 1
    fi

    if ! grep -q "$SENTINEL" "$out_dir/$module-chaoslab"; then
        echo "$module: chaoslab binary lacks the sentinel; the check is blind" >&2
        exit 1
    fi

    echo "ok: $module production binary has no chaos code"
done
