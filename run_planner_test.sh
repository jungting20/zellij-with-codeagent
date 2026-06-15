#!/usr/bin/env bash
set -euo pipefail

SOCKET_PATH="${AGENTD_SOCKET:-/tmp/agentd.sock}"
PLAN_FILE="${AGENTD_PLAN_FILE:-examples/plans/agent-role-demo.json}"
REQUEST_ID="${AGENTD_REQUEST_ID:-req_planner_manual_$(date +%s)}"

echo "Submitting execution plan ${PLAN_FILE} to agentd via socket: ${SOCKET_PATH}"

go run ./cmd/agentctl plan \
  --socket "${SOCKET_PATH}" \
  --file "${PLAN_FILE}" \
  --request-id "${REQUEST_ID}"

echo
echo "Execution plan request sent."
