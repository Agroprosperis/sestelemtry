#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_ROOT="${SESTELEMETRY_INSTALL_ROOT:-/opt/sestelemetry}"
TARGET_DEPLOY_DIR="$TARGET_ROOT/deploy"
TARGET_ENV_FILE="$TARGET_DEPLOY_DIR/.env.service"
TARGET_CONFIG_DIR="/etc/sestelemetry"
TARGET_REGISTERS_DIR="$TARGET_CONFIG_DIR/registers"

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Missing required command: $cmd" >&2
    exit 1
  fi
}

print_step() {
  echo "==> $*"
}

require_cmd sudo
require_cmd rsync
require_cmd systemctl
require_cmd docker

print_step "Syncing project to $TARGET_ROOT"
sudo mkdir -p "$TARGET_ROOT"
sudo rsync -a "$ROOT_DIR/" "$TARGET_ROOT/"

print_step "Ensuring external Modbus config paths exist"
sudo mkdir -p "$TARGET_REGISTERS_DIR"

if ! sudo test -f "$TARGET_CONFIG_DIR/config.yaml"; then
  print_step "Installing initial collector config to $TARGET_CONFIG_DIR/config.yaml"
  sudo cp "$TARGET_ROOT/config.docker.yaml" "$TARGET_CONFIG_DIR/config.yaml"
fi

if ! sudo find "$TARGET_REGISTERS_DIR" -mindepth 1 -maxdepth 1 | read -r _; then
  print_step "Installing initial register catalogs to $TARGET_REGISTERS_DIR"
  sudo cp -r "$TARGET_ROOT/registers/." "$TARGET_REGISTERS_DIR/"
fi

if ! sudo test -f "$TARGET_ENV_FILE"; then
  print_step "Creating $TARGET_ENV_FILE from service.env.example"
  sudo cp "$TARGET_DEPLOY_DIR/service.env.example" "$TARGET_ENV_FILE"
  sudo chmod 600 "$TARGET_ENV_FILE"
fi

print_step "Installing systemd unit"
sudo cp "$TARGET_DEPLOY_DIR/sestelemetry.service" /etc/systemd/system/sestelemetry.service
sudo systemctl daemon-reload
sudo systemctl enable sestelemetry

if sudo grep -Eq 'your-org|change-me' "$TARGET_ENV_FILE"; then
  cat <<EOF

Setup requires one edit before first start:
  sudo editor $TARGET_ENV_FILE

Set at least:
  - SESTELEMETRY_COLLECTOR_IMAGE
  - SESTELEMETRY_API_IMAGE
  - SESTELEMETRY_WEB_IMAGE
  - SESTELEMETRY_DB_PASSWORD
  - SESTELEMETRY_DATABASE_URL
  - SESTELEMETRY_WEB_API_BASE_URL
  - SESTELEMETRY_API_ALLOW_ORIGIN

Then start service:
  sudo systemctl restart sestelemetry
EOF
  exit 0
fi

print_step "Starting sestelemetry service"
sudo systemctl restart sestelemetry
sudo systemctl status sestelemetry --no-pager -l

echo
echo "Health checks:"
echo "  curl -fsS http://localhost:8080/healthz"
echo "  curl -fsS http://localhost:8080/readyz"
