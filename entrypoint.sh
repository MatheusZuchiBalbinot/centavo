#!/usr/bin/env bash
# Generates .env with random secrets, adding only the ones missing from an
# existing file, then starts the lab. Usage: ./entrypoint.sh [--env-only]
set -euo pipefail

readonly ENV_FILE=".env"
readonly PANEL_URL="http://localhost:8090"
readonly SECRETS=(POSTGRES_PASSWORD BANK_MIGRATOR_PASSWORD BANK_APP_PASSWORD BANK_AUDITOR_PASSWORD CENTAVO_CHAOS_TOKEN)

cd "$(dirname "$0")"

if ! docker compose version >/dev/null 2>&1; then
    echo "Docker with Compose v2 is required." >&2
    exit 1
fi

random_secret() {
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

umask 077
touch "$ENV_FILE"

# Never rewrite a secret that exists: the database volume already uses it.
for name in "${SECRETS[@]}"; do
    if ! grep -q "^${name}=" "$ENV_FILE"; then
        echo "${name}=$(random_secret)" >> "$ENV_FILE"
        echo "Added ${name} to $ENV_FILE."
    fi
done

if [[ "${1:-}" == "--env-only" ]]; then
    exit 0
fi

docker compose up --build --detach --wait
echo "Lab panel: $PANEL_URL"
