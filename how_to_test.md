# agentd 테스트 방법 가이드

1. **바이너리 빌드**:
   ```bash
   go build -o bin/agentd ./cmd/agentd
   go build -o bin/agentctl ./cmd/agentctl
   go build -o bin/agent-role ./cmd/agent-role
   ```
2. **Zellij 세션 실행**: `zellij -s agentd-test-session` 명령어로 Zellij를 기동합니다.
3. **데몬 서버 구동**:
   ```bash
   export ZELLIJ_SESSION_NAME=agentd-test-session
   ./bin/agentd serve --socket /tmp/agentd.sock
   ```
4. **샘플 실행 계획 제출**: 다른 터미널 창을 열고 `./run_planner_test.sh`를 실행하여 `examples/plans/agent-role-demo.json`에 정의된 역할별 Pane을 일괄 구동합니다.
5. **상태 확인**:
   ```bash
   go run ./cmd/agentctl status --socket /tmp/agentd.sock
   go run ./cmd/agentctl events --socket /tmp/agentd.sock --limit 20
   ```
