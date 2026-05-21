# agentd 테스트 방법 가이드

1. **Zellij 세션 실행**: `zellij -s agentd-test-session` 명령어로 zellij를 기동합니다.
2. **데몬 서버 구동**: `export ZELLIJ_SESSION_NAME=agentd-test-session && ./bin/agentd serve --socket /tmp/agentd.sock` 으로 서버를 실행합니다.
3. **테스트 스크립트 실행**: 다른 터미널 창을 열고 `./run_planner_test.sh`를 실행하여 3개 역할의 에이전트 Pane을 일괄 구동합니다.
