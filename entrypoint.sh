#!/usr/bin/env bash
# Sets the lab up and starts it: checks Docker, writes .env with random
# secrets (adding only the ones missing from an existing file), builds the
# images and waits until every service is healthy.
#
# Usage: ./entrypoint.sh [--env-only]
set -euo pipefail

readonly ENV_FILE=".env"
readonly PANEL_PORT=8090
readonly PANEL_URL="http://localhost:${PANEL_PORT}"
readonly WAIT_TIMEOUT_SECONDS=600
readonly SECRETS=(POSTGRES_PASSWORD BANK_MIGRATOR_PASSWORD BANK_APP_PASSWORD BANK_AUDITOR_PASSWORD REPLICATOR_PASSWORD CENTAVO_CHAOS_TOKEN PARTNER_WEBHOOK_SECRET)

cd "$(dirname "$0")"

fail() {
    echo "error: $*" >&2
    exit 1
}

random_secret() {
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
}

check_docker() {
    command -v docker >/dev/null 2>&1 || fail "Docker is not installed: https://docs.docker.com/get-docker/"
    docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is required (the 'docker compose' command)."
    docker info >/dev/null 2>&1 || fail "Docker is installed but not running. Start it and run ./entrypoint.sh again."
}

# The panel's port must be free, unless this lab already holds it.
check_port() {
    local holder

    holder=$(docker ps --filter "publish=${PANEL_PORT}" --format '{{.Names}}')

    if [[ -n "$holder" && "$holder" != centavo-lab-* ]]; then
        fail "port ${PANEL_PORT} is taken by the container ${holder}. Stop it and run ./entrypoint.sh again."
    fi

    if [[ -z "$holder" ]] && (echo >"/dev/tcp/127.0.0.1/${PANEL_PORT}") 2>/dev/null; then
        fail "port ${PANEL_PORT} is taken by another program. Free it and run ./entrypoint.sh again."
    fi
}

# Never rewrite a secret that exists: the database volume already uses it.
write_env() {
    umask 077
    touch "$ENV_FILE"

    for name in "${SECRETS[@]}"; do
        if ! grep -q "^${name}=" "$ENV_FILE"; then
            echo "${name}=$(random_secret)" >>"$ENV_FILE"
            echo "Added ${name} to ${ENV_FILE}."
        fi
    done
}

start_lab() {
    echo "Building and starting the lab. The first time takes a few minutes."

    if ! docker compose up --build --detach --wait --wait-timeout "$WAIT_TIMEOUT_SECONDS"; then
        docker compose ps --all
        fail "a service did not become healthy. See why with: docker compose logs <service>"
    fi

    echo
    echo "The lab is up. Open the panel at ${PANEL_URL}"
    echo "Stop it with: docker compose down"
}

check_docker
write_env

if [[ "${1:-}" == "--env-only" ]]; then
    exit 0
fi

check_port
start_lab
