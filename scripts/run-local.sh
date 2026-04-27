#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$ROOT_DIR/config.yaml}"
MODE="${1:-all}"
API_LISTEN="${API_LISTEN:-:8080}"
VITE_API_BASE_URL="${VITE_API_BASE_URL:-http://localhost:8080}"

collector_pid=""
api_pid=""
web_pid=""

usage() {
  cat <<'EOF'
Usage: scripts/run-local.sh [mode]

Modes:
  all       Start collector + api + web (default)
  backend   Start collector + api
  collector Start collector only
  api       Start api only
  web       Start web only

Environment overrides:
  CONFIG_FILE       Path to collector config (default: ./config.yaml)
  DATABASE_URL      PostgreSQL URL (required for collector/api if missing in config)
  API_LISTEN        API listen address (default: :8080)
  VITE_API_BASE_URL Web API base URL (default: http://localhost:8080)
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Missing required command: $cmd" >&2
    exit 1
  fi
}

config_database_url() {
  sed -nE 's/^database_url:[[:space:]]*"(.*)"/\1/p' "$CONFIG_FILE" | sed -n '1p'
}

check_db_reachable() {
  require_cmd python3
  python3 - "$1" <<'PY'
import socket
import sys
from urllib.parse import urlparse

url = sys.argv[1].strip()
parsed = urlparse(url)
host = parsed.hostname or "localhost"
port = parsed.port or 5432

sock = socket.socket()
sock.settimeout(2)
try:
    sock.connect((host, port))
except OSError as err:
    print(f"Database is not reachable at {host}:{port} ({err})", file=sys.stderr)
    sys.exit(1)
finally:
    sock.close()
PY
}

shutdown() {
  local pids=()
  [[ -n "$collector_pid" ]] && pids+=("$collector_pid")
  [[ -n "$api_pid" ]] && pids+=("$api_pid")
  [[ -n "$web_pid" ]] && pids+=("$web_pid")
  if [[ "${#pids[@]}" -gt 0 ]]; then
    kill "${pids[@]}" >/dev/null 2>&1 || true
  fi
}

start_collector() {
  require_cmd go
  echo "Starting collector..."
  (
    cd "$ROOT_DIR"
    DATABASE_URL="$DATABASE_URL" go run ./cmd/collector -config "$CONFIG_FILE"
  ) &
  collector_pid="$!"
}

start_api() {
  require_cmd go
  echo "Starting api on $API_LISTEN..."
  (
    cd "$ROOT_DIR"
    DATABASE_URL="$DATABASE_URL" go run ./cmd/api -listen "$API_LISTEN"
  ) &
  api_pid="$!"
}

start_web() {
  require_cmd npm
  echo "Starting web (API base: $VITE_API_BASE_URL)..."
  (
    cd "$ROOT_DIR/web"
    if [[ ! -d node_modules ]]; then
      npm install
    fi
    VITE_API_BASE_URL="$VITE_API_BASE_URL" npm run dev
  ) &
  web_pid="$!"
}

case "$MODE" in
  all|backend|collector|api|web)
    ;;
  -h|--help|help)
    usage
    exit 0
    ;;
  *)
    echo "Unknown mode: $MODE" >&2
    usage
    exit 1
    ;;
esac

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "Config file not found: $CONFIG_FILE" >&2
  echo "Tip: cp config.example.yaml config.yaml" >&2
  exit 1
fi

DATABASE_URL="${DATABASE_URL:-$(config_database_url)}"
if [[ "$MODE" != "web" ]]; then
  if [[ -z "${DATABASE_URL}" ]]; then
    echo "DATABASE_URL is required for mode '$MODE'" >&2
    echo "Set DATABASE_URL or add database_url to $CONFIG_FILE" >&2
    exit 1
  fi
  check_db_reachable "$DATABASE_URL"
fi

trap shutdown EXIT INT TERM

case "$MODE" in
  all)
    start_collector
    start_api
    start_web
    ;;
  backend)
    start_collector
    start_api
    ;;
  collector)
    start_collector
    ;;
  api)
    start_api
    ;;
  web)
    start_web
    ;;
esac

wait
