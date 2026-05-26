#!/bin/bash

# 에러 발생 시 즉시 종료
set -e

SOCKET_PATH="/tmp/agentd-test.sock"

# 기존 소켓 파일 및 데몬 프로세스 정리
cleanup() {
  echo -e "\n=== [Cleanup] Cleaning up background daemon and sockets ==="
  if [ -f "$SOCKET_PATH" ]; then
    rm -f "$SOCKET_PATH"
  fi
  if [ -n "$DAEMON_PID" ]; then
    kill "$DAEMON_PID" 2>/dev/null || true
  fi
}

trap cleanup EXIT

echo "=== [1/5] Starting agentd daemon in background ==="
go run ./cmd/agentd serve --socket "$SOCKET_PATH" > /dev/null 2>&1 &
DAEMON_PID=$!

# 데몬 시작 및 소켓 파일 생성 대기 (최대 10초)
echo "Waiting for daemon socket to be ready..."
TIMEOUT=20
while [ ! -S "$SOCKET_PATH" ]; do
  if [ "$TIMEOUT" -le 0 ]; then
    echo "Error: Daemon socket file was not created within timeout limit."
    exit 1
  fi
  sleep 0.5
  TIMEOUT=$((TIMEOUT-1))
done
echo "Daemon socket is ready."

echo "=== [2/5] Creating a test pane ==="
# /v1/panes 에 POST 요청을 보내 기본 세션("test-session")에 pane 등록
# new_tab=true 로 보내 탭도 함께 생성
CREATE_RESP=$(curl -s --unix-socket "$SOCKET_PATH" -X POST \
  -H "Content-Type: application/json" \
  -d '{
    "id": "pane-test-1",
    "task_id": "task-test",
    "agent_id": "agent-test",
    "role": "test-role",
    "new_tab": true,
    "tab_name": "Tab-Test"
  }' http://localhost/v1/panes)

echo "Create Pane Response:"
echo "$CREATE_RESP" | jq . || echo "$CREATE_RESP"

echo -e "\n=== [3/5] Testing GET /v1/sessions ==="
SESS_RESP=$(curl -s --unix-socket "$SOCKET_PATH" http://localhost/v1/sessions)
echo "$SESS_RESP" | jq . || echo "$SESS_RESP"

# 세션 ID 파싱 (첫 번째 세션 ID)
SESSION_ID=$(echo "$SESS_RESP" | jq -r '.sessions[0].id // empty')

if [ -z "$SESSION_ID" ]; then
  echo "Error: No session found in response."
  exit 1
fi

echo -e "\n=== [4/5] Testing GET /v1/sessions/$SESSION_ID ==="
curl -s --unix-socket "$SOCKET_PATH" "http://localhost/v1/sessions/$SESSION_ID" | jq .

echo -e "\n=== [5/5] Testing GET /v1/sessions/$SESSION_ID/tabs ==="
TABS_RESP=$(curl -s --unix-socket "$SOCKET_PATH" "http://localhost/v1/sessions/$SESSION_ID/tabs")
echo "$TABS_RESP" | jq .

# 탭 ID 파싱 (첫 번째 탭 ID)
TAB_ID=$(echo "$TABS_RESP" | jq -r '.tabs[0].id // empty')

if [ -n "$TAB_ID" ]; then
  echo -e "\n=== [Extra] Testing GET /v1/sessions/$SESSION_ID/tabs/$TAB_ID ==="
  curl -s --unix-socket "$SOCKET_PATH" "http://localhost/v1/sessions/$SESSION_ID/tabs/$TAB_ID" | jq .

  echo -e "\n=== [Extra] Testing GET /v1/sessions/$SESSION_ID/tabs/$TAB_ID/panes ==="
  curl -s --unix-socket "$SOCKET_PATH" "http://localhost/v1/sessions/$SESSION_ID/tabs/$TAB_ID/panes" | jq .
fi

echo -e "\n=== Test Finished Successfully ==="
