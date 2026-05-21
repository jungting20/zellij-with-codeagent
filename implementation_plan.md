# 역할별 에이전트 폴더 분할 및 실제 데이터 연동 계획

역할별 스크립트(`agent-role`)의 유지보수성과 확장성을 높이기 위해 각 역할을 독립된 Go 패키지로 분리하고, 네트워크 및 콘솔 트래커에 실제 브라우저 데이터 수집 도구(`chromedp`)를 도입하는 단계별 구현 계획입니다.

## Proposed Directory Structure

```text
cmd/agent-role/
├── main.go               # 공통 CLI 진입점 및 역할 라우팅
├── ui/                   # ANSI 색상 및 터미널 헬퍼 공통 모듈
│   └── ui.go
├── coder/                # Coder 역할
│   └── coder.go
├── network/              # Network Tracker 역할 (chromedp 연동)
│   └── tracker.go
└── console/              # Console Tracker 역할 (chromedp 연동)
    └── tracker.go
```

## Proposed Changes

### 1단계: 디렉토리 분할 및 Coder / UI 공통화
- **[NEW] [ui.go](file:///Users/in05908_mac/zellij-with-codeagent/cmd/agent-role/ui/ui.go)**: ANSI 색상 상수, 화면 초기화(`ClearScreen`), 진행 표시줄(`GenerateProgressBar`) 등 화면 표출에 공통으로 사용할 함수 정의
- **[NEW] [coder.go](file:///Users/in05908_mac/zellij-with-codeagent/cmd/agent-role/coder/coder.go)**: `coder` 패키지 생성 및 기존 Coder 전용 애니메이션/로직 이관
- **[MODIFY] [main.go](file:///Users/in05908_mac/zellij-with-codeagent/cmd/agent-role/main.go)**: 외부 패키지(`coder`, `network`, `console`)를 import 하도록 리팩토링 및 껍데기 함수 연결

### 2단계: Network Tracker 실제 데이터 연동 (chromedp)
- **Dependency**: `github.com/chromedp/chromedp` 패키지 추가
- **[NEW] [tracker.go](file:///Users/in05908_mac/zellij-with-codeagent/cmd/agent-role/network/tracker.go)**:
  - `chromedp`를 사용하여 백그라운드에서 크롬 브라우저 인스턴스 구동
  - `network.EventRequestWillBeSent` 및 `network.EventResponseReceived` 이벤트를 구독(Listen)하여 실시간 HTTP 트래픽 수집
  - 수집된 실제 네트워크 호출 내역(Method, Path, Status Code, Size 등)을 터미널 UI에 실시간 스트리밍

### 3단계: Console Tracker 실제 데이터 연동 (chromedp)
- **[NEW] [tracker.go](file:///Users/in05908_mac/zellij-with-codeagent/cmd/agent-role/console/tracker.go)**:
  - `chromedp`를 통해 대상 URL 페이지 로드
  - `runtime.EventConsoleAPICalled` 이벤트를 구독하여 브라우저 콘솔에서 발생하는 실시간 `Console.log`, `Console.error` 등의 로그 수집
  - 콘솔의 로그 레벨(DEBUG, INFO, WARN, ERROR)에 따라 ANSI 색상을 적용하여 터미널 UI에 실시간 스트리밍

---

## Verification Plan

### Automated / Manual Tests
- 각 단계별로 리팩토링 및 실제 기능 연동 후 빌드 및 구동을 테스트합니다:
  ```bash
  # 1단계 리팩토링 검증
  go run ./cmd/agent-role coder
  
  # 2단계 실제 네트워크 트래킹 검증
  go run ./cmd/agent-role network-tracker --url https://www.google.com
  
  # 3단계 실제 브라우저 콘솔 로그 트래킹 검증
  go run ./cmd/agent-role console-tracker --url https://html5test.co
  ```
