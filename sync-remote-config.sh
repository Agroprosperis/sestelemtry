#!/usr/bin/env bash
set -euo pipefail

REMOTE_HOST="itspec@10.201.50.10"
REMOTE_APP_DIR="~/sestelemtry"
LOCAL_CONFIG_FILE="./config.docker.yaml"

if [[ ! -f "$LOCAL_CONFIG_FILE" ]]; then
  echo "Missing $LOCAL_CONFIG_FILE"
  exit 1
fi

scp "$LOCAL_CONFIG_FILE" "$REMOTE_HOST:$REMOTE_APP_DIR/config.docker.yaml"
ssh "$REMOTE_HOST" "cd $REMOTE_APP_DIR/deploy && docker compose up -d --force-recreate collector && docker compose logs --tail=80 collector"
