#!/bin/bash
set -euo pipefail

HOST="${DEPLOY_HOST:-root@192.9.238.12}"
PASS="${DEPLOY_PASS:-Aa1415926}"
REMOTE_DIR="${DEPLOY_DIR:-/opt/api-in-one}"
SSH_OPTS="-o StrictHostKeyChecking=no"

echo "=== 1. Build frontend ==="
cd "$(dirname "$0")/web"
npm run build

echo "=== 2. Build binary ==="
cd ..
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o api-in-one .

echo "=== 3. Stop service ==="
sshpass -p "$PASS" ssh $SSH_OPTS "$HOST" "systemctl stop api-in-one"

echo "=== 4. Copy binary ==="
sshpass -p "$PASS" scp $SSH_OPTS api-in-one "$HOST:$REMOTE_DIR/api-in-one"

echo "=== 5. Copy assets ==="
sshpass -p "$PASS" ssh $SSH_OPTS "$HOST" "rm -rf $REMOTE_DIR/web/assets && mkdir -p $REMOTE_DIR/web/assets"
sshpass -p "$PASS" scp $SSH_OPTS web/dist/assets/* "$HOST:$REMOTE_DIR/web/assets/"

echo "=== 6. Start service ==="
sshpass -p "$PASS" ssh $SSH_OPTS "$HOST" "systemctl start api-in-one"

echo "=== 7. Verify ==="
sleep 2
sshpass -p "$PASS" ssh $SSH_OPTS "$HOST" "
  systemctl is-active api-in-one &&
  echo -n '  / → ' && curl -s -o /dev/null -w '%{http_code}' http://localhost:3000/ &&
  echo -n '  /v1/models → ' && curl -s -o /dev/null -w '%{http_code}' http://localhost:3000/v1/models
"

echo "=== Done ==="
