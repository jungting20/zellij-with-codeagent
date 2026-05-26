#!/bin/bash

# Default socket path and target URL
SOCKET_PATH="/tmp/agentd.sock"
TARGET_URL="https://html5test.co"

# Use current working directory as default CWD
CURRENT_DIR=$(pwd)

echo "Sending execution plan to agentd via socket: ${SOCKET_PATH}..."

curl --unix-socket "${SOCKET_PATH}" -X POST -H "Content-Type: application/json" \
  -d '{
    "type": "execution_plan",
    "request_id": "req_planner_manual_'"$(date +%s)"'",
    "payload": {
      "session": "agent-planner-session",
      "panes": [
        {
          "id": "coder-pane",
          "role": "coder",
          "cwd": "'"${CURRENT_DIR}"'",
          "command": ["./bin/agent-role", "coder"]
        },
        {
          "id": "network-pane",
          "role": "network-tracker",
          "cwd": "'"${CURRENT_DIR}"'",
          "command": ["./bin/agent-role", "network-tracker", "--url", "'"${TARGET_URL}"'"]
        },
        {
          "id": "console-pane",
          "role": "console-tracker",
          "cwd": "'"${CURRENT_DIR}"'",
          "command": ["./bin/agent-role", "console-tracker", "--url", "'"${TARGET_URL}"'"]
        }
      ]
    }
  }' \
  http://localhost/v1/requests

echo -e "\n\nExecution plan request sent."
