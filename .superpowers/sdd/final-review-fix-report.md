# Final Whole-Branch Review Fixes

## Finding 1: process-tree cancellation

- Root cause: `exec.CommandContext` used its default cancellation, which kills only the direct role wrapper process. Provider grandchildren inherited no independently cancellable boundary and could survive the wrapper timeout.
- Fix: role commands now start in a dedicated process group on Unix and replace `Cmd.Cancel` with a process-group `SIGKILL`. The build-tagged non-Unix helper leaves `CommandContext`'s immediate-process cancellation intact.
- Test added: `TestProcessRoleRunnerCancellationTerminatesGrandchild` starts a helper wrapper that starts a helper grandchild, lets the role timeout fire, and verifies the grandchild PID no longer exists. No provider is invoked.

## Finding 2: overall timeout did not cover post-debate Codex

- Root cause: the debate pipeline created the overall command context correctly, but passed a new `context.Background()` to `CodexStarter.Start` after persistence.
- Fix: the existing overall context is now passed to `CodexStarter.Start`.
- Test added: `TestRunOverallTimeoutCancelsCodexStarter` uses a blocking fake starter, observes `context.DeadlineExceeded`, and asserts exit code 1 and the emitted deadline diagnostic.

## Finding 3: stderr diagnostics missing after successful process exit

- Root cause: bounded stderr decoration lived only inside the nonzero-exit branch. Decode, trailing JSON, contract, status, and empty-content failures discarded the already-captured stderr.
- Fix: `withStderrDiagnostic` is the single bounded diagnostic decorator for timeout, execution, malformed/trailing JSON, schema/role/engine/status contract mismatch, and empty content. Existing `RunError.Kind` and `ExitCode` values are unchanged.
- Tests added: `TestProcessRoleRunnerIncludesBoundedStderrForOutputErrors` covers malformed JSON and role contract mismatch, verifies the retained tail marker, discarded head marker, bounded message size, stable kind, and nil exit code.

## TDD evidence

### RED

Command:

```text
go test ./internal/backgrounddebate -run 'TestProcessRoleRunner(IncludesBoundedStderrForOutputErrors|CancellationTerminatesGrandchild)$' -count=1 -v
go test ./internal/cli/debatebackground -run '^TestRunOverallTimeoutCancelsCodexStarter$' -count=1 -v
```

Result: failed as expected. The malformed and contract errors lacked `stderr-tail-marker`; grandchild PID 69777 remained alive after the role timeout; the blocking starter's observed context error was nil.

### GREEN

Command:

```text
go test ./internal/backgrounddebate -run 'TestProcessRoleRunner(IncludesBoundedStderrForOutputErrors|CancellationTerminatesGrandchild)$' -count=1 -v
go test ./internal/cli/debatebackground -run '^TestRunOverallTimeoutCancelsCodexStarter$' -count=1 -v
```

Result: PASS. Background debate regressions passed in 0.647s; Codex context regression passed in 0.734s.

## Final verification

Commands and results:

```text
go test ./internal/backgrounddebate ./internal/cli/debatebackground -count=1
```

PASS: `internal/backgrounddebate` in 0.729s and `internal/cli/debatebackground` in 0.439s.

```text
GOOS=linux GOARCH=amd64 go test -c -o /tmp/backgrounddebate-linux.test ./internal/backgrounddebate
GOOS=linux GOARCH=amd64 go test -c -o /tmp/debatebackground-linux.test ./internal/cli/debatebackground
GOOS=windows GOARCH=amd64 go test -c -o /tmp/backgrounddebate-windows.test.exe ./internal/backgrounddebate
GOOS=windows GOARCH=amd64 go test -c -o /tmp/debatebackground-windows.test.exe ./internal/cli/debatebackground
CGO_ENABLED=0 GOOS=android GOARCH=amd64 go build ./internal/backgrounddebate
GOOS=ios GOARCH=arm64 go build ./internal/backgrounddebate
GOOS=illumos GOARCH=amd64 go build ./internal/backgrounddebate
GOOS=js GOARCH=wasm go build ./internal/backgrounddebate
```

PASS: all compile-only/build checks exited 0. An earlier attempted cross-platform `go test -run '^$'` compiled successfully but then produced the expected `exec format error` by trying to execute Linux test binaries on macOS; it was replaced with the compile-only commands above.

```text
go test ./...
```

PASS: all repository packages passed; packages without tests reported `[no test files]`.

```text
go build -o bin/zellij-agent ./cmd/zellij-agent
cp bin/zellij-agent ~/.config/custom-cli
git diff --check
```

PASS: unified binary built and was registered on the custom-cli PATH; diff whitespace check exited 0.

## Files

- `internal/backgrounddebate/process_runner.go`
- `internal/backgrounddebate/process_tree_unix.go`
- `internal/backgrounddebate/process_tree_fallback.go`
- `internal/backgrounddebate/process_runner_test.go`
- `internal/backgrounddebate/process_runner_unix_test.go`
- `internal/cli/debatebackground/debatebackground.go`
- `internal/cli/debatebackground/debatebackground_test.go`
- `.superpowers/sdd/final-review-fix-report.md`

## Self-review

- Verified every `ProcessRoleRunner.Run` failure return after process start uses the common bounded decorator while retaining the prior failure kind and exit-code semantics.
- Verified the process group is created only for the fresh role command and cancellation targets the negative group PID, covering descendants without affecting the caller's process group.
- Expanded Unix build constraints to all supported Unix-like Go targets (`aix`, `android`, `darwin`, `dragonfly`, `freebsd`, `illumos`, `ios`, `linux`, `netbsd`, `openbsd`, `solaris`) and compile-checked representative Unix and fallback targets.
- Verified JSON rendering/stdout code was untouched, fixed role CLI arguments and pipeline order were untouched, and tests use helper processes/fakes only.
- `git diff --check` is clean.

## Concerns

- None. Unix cancellation intentionally uses `SIGKILL` to match `exec.CommandContext`'s immediate cancellation semantics while extending it to the whole role process group.
