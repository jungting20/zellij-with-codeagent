# Event Bus Concurrency Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the event bus send-versus-close data race while preserving non-blocking delivery, channel closure, and bounded event history.

**Architecture:** Use the existing `eventbus.Bus` mutex as the single subscriber-lifetime boundary. `Publish`, unregister, and `Close` will all hold that mutex while accessing or closing subscriber channels; publication remains non-blocking through the existing `select` default branch.

**Tech Stack:** Go 1.22+, standard library `sync` and `testing`, Go race detector, Bash verification script.

## Global Constraints

- All Zellij mutations continue to flow through `RuntimeService`; this change does not add direct Zellij calls.
- Slow subscribers continue to drop overflow events rather than block publishers.
- Subscription cancellation, explicit unregister, and bus close continue to close subscription channels.
- Recent event history ordering and its existing bound remain unchanged.
- Do not add durable history, delivery retry, cursors, or subscription restart behavior.
- Use TDD: verify the new regression test fails under `-race` before modifying `bus.go`.

---

## File Structure

- `internal/eventbus/bus_test.go`: owns the concurrent publish/unregister regression test.
- `internal/eventbus/bus.go`: owns the minimal subscriber-lifetime synchronization fix.
- `scripts/test-race-core.sh`: provides a repeatable race-enabled core verification command.
- `docs/next-steps-todolist.md`: records P0 completion and the dropped-event diagnostics decision.

### Task 1: Reproduce and Fix Concurrent Publish/Unregister

**Files:**
- Modify: `internal/eventbus/bus_test.go:1-94`
- Modify: `internal/eventbus/bus.go:79-103`

**Interfaces:**
- Consumes: `func NewWithBuffer(buffer int) *Bus`, `func (b *Bus) Subscribe(ctx context.Context) (<-chan Event, func())`, and `func (b *Bus) Publish(e Event)`.
- Produces: unchanged public interfaces with synchronized subscriber delivery and shutdown.

- [ ] **Step 1: Add the concurrent regression test**

Add `sync` to the test imports and append this test to `internal/eventbus/bus_test.go`:

```go
func TestBusConcurrentPublishAndUnregister(t *testing.T) {
	const iterations = 1000

	for i := 0; i < iterations; i++ {
		bus := NewWithBuffer(1)
		ctx, cancel := context.WithCancel(context.Background())
		_, unregister := bus.Subscribe(ctx)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			bus.Publish(Event{Type: TypeRawOutput, Message: "concurrent"})
		}()
		go func() {
			defer wg.Done()
			<-start
			unregister()
		}()

		close(start)
		wg.Wait()
		cancel()
		bus.Close()
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED under the race detector**

Run:

```bash
go test -race -count=1 ./internal/eventbus -run '^TestBusConcurrentPublishAndUnregister$'
```

Expected: `FAIL` with a race between `eventbus.(*Bus).Publish` sending in `bus.go` and the unregister closure closing a channel in `bus.go`. A `send on closed channel` panic also demonstrates the same defect.

- [ ] **Step 3: Keep delivery inside the bus lifetime lock**

Replace `Bus.Publish` in `internal/eventbus/bus.go` with:

```go
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.history = append(b.history, e)
	if overflow := len(b.history) - b.historyN; overflow > 0 {
		copy(b.history, b.history[overflow:])
		b.history = b.history[:b.historyN]
	}

	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			// drop — subscriber is slower than publisher
		}
	}
}
```

- [ ] **Step 4: Format the changed Go files**

Run:

```bash
gofmt -w internal/eventbus/bus.go internal/eventbus/bus_test.go
```

Expected: both files are formatted without changing behavior beyond the planned test and lock scope.

- [ ] **Step 5: Run focused tests and verify GREEN**

Run:

```bash
go test -race -count=1 ./internal/eventbus -run '^TestBusConcurrentPublishAndUnregister$'
go test -race -count=1 ./internal/eventbus
```

Expected: both commands report `ok` with no race warning or panic.

- [ ] **Step 6: Run the core race suite**

Run:

```bash
go test -race -count=1 ./internal/eventbus ./internal/runtime ./internal/transport
```

Expected: all three packages report `ok` with no race warning.

- [ ] **Step 7: Commit the regression test and fix**

```bash
git add internal/eventbus/bus.go internal/eventbus/bus_test.go
git commit -m "fix: synchronize event bus delivery and shutdown"
```

Expected: the commit contains only the event bus production and test files.

### Task 2: Add Repeatable Verification and Close P0

**Files:**
- Create: `scripts/test-race-core.sh`
- Modify: `docs/next-steps-todolist.md:5-43`

**Interfaces:**
- Consumes: Go package paths `./internal/eventbus`, `./internal/runtime`, and `./internal/transport`.
- Produces: executable `scripts/test-race-core.sh` with no arguments and exit status inherited from `go test`.

- [ ] **Step 1: Add the core race verification script**

Create `scripts/test-race-core.sh` with:

```bash
#!/usr/bin/env bash
set -euo pipefail

go test -race -count=1 ./internal/eventbus ./internal/runtime ./internal/transport
```

Make it executable:

```bash
chmod +x scripts/test-race-core.sh
```

- [ ] **Step 2: Run the script**

Run:

```bash
./scripts/test-race-core.sh
```

Expected: eventbus, runtime, and transport all report `ok`.

- [ ] **Step 3: Update the P0 roadmap state**

In `docs/next-steps-todolist.md`:

- Replace the unchecked baseline race statement with a checked statement that `./scripts/test-race-core.sh` passes.
- Mark the regression-test, subscriber-lifetime synchronization, non-blocking policy, and checked-in verification-script tasks complete.
- Mark the dropped-event diagnostics decision complete with the explicit outcome: counters and diagnostic events are deferred because they are outside this concurrency fix.
- Leave the P1, P2, P3, and later-roadmap items unchanged.

- [ ] **Step 4: Verify the full repository**

Run:

```bash
go test ./...
git diff --check
```

Expected: all Go packages report `ok` or `[no test files]`, and `git diff --check` produces no output.

- [ ] **Step 5: Build and register the unified binary**

Run:

```bash
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
```

Expected: both commands exit successfully, and `~/.config/custom-cli` contains the newly built executable.

- [ ] **Step 6: Commit verification and roadmap updates**

```bash
git add scripts/test-race-core.sh docs/next-steps-todolist.md
git commit -m "test: add core race verification"
```

Expected: the commit contains the executable verification script and the P0 roadmap update, including the previously requested current-roadmap rewrite.

## Final Verification

- [ ] Run `./scripts/test-race-core.sh` and confirm all three core packages pass.
- [ ] Run `go test ./...` and confirm the full suite passes.
- [ ] Run `go build -o bin/zellij-agent ./cmd/zellij-agent` and immediately run `cp bin/zellij-agent ~/.config/custom-cli`.
- [ ] Run `git status --short` and confirm no unexpected files were modified.
- [ ] Review `git log -3 --oneline` and confirm the design, fix, and verification commits are present.
