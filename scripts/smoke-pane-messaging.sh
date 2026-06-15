#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOCKET="${AGENTD_SOCKET:-/tmp/agentd-message-smoke.sock}"
TASK_ID="${AGENTD_SMOKE_TASK:-agentd-message-smoke-$(date +%s)}"
LOG_FILE="${AGENTD_SMOKE_LOG:-/tmp/agentd-message-smoke.log}"
MARKER="agentd-smoke-message-${TASK_ID}"
PLAN_FILE="$(mktemp /tmp/agentd-message-plan.XXXXXX.json)"
DAEMON_PID=""

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

agentd_curl() {
  curl --silent --show-error --fail --unix-socket "$SOCKET" "$@"
}

cleanup() {
  set +e
  if [[ -S "$SOCKET" ]]; then
    agentd_curl \
      -X POST http://agentd/v1/cleanup \
      -H 'Content-Type: application/json' \
      -d "{\"task_id\":\"${TASK_ID}\"}" >/dev/null
  fi
  if [[ -n "$DAEMON_PID" ]]; then
    kill "$DAEMON_PID" >/dev/null 2>&1
    wait "$DAEMON_PID" >/dev/null 2>&1
  fi
  rm -f "$SOCKET" "$PLAN_FILE"
}
trap cleanup EXIT

require_cmd go
require_cmd curl
require_cmd zellij

if [[ -z "${ZELLIJ:-}" ]]; then
  echo "run this script inside a Zellij session so agentd can create panes" >&2
  exit 1
fi

cd "$ROOT_DIR"
rm -f "$SOCKET"

echo "starting agentd on $SOCKET"
go run ./cmd/agentd serve --socket "$SOCKET" >"$LOG_FILE" 2>&1 &
DAEMON_PID="$!"

for _ in {1..100}; do
  if agentd_curl http://agentd/v1/health >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
agentd_curl http://agentd/v1/health >/dev/null

cat >"$PLAN_FILE" <<JSON
{
  "type": "execution_plan",
  "request_id": "req_${TASK_ID}",
  "payload": {
    "session": "${TASK_ID}",
    "layout": "triple-horizontal",
    "tabs": [
      {
        "name": "${TASK_ID}",
        "panes": [
          {
            "id": "message-from",
            "role": "sender",
            "command": ["sh", "-lc", "printf 'message-from-ready\\n'; while IFS= read -r line; do printf 'message-from:%s\\n' \"\$line\"; done"]
          },
          {
            "id": "message-to",
            "role": "receiver",
            "command": ["sh", "-lc", "printf 'message-to-ready\\n'; while IFS= read -r line; do printf 'message-to:%s\\n' \"\$line\"; done"]
          }
        ]
      }
    ]
  }
}
JSON

echo "creating smoke panes in task $TASK_ID"
agentd_curl \
  -X POST http://agentd/v1/requests \
  -H 'Content-Type: application/json' \
  --data-binary @"$PLAN_FILE" >/dev/null

for _ in {1..100}; do
  if agentd_curl \
    -X POST http://agentd/v1/panes/message-to/snapshot \
    -H 'Content-Type: application/json' \
    -d '{"full":true}' | grep -q 'message-to-ready'; then
    break
  fi
  sleep 0.1
done

echo "sending message-from -> message-to through agentd"
agentd_curl \
  -X POST http://agentd/v1/messages \
  -H 'Content-Type: application/json' \
  -d "{\"from\":\"message-from\",\"to\":\"message-to\",\"type\":\"smoke\",\"body\":\"${MARKER}\"}" >/dev/null

for _ in {1..100}; do
  if agentd_curl \
    -X POST http://agentd/v1/panes/message-to/snapshot \
    -H 'Content-Type: application/json' \
    -d '{"full":true}' | grep -q "$MARKER"; then
    echo "ok: receiver pane snapshot contains $MARKER"
    exit 0
  fi
  sleep 0.1
done

echo "message marker was not observed in receiver pane" >&2
echo "agentd log: $LOG_FILE" >&2
exit 1
