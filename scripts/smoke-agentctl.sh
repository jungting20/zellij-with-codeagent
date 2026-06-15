#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOCKET="${AGENTD_SOCKET:-/tmp/agentd-smoke.sock}"
TASK_ID="zellij-with-code-agent"
PLAN_FILE="${AGENTD_PLAN_FILE:-examples/plans/agent-role-demo.json}"
LOG_FILE="${AGENTD_SMOKE_LOG:-/tmp/agentd-smoke.log}"
DAEMON_PID=""

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_file() {
  if [[ ! -e "$1" ]]; then
    echo "missing required file: $1" >&2
    exit 1
  fi
}

cleanup() {
  set +e
  if [[ -S "$SOCKET" && -x "$ROOT_DIR/bin/agentctl" ]]; then
    "$ROOT_DIR/bin/agentctl" cleanup --socket "$SOCKET" --task "$TASK_ID" >/dev/null 2>&1
  fi
  if [[ -n "$DAEMON_PID" ]]; then
    kill "$DAEMON_PID" >/dev/null 2>&1
    wait "$DAEMON_PID" >/dev/null 2>&1
  fi
  rm -f "$SOCKET"
}
trap cleanup EXIT

cd "$ROOT_DIR"

require_cmd zellij
require_cmd nvim
require_file "bin/agentd"
require_file "bin/agentctl"
require_file "bin/agent-role"
require_file "$PLAN_FILE"

if [[ ! -x "bin/agentd" || ! -x "bin/agentctl" || ! -x "bin/agent-role" ]]; then
  echo "local binaries must be executable; run the build commands in docs/manual-smoke-test.md" >&2
  exit 1
fi

if [[ -z "${ZELLIJ:-}" && -z "${ZELLIJ_SESSION_NAME:-}" ]]; then
  echo "run this script inside a Zellij session or set ZELLIJ_SESSION_NAME" >&2
  exit 1
fi

rm -f "$SOCKET"

echo "starting agentd on $SOCKET"
"$ROOT_DIR/bin/agentd" serve --socket "$SOCKET" >"$LOG_FILE" 2>&1 &
DAEMON_PID="$!"

for _ in {1..100}; do
  if "$ROOT_DIR/bin/agentctl" health --socket "$SOCKET" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

"$ROOT_DIR/bin/agentctl" health --socket "$SOCKET" >/dev/null

echo "submitting $PLAN_FILE"
"$ROOT_DIR/bin/agentctl" plan --socket "$SOCKET" --file "$PLAN_FILE" >/dev/null

echo "checking runtime state"
STATUS_OUTPUT="$("$ROOT_DIR/bin/agentctl" status --socket "$SOCKET")"
echo "$STATUS_OUTPUT"

for pane in coder network-tracker console-tracker editor; do
  if ! grep -q -- "- ${pane} " <<<"$STATUS_OUTPUT"; then
    echo "expected pane missing from status: $pane" >&2
    echo "agentd log: $LOG_FILE" >&2
    exit 1
  fi
done

echo "checking recent events"
"$ROOT_DIR/bin/agentctl" events --socket "$SOCKET" --limit 20 >/dev/null

echo "cleaning up task $TASK_ID"
"$ROOT_DIR/bin/agentctl" cleanup --socket "$SOCKET" --task "$TASK_ID" >/dev/null

echo "ok: agentctl smoke flow completed"
