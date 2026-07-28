# Ticket Manager Voice Notification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Announce `{prefix}:{ticket number}:완료` through a configurable, cross-platform, serialized voice queue after ticket-manager closes a completed worker.

**Architecture:** Extend repository YAML configuration with enabled and prefix fields. Add a `VoiceNotifier` whose single FIFO worker serializes native speech commands, then inject it into the manager and enqueue once after successful pane closure.

**Tech Stack:** Go standard library (`context`, `encoding/base64`, `encoding/binary`, `os/exec`, `runtime`, `sync`, `unicode/utf16`), YAML v3, Go `testing`

## Global Constraints

- `voice_notifications` defaults to `true` when omitted.
- `voice_notification_prefix` defaults to `ticket-manager` and must be non-empty after trimming.
- Native backends: macOS `say`; Linux `spd-say --wait`, then `espeak`; Windows PowerShell `System.Speech.Synthesis.SpeechSynthesizer` through UTF-16LE Base64 `-EncodedCommand`.
- Commands use direct arguments, never a shell.
- One unbounded FIFO queue prevents overlapping audio and avoids blocking ticket completion.
- Voice errors never alter ticket or pane outcomes and are not retried.
- Shutdown cancels active playback, normalizes process termination to the context cancellation error, and discards queued messages.
- Configuration version remains `1`; this background feature requires no new role.
- Commit messages are written in Korean.

---

### Task 1: Add voice configuration with backward-compatible defaults

**Files:**
- Modify: `internal/ticketworker/config.go`
- Modify: `internal/ticketworker/config_test.go`
- Modify: `internal/ticketworker/manager_test.go` (existing direct `Config` fixtures only)

**Interfaces:**
- Produces: `Config.VoiceNotifications bool`
- Produces: `Config.VoiceNotificationPrefix string`
- Produces YAML keys `voice_notifications`, `voice_notification_prefix`

- [ ] **Step 1: Write failing configuration tests**

Add tests that cover omitted keys, explicit disable/custom prefix, whitespace-only prefix rejection, and the generated template. The desired API is:

```go
cfg, err := LoadConfig(root)
if err != nil {
	t.Fatal(err)
}
if !cfg.VoiceNotifications || cfg.VoiceNotificationPrefix != "ticket-manager" {
	t.Fatalf("voice config = enabled:%v prefix:%q", cfg.VoiceNotifications, cfg.VoiceNotificationPrefix)
}
```

For explicit values, write:

```yaml
version: 1
voice_notifications: false
voice_notification_prefix: " project-a "
```

and assert `false` plus `project-a`. With `voice_notifications: true` and a whitespace-only prefix, assert the error contains `voice_notification_prefix must not be empty`.

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/ticketworker -run 'Test(LoadConfig.*Voice|EnsureConfig)' -count=1
```

Expected: FAIL because the fields and template keys do not exist.

- [ ] **Step 3: Implement defaults, parsing, and validation**

Add:

```go
const defaultVoiceNotificationPrefix = "ticket-manager"

type Config struct {
	Version                 int
	MaxWorkers              int
	PollInterval            time.Duration
	VoiceNotifications      bool
	VoiceNotificationPrefix string
}

type diskConfig struct {
	Version                 int     `yaml:"version"`
	MaxWorkers              *int    `yaml:"max_workers"`
	PollInterval            string  `yaml:"poll_interval"`
	VoiceNotifications      *bool   `yaml:"voice_notifications"`
	VoiceNotificationPrefix *string `yaml:"voice_notification_prefix"`
}
```

Initialize loaded configuration with enabled `true` and prefix `ticket-manager`; override each value only when its YAML pointer is non-nil, trim the prefix, and reject an explicitly empty or whitespace-only result. Extend `configTemplate` with:

```yaml
voice_notifications: true
voice_notification_prefix: ticket-manager
```

Add `VoiceNotificationPrefix: defaultVoiceNotificationPrefix` to the three existing direct `Config` literals in `manager_test.go`. Leave `VoiceNotifications` false in those legacy fixtures so Task 1 does not require a notifier before manager integration exists.

- [ ] **Step 4: Format, verify GREEN, and commit**

```bash
gofmt -w internal/ticketworker/config.go internal/ticketworker/config_test.go internal/ticketworker/manager_test.go
go test ./internal/ticketworker -run 'Test(LoadConfig|EnsureConfig)' -count=1
go test ./internal/ticketworker -count=1
git add internal/ticketworker/config.go internal/ticketworker/config_test.go internal/ticketworker/manager_test.go
git commit -m "feat: 음성 알림 설정 추가"
```

Expected: tests PASS and the config commit succeeds.

### Task 2: Implement native backend resolution and the serial queue

**Files:**
- Create: `internal/ticketworker/voice.go`
- Create: `internal/ticketworker/voice_test.go`

**Interfaces:**
- Produces: `type VoiceNotifier interface { Notify(string) error; Close() error }`
- Produces: `func NewNativeVoiceNotifier(io.Writer) VoiceNotifier`
- Test seams: `resolveSpeechBackend(string, func(string) (string, error)) (speechBackend, error)` and `newSerialVoiceNotifier(func(context.Context, string) error, io.Writer) VoiceNotifier`

- [ ] **Step 1: Write failing backend-resolution tests**

Use table tests and a fake `LookPath` map. Assert:

```go
backend, err := resolveSpeechBackend("linux", mapLookPath(map[string]string{
	"spd-say": "/usr/bin/spd-say",
	"espeak":  "/usr/bin/espeak",
}))
if err != nil {
	t.Fatal(err)
}
if backend.path != "/usr/bin/spd-say" || !reflect.DeepEqual(backend.args("hello"), []string{"--wait", "hello"}) {
	t.Fatalf("backend = path:%q args:%q", backend.path, backend.args("hello"))
}
```

Cover macOS `say`, Linux `spd-say --wait` preference and `espeak` fallback, Windows `powershell.exe` then `pwsh.exe`, unsupported OS, and missing executables. For Windows, pass `-NoProfile`, `-NonInteractive`, and `-EncodedCommand` followed by one UTF-16LE Base64 command. Decode the command in tests and prove that arbitrary Unicode, quotes, newlines, `$`, and expression-like content round-trip exactly through a Base64 message literal inside the fixed script. The raw message must not appear as a process argument or executable PowerShell expression.

- [ ] **Step 2: Verify backend tests RED**

```bash
go test ./internal/ticketworker -run TestResolveSpeechBackend -count=1
```

Expected: FAIL because the resolver does not exist.

- [ ] **Step 3: Implement direct-argument backend resolution**

Define:

```go
type speechBackend struct {
	path string
	args func(string) []string
}

func (b speechBackend) speak(ctx context.Context, message string) error {
	err := exec.CommandContext(ctx, b.path, b.args(message)...).Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
```

Resolve OS candidates with the injected lookup. Use `--wait` before the message for `spd-say`. For Windows, Base64-encode the message inside a fixed `System.Speech` script, encode the complete script as UTF-16LE Base64, and pass it after `-NoProfile`, `-NonInteractive`, and `-EncodedCommand`. Continue using `exec.CommandContext` directly with no shell. Return a descriptive error listing the OS and candidates when resolution fails. If command execution returns after cancellation, return `ctx.Err()` instead of the platform-specific process exit error.

- [ ] **Step 4: Write failing serial-queue tests**

Use a controllable speaker:

```go
started := make(chan string, 3)
release := make(chan struct{})
notifier := newSerialVoiceNotifier(func(ctx context.Context, message string) error {
	started <- message
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}, io.Discard)
t.Cleanup(func() { _ = notifier.Close() })
```

Verify FIFO order using sequential enqueue calls. In a separate concurrency test, verify only one active speaker and every concurrently accepted message runs once without requiring a deterministic order between competing callers. Also verify continuation after one speaker error, genuine-error logging, idempotent `Close`, cancellation of the active speaker, discard of pending messages, and rejection of `Notify` after close with `voice notifier is closed`.

Use a real cancellable helper subprocess to verify `speechBackend.speak` normalizes `exec.CommandContext` termination to `context.Canceled` and the serial worker does not log expected shutdown cancellation. Add a targeted concurrent `Notify`-versus-`Close` test: each `Notify` either succeeds before shutdown (and may be discarded) or returns `voice notifier is closed`; the race must not panic, hang, or report a data race.

- [ ] **Step 5: Verify queue tests RED**

```bash
go test ./internal/ticketworker -run TestSerialVoiceNotifier -count=1
```

Expected: FAIL because the serial notifier does not exist.

- [ ] **Step 6: Implement the notifier and native constructor**

Define:

```go
type VoiceNotifier interface {
	Notify(string) error
	Close() error
}

func NewNativeVoiceNotifier(log io.Writer) VoiceNotifier {
	backend, err := resolveSpeechBackend(runtime.GOOS, exec.LookPath)
	if err != nil {
		return unavailableVoiceNotifier{err: err}
	}
	return newSerialVoiceNotifier(backend.speak, log)
}
```

Implement `serialVoiceNotifier` with `sync.Mutex`, `sync.Cond`, an unbounded `[]string`, one context/cancel pair, and a `done` channel. `Notify` appends and signals. The sole worker removes one message, unlocks, waits for `speak`, logs non-cancellation errors, then takes the next. `Close` marks closed, clears the queue, cancels speech, broadcasts, and waits for `done`. `unavailableVoiceNotifier.Notify` returns the resolution error while `Close` returns nil.

- [ ] **Step 7: Format, verify GREEN, and commit**

```bash
gofmt -w internal/ticketworker/voice.go internal/ticketworker/voice_test.go
go test ./internal/ticketworker -run 'Test(ResolveSpeechBackend|SpeechBackend|SerialVoiceNotifier|NativeVoiceNotifier)' -count=1
go test -race ./internal/ticketworker -run 'Test(SerialVoiceNotifier|SpeechBackend|ResolveSpeechBackend)' -count=10
git add internal/ticketworker/voice.go internal/ticketworker/voice_test.go \
  docs/superpowers/specs/2026-07-28-ticket-manager-voice-notification-design.md \
  docs/superpowers/plans/2026-07-28-ticket-manager-voice-notification.md
git commit -m "feat: 크로스플랫폼 음성 알림 큐 추가"
```

Expected: tests PASS without hangs or leaked processes.

### Task 3: Integrate notification after successful pane closure

**Files:**
- Modify: `internal/ticketworker/manager.go`
- Modify: `internal/ticketworker/manager_test.go`
- Modify: `cmd/agent-role/ticketmanager/ticketmanager.go`
- Modify: `cmd/agent-role/ticketmanager/ticketmanager_test.go`

**Interfaces:**
- Adds: `ManagerOptions.VoiceNotifier VoiceNotifier`
- Adds command dependency: `newVoiceNotifier func(io.Writer) ticketworker.VoiceNotifier`
- Consumes the configuration and notifier interfaces from Tasks 1 and 2.

- [ ] **Step 1: Write failing manager behavior tests**

Add a mutex-protected recording notifier implementing `Notify` and `Close`. Test an enabled manager whose closing slot contains ticket `42`: after `retryClose`, assert the only message is `ticket-manager:42:완료` and the slot is empty. Add cases asserting:

- disabled configuration produces no message;
- a failed close produces no message, while the later successful retry produces exactly one;
- a `Notify` error is logged as `notify ticket=42` but the slot still clears;
- `Run` closes the notifier exactly once on cancellation;
- enabled configuration without a notifier makes `NewManager` return `ticket manager voice notifier is required`.

- [ ] **Step 2: Verify manager tests RED**

```bash
go test ./internal/ticketworker -run 'Test(NewManager.*Voice|Manager.*Voice|Manager.*Notify)' -count=1
```

Expected: FAIL because manager has no voice dependency or enqueue point.

- [ ] **Step 3: Implement manager ownership and enqueue semantics**

Add the option and field, require a notifier only when enabled, and install an early `Run` defer that calls `Close` and logs close errors. In `retryClose`, after `closeOrAbsent` succeeds and immediately before clearing the slot, add:

```go
if m.config.VoiceNotifications {
	message := fmt.Sprintf("%s:%d:완료", m.config.VoiceNotificationPrefix, slot.ticket.ID)
	if err := m.voiceNotifier.Notify(message); err != nil {
		m.logTicketf("notify", slot.ticket, "failed: %v", err)
	}
}
m.logTicketf("closed", slot.ticket, "pane=%s", slot.paneID)
*slot = managerSlot{}
```

- [ ] **Step 4: Write failing production-wiring tests**

Extend command dependencies with a notifier factory. In `TestRunWithDependenciesWiresProjectConfigStoreClientAndManager`, inject a recording notifier and assert the factory receives `stdout`, `ManagerOptions.VoiceNotifier` is that instance, and config contains enabled plus prefix `ticket-manager`. Add a disabled-config test asserting the factory is not called and the manager option is nil.

- [ ] **Step 5: Verify wiring tests RED**

```bash
go test ./cmd/agent-role/ticketmanager -run 'TestRunWithDependencies.*Voice|TestRunWithDependenciesWires' -count=1
```

Expected: FAIL because production dependencies do not create or pass a notifier.

- [ ] **Step 6: Implement production wiring and ownership transfer**

Add:

```go
newVoiceNotifier func(io.Writer) ticketworker.VoiceNotifier
```

to command dependencies and default it to `ticketworker.NewNativeVoiceNotifier`. Create it only when notifications are enabled, pass it to `ManagerOptions`, and close it if manager construction fails. After successful construction, `Manager.Run` owns closure.

- [ ] **Step 7: Format, verify GREEN, and commit**

```bash
gofmt -w internal/ticketworker/manager.go internal/ticketworker/manager_test.go cmd/agent-role/ticketmanager/ticketmanager.go cmd/agent-role/ticketmanager/ticketmanager_test.go
go test ./internal/ticketworker ./cmd/agent-role/ticketmanager -count=1
git add internal/ticketworker/manager.go internal/ticketworker/manager_test.go cmd/agent-role/ticketmanager/ticketmanager.go cmd/agent-role/ticketmanager/ticketmanager_test.go
git commit -m "feat: 티켓 완료 음성 알림 연동"
```

Expected: focused package tests PASS.

### Task 4: Verify, build, and register the unified binary

**Files:**
- Verify all modified Go files
- Build: `bin/zellij-agent`
- Register: `~/.config/custom-cli/zellij-agent`

**Interfaces:**
- Verifies the completed feature across all packages and the installed CLI.

- [ ] **Step 1: Run repository-wide formatting and tests**

```bash
gofmt -w internal/ticketworker/config.go internal/ticketworker/config_test.go internal/ticketworker/voice.go internal/ticketworker/voice_test.go internal/ticketworker/manager.go internal/ticketworker/manager_test.go cmd/agent-role/ticketmanager/ticketmanager.go cmd/agent-role/ticketmanager/ticketmanager_test.go
go test ./...
```

Expected: all packages PASS.

- [ ] **Step 2: Build and atomically register the binary**

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli/.zellij-agent.new
chmod 755 ~/.config/custom-cli/.zellij-agent.new
mv -f ~/.config/custom-cli/.zellij-agent.new ~/.config/custom-cli/zellij-agent
```

Expected: every command exits `0`; the installed binary is not overwritten in place.

- [ ] **Step 3: Inspect final repository state**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors and no uncommitted feature changes.
