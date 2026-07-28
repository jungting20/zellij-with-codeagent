# Daemon-Owned Ticket Voice Notification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route coding-agent completion summaries through a daemon-owned, bounded, idempotent speech queue so all ticket managers share one non-overlapping native voice worker.

**Architecture:** The coding agent emits an optional summary line immediately before the unchanged completion marker. The ticket manager finishes the database and pane lifecycle, then calls a dedicated Unix-socket HTTP endpoint using the database completion timestamp as an idempotency generation. The daemon adapts that request into a standalone voice service that owns formatting, bounded FIFO ordering, deduplication, native execution, and shutdown.

**Tech Stack:** Go standard library, Unix-socket HTTP, SQLite timestamps, native `say`/`spd-say`/`espeak`/PowerShell speech executables.

## Global Constraints

- Keep config version 1 and the current voice toggle/prefix defaults.
- Keep `ZELLIJ_AGENT_TICKET_DONE <positive ID>` backward-compatible.
- Accept at most an 8 KiB body, 256-byte request ID, 128-code-point prefix, and 4 KiB one-line summary.
- Strip control characters, collapse whitespace, and speak at most 120 summary code points.
- Cap pending items at 256 and remembered accepted IDs at 1,024 per daemon lifetime.
- Use one-second attempts, at most three attempts, and 100 ms then 200 ms backoff.
- Notification failure never changes `done`, reopens a pane, or retains a completed slot.
- Daemon shutdown cancels speech and discards the queue; add no persistence.
- Invoke native executables directly without a shell on macOS, Linux, and Windows.
- Commit messages are Korean.
- Never stage the existing `.zellij-agent/worker/config.yaml` modification.
- Preserve the existing space-format edits in `manager.go` and `manager_test.go`; Task 5 absorbs them.

---

### Task 1: Standalone daemon voice service

**Files:**
- Create: `internal/voice/backend.go`
- Create: `internal/voice/backend_test.go`
- Create: `internal/voice/service.go`
- Create: `internal/voice/service_test.go`
- Reference: `internal/ticketworker/voice.go`
- Reference: `internal/ticketworker/voice_test.go`

**Interfaces:**
- Consumes: the existing safe native backend implementation.
- Produces: the daemon-owned queue API used by Task 3.

- [ ] **Step 1: Write failing service tests for the exact API**

```go
type Notification struct {
	RequestID string
	Prefix    string
	TicketID  int64
	Summary   string
}
type EnqueueStatus string
const (
	EnqueueStatusQueued EnqueueStatus = "queued"
	EnqueueStatusDuplicate EnqueueStatus = "duplicate"
)
type Options struct {
	Capacity int
	RecentLimit int
	Speak func(context.Context, string) error
	Log io.Writer
}
func NewService(Options) (*Service, error)
func NewNativeService(io.Writer) *Service
func (*Service) Enqueue(Notification) (EnqueueStatus, error)
func (*Service) Close() error
```

Cover formatting with and without summary, control/whitespace normalization, 120-rune truncation, FIFO, no overlap, queued/in-flight/recent deduplication, 256-capacity rejection, rejected-ID retry, 1,024-ID eviction, command failure continuation, close cancellation/discard, and concurrent enqueue/close.

- [ ] **Step 2: Verify the tests fail before implementation**

Run: `go test ./internal/voice -run 'TestService' -count=1`

Expected: FAIL because `internal/voice` does not exist.

- [ ] **Step 3: Implement queue state and formatting**

```go
const (
	DefaultCapacity = 256
	DefaultRecentLimit = 1024
	MaxSpokenSummaryRunes = 120
)
var ErrQueueFull = errors.New("voice notification queue is full")
var ErrClosed = errors.New("voice notification service is closed")

func formatMessage(n Notification) string {
	base := fmt.Sprintf("%s %d 완료", strings.TrimSpace(n.Prefix), n.TicketID)
	summary := normalizeSummary(n.Summary)
	if summary == "" { return base }
	return base + ". " + summary
}
```

Under one mutex, check closed, duplicate, then capacity; add `RequestID` to `seen` only after acceptance. One goroutine speaks FIFO items serially. `Close` is idempotent, clears pending items, cancels the active process context, and waits for worker exit.

- [ ] **Step 4: Port backend code and tests, then verify**

Move the native backend logic conceptually into `backend.go` while leaving the old file temporarily for Task 5 compatibility. Port tests for macOS `say`, Linux preference/fallback, PowerShell UTF-16LE encoding, missing executable, real command failure, and cancellation normalization.

Run: `go test ./internal/voice -count=1`

Expected: PASS without invoking host audio.

- [ ] **Step 5: Commit**

```bash
git add internal/voice
git commit -m "feat: daemon용 음성 알림 서비스 추가"
```

---

### Task 2: Unix-socket HTTP contract

**Files:**
- Modify: `internal/transport/types.go`
- Modify: `internal/transport/client.go`
- Modify: `internal/transport/server.go`
- Modify: `internal/transport/errors.go`
- Create: `internal/transport/handlers_voice.go`
- Create: `internal/transport/voice_notifications_test.go`

**Interfaces:**
- Consumes: no `internal/voice` type; transport stays implementation-independent.
- Produces: DTOs, service seam, route, errors, and client call used by Tasks 3 and 5.

- [ ] **Step 1: Write failing route and client tests**

```go
type VoiceNotificationRequest struct {
	RequestID string `json:"request_id"`
	Prefix string `json:"prefix"`
	TicketID int64 `json:"ticket_id"`
	Summary string `json:"summary,omitempty"`
}
type VoiceNotificationResponse struct { Status string `json:"status"` }
type VoiceNotificationService interface {
	QueueVoiceNotification(context.Context, VoiceNotificationRequest) (VoiceNotificationResponse, error)
}
```

Test `202 queued`, `200 duplicate`, `400 bad_request`, retryable `503 queue_full`, all exact size/line limits, wrong method, trailing JSON, and a real Unix-socket client round trip.

- [ ] **Step 2: Verify focused tests fail**

Run: `go test ./internal/transport -run 'Test(VoiceNotification|ClientQueuesVoice)' -count=1`

Expected: FAIL because the contract is absent.

- [ ] **Step 3: Implement the route and client**

Add `CodeQueueFull ErrorCode = "queue_full"`, `ErrVoiceQueueFull`, `ServerOptions.VoiceNotifications`, and a matching `Server` field. Require the service in `NewServer` and update existing server-test constructors with a no-op fake. Add only `POST /v1/voice-notifications`.

Use `http.MaxBytesReader(w, r.Body, 8<<10)`, reject trailing JSON, and validate:

```go
req.RequestID != "" && len(req.RequestID) <= 256
strings.TrimSpace(req.Prefix) != ""
utf8.RuneCountInString(strings.TrimSpace(req.Prefix)) <= 128
req.TicketID > 0
len(req.Summary) <= 4<<10
!strings.ContainsAny(req.Summary, "\r\n")
```

Add:

```go
func (c *Client) QueueVoiceNotification(ctx context.Context, req VoiceNotificationRequest) (VoiceNotificationResponse, error)
```

- [ ] **Step 4: Run all transport tests**

Run: `go test ./internal/transport -count=1`

Expected: PASS for new and existing routes.

- [ ] **Step 5: Commit**

```bash
git add internal/transport
git commit -m "feat: 음성 알림 HTTP 요청 추가"
```

---

### Task 3: Daemon lifecycle ownership

**Files:**
- Create: `internal/cli/daemon/voice.go`
- Modify: `internal/cli/daemon/daemon.go`
- Modify: `internal/cli/daemon/daemon_test.go`

**Interfaces:**
- Consumes: Task 1 service and Task 2 transport seam.
- Produces: daemon adapter and lifecycle wiring.

- [ ] **Step 1: Write failing adapter and lifecycle tests**

```go
type daemonVoiceService interface {
	Enqueue(voice.Notification) (voice.EnqueueStatus, error)
	Close() error
}
```

Test field conversion, queued/duplicate conversion, `voice.ErrQueueFull` to `transport.ErrVoiceQueueFull`, canceled context rejection, and exactly one `Close` after `RunContext` server shutdown.

- [ ] **Step 2: Verify daemon tests fail**

Run: `go test ./internal/cli/daemon -run 'Test.*Voice' -count=1`

Expected: FAIL because adapter/factory wiring is absent.

- [ ] **Step 3: Implement adapter and serve wiring**

```go
func (a voiceQueueAdapter) QueueVoiceNotification(
	ctx context.Context,
	req transport.VoiceNotificationRequest,
) (transport.VoiceNotificationResponse, error)
```

Check `ctx.Err`, translate fields/status/errors, and add an overridable factory returning `daemonVoiceService`, defaulting to `voice.NewNativeService(stdout)`. In `serve`, create the service before `transport.NewServer`, pass the adapter, and defer `Close`; `ListenAndServe` stops HTTP before the deferred voice close.

- [ ] **Step 4: Verify daemon plus transport**

Run: `go test ./internal/cli/daemon ./internal/transport -count=1`

Expected: PASS including shutdown/socket cleanup.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/daemon
git commit -m "feat: daemon이 음성 큐 수명주기 관리"
```

---

### Task 4: Completion summary protocol

**Files:**
- Modify: `internal/ticketworker/prompt.go`
- Modify: `internal/ticketworker/prompt_test.go`

**Interfaces:**
- Consumes: current completion marker.
- Produces: summary prompt and parser used by Task 5.

- [ ] **Step 1: Write failing prompt/parser tests**

```go
const completionSummaryPrefix = "ZELLIJ_AGENT_TICKET_SUMMARY"
func parseCompletionOutput(output, marker string) (done bool, summary string)
```

Test the exact final-two-line instruction, nearest preceding summary, marker-only compatibility, empty summary, wrong marker, display bullet prefix, multiple summaries, and summary-after-marker rejection.

- [ ] **Step 2: Verify prompt tests fail**

Run: `go test ./internal/ticketworker -run 'Test(RenderTicketPrompt|ParseCompletionOutput)' -count=1`

Expected: FAIL because summary support is absent.

- [ ] **Step 3: Implement prompt and parser**

Scan lines, normalize existing optional `• ` display prefixes, remember the latest non-empty summary, and return it only at the exact requested marker. Keep a `containsExactLine` compatibility wrapper until Task 5 replaces call sites. Tell the agent in Korean to emit a concise actual-change summary followed by the unchanged marker.

- [ ] **Step 4: Verify backward compatibility**

Run: `go test ./internal/ticketworker -run 'Test(CompletionMarker|RenderTicketPrompt|ParseCompletionOutput)' -count=1`

Expected: PASS for summary and old marker-only output.

- [ ] **Step 5: Commit**

```bash
git add internal/ticketworker/prompt.go internal/ticketworker/prompt_test.go
git commit -m "feat: 티켓 완료 변경 요약 프로토콜 추가"
```

---

### Task 5: Manager migration to daemon requests

**Files:**
- Modify: `internal/ticketworker/manager.go`
- Modify: `internal/ticketworker/manager_test.go`
- Delete: `internal/ticketworker/voice.go`
- Delete: `internal/ticketworker/voice_test.go`
- Modify: `cmd/agent-role/ticketmanager/ticketmanager.go`
- Modify: `cmd/agent-role/ticketmanager/ticketmanager_test.go`

**Interfaces:**
- Consumes: Task 2 client API, Task 4 parser, and `Ticket.CompletedAt`.
- Produces: stable, retried daemon notification requests and no manager-owned speech.

- [ ] **Step 1: Replace notifier tests with failing transport-request tests**

Extend `ManagerClient` and its fake:

```go
QueueVoiceNotification(context.Context, transport.VoiceNotificationRequest) (transport.VoiceNotificationResponse, error)
```

Add `type NotificationBackoff func(context.Context, time.Duration) error` to `ManagerOptions` for no-sleep tests. Cover summary request after done/close, split-event snapshot recovery, no-summary fallback, stored already-done timestamp, same-ID retries and 100/200 ms delays, non-retryable validation failure, exhausted retry slot cleanup, disabled notification, and missing `CompletedAt`.

- [ ] **Step 2: Verify manager and role tests fail**

Run: `go test ./internal/ticketworker ./cmd/agent-role/ticketmanager -run 'Test(Manager|RunWithDependencies)' -count=1`

Expected: FAIL because local notifier ownership remains.

- [ ] **Step 3: Capture summary and completion generation**

Add `summary string` to `managerSlot`. Parse event output; if it has the marker but no summary, take one identity-checked snapshot and parse again. Parse summary during periodic snapshot recovery too. Retain the `Ticket` returned from `ActionDone` or the stored already-done ticket.

```go
func completionVoiceRequestID(taskID string, ticket Ticket) (string, error) {
	if ticket.CompletedAt == nil {
		return "", errors.New("completed ticket is missing completed_at")
	}
	return fmt.Sprintf("%s:%d:%d", taskID, ticket.ID, ticket.CompletedAt.UTC().UnixNano()), nil
}
```

- [ ] **Step 4: Submit after close with bounded retry**

```go
func (m *Manager) finalizeCompletedSlot(ctx context.Context, slot *managerSlot)
func (m *Manager) queueCompletionVoice(ctx context.Context, slot *managerSlot) error
```

Each attempt gets `context.WithTimeout(ctx, time.Second)`. Treat queued/duplicate as success. Retry network ambiguity and `ClientError.APIError.Retryable`; do not retry non-retryable client errors. Use injected 100/200 ms backoff, log final failure, and clear the slot unconditionally.

- [ ] **Step 5: Remove manager-owned voice wiring**

Remove `VoiceNotifier` from manager options/state/lifecycle. Remove `newVoiceNotifier` and cleanup from the ticket-manager role. Delete the old ticket-worker voice files only after all callers compile against transport. Update role tests to verify config/client/manager wiring without a native notifier.

- [ ] **Step 6: Run regression and race tests**

Run: `go test ./internal/ticketworker ./cmd/agent-role/ticketmanager -count=1`

Expected: PASS for completion, close retry, reconnect, snapshot recovery, shutdown, disabled voice, and retry behavior.

Run: `go test -race ./internal/ticketworker ./internal/voice -count=1`

Expected: PASS with no races.

- [ ] **Step 7: Commit**

```bash
git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go internal/ticketworker/voice.go internal/ticketworker/voice_test.go cmd/agent-role/ticketmanager
git commit -m "refactor: 티켓 음성 알림을 daemon 요청으로 전환"
```

Do not stage `.zellij-agent/worker/config.yaml`.

---

### Task 6: Full verification and atomic binary registration

**Files:**
- Verify: all changed Go packages and unified binary
- Preserve: `.zellij-agent/worker/config.yaml`

**Interfaces:**
- Consumes: Tasks 1-5.
- Produces: verified installed CLI/daemon binary.

- [ ] **Step 1: Format and inspect**

```bash
gofmt -w internal/voice/*.go internal/transport/*.go internal/cli/daemon/*.go internal/ticketworker/*.go cmd/agent-role/ticketmanager/*.go
git diff --check
git status --short
```

Expected: no formatting errors and no feature-unrelated changes in the isolated worktree. The pre-existing config modification remains only in the original main checkout.

- [ ] **Step 2: Run complete and race suites**

Run: `go test ./...`

Expected: PASS for all packages.

Run: `go test -race ./internal/voice ./internal/transport ./internal/ticketworker -count=1`

Expected: PASS with no races.

- [ ] **Step 3: Build and atomically install**

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
chmod 755 /Users/in05908_mac/.config/custom-cli/.zellij-agent.new
mv -f /Users/in05908_mac/.config/custom-cli/.zellij-agent.new /Users/in05908_mac/.config/custom-cli/zellij-agent
```

Expected: every command exits zero and the executable is never overwritten in place.

- [ ] **Step 4: Verify repository state**

```bash
git status --short
git log -6 --oneline
```

Expected: the isolated feature worktree is clean and Tasks 1-5 appear as Korean commits. The original main checkout still retains its pre-existing `.zellij-agent/worker/config.yaml` modification.
