#!/bin/bash

# Default socket path
SOCKET_PATH="/tmp/agentd.sock"

echo "Requesting registered panes from agentd via socket: ${SOCKET_PATH}..."

curl --unix-socket "${SOCKET_PATH}" -X GET http://localhost/v1/panes

echo -e "\n"
