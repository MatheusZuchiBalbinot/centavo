#!/usr/bin/env bash
# Generates .env with random secrets (only if it does not exist yet), then
# starts the lab. Usage: ./entrypoint.sh [--env-only]
set -euo pipefail

readonly ENV_FILE=".env"
readonly PANEL_URL="http://localhost:8090"

cd "$(dirname "$0")"

if ! docker compose version >/dev/null 2>&1; then
    echo "Docker with Compose v2 is required." >&2
    exit 1
fi

random_secret() {
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

if [[ ! -f "$ENV_FILE" ]]; then
    umask 077
    {
        echo "POSTGRES_PASSWORD=$(random_secret)"
        echo "BANK_MIGRATOR_PASSWORD=$(random_secret)"
        echo "BANK_APP_PASSWORD=$(random_secret)"
        echo "BANK_AUDITOR_PASSWORD=$(random_secret)"
    } > "$ENV_FILE"
    echo "Generated $ENV_FILE with random secrets."
fi

if [[ "${1:-}" == "--env-only" ]]; then
    exit 0
fi

docker compose up --build --detach --wait
echo "Lab panel: $PANEL_URL"
