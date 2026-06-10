#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/compose.production.yml"
COMPOSE_ARGS=(-f "$COMPOSE_FILE")
REQUESTED_IMAGE="${CPA_MANAGER_PLUS_IMAGE:-}"

mkdir -p "$ROOT_DIR"

if [[ ! -f "$ROOT_DIR/.env" && -f "$ROOT_DIR/.env.example" ]]; then
  cp "$ROOT_DIR/.env.example" "$ROOT_DIR/.env"
  chmod 600 "$ROOT_DIR/.env"
fi

if [[ -f "$ROOT_DIR/.env" ]]; then
  # shellcheck disable=SC1091
  source "$ROOT_DIR/.env"
fi

if [[ -n "$REQUESTED_IMAGE" ]]; then
  CPA_MANAGER_PLUS_IMAGE="$REQUESTED_IMAGE"
fi

: "${CPA_MANAGER_PLUS_IMAGE:?CPA_MANAGER_PLUS_IMAGE must be set}"
: "${TAILSCALE_BIND_IP:?TAILSCALE_BIND_IP must be set}"
: "${CPA_MANAGER_PLUS_DATA_DIR:?CPA_MANAGER_PLUS_DATA_DIR must be set}"

GATEWAY_NETWORK="${GATEWAY_NETWORK:-vps-gateway}"
COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-cpa-manager-plus}"
export CPA_MANAGER_PLUS_IMAGE GATEWAY_NETWORK COMPOSE_PROJECT_NAME

mkdir -p "$CPA_MANAGER_PLUS_DATA_DIR"
docker network inspect "$GATEWAY_NETWORK" >/dev/null 2>&1 || docker network create "$GATEWAY_NETWORK" >/dev/null

existing_container="$(docker ps -a --format '{{.Names}}' | grep -x 'cpa-manager-plus' || true)"
if [[ -n "$existing_container" ]]; then
  existing_project="$(docker inspect cpa-manager-plus --format '{{ index .Config.Labels "com.docker.compose.project" }}' 2>/dev/null || true)"
  if [[ "$existing_project" != "$COMPOSE_PROJECT_NAME" ]]; then
    echo "Adopting existing cpa-manager-plus container into compose-managed deployment."
    docker stop cpa-manager-plus >/dev/null
    docker rm cpa-manager-plus >/dev/null
  fi
fi

cd "$ROOT_DIR"
docker compose "${COMPOSE_ARGS[@]}" pull cpa-manager-plus
docker compose "${COMPOSE_ARGS[@]}" up -d cpa-manager-plus
docker inspect --format 'cpa-manager-plus image={{.Config.Image}}' cpa-manager-plus
docker compose "${COMPOSE_ARGS[@]}" ps cpa-manager-plus
