# Final Fix Report

## Final Fix Wave

### Files

- `internal/codingagent/service_test.go`
- `internal/transport/server_test.go`
- `docs/superpowers/specs/2026-08-03-idle-agent-next-filter-design.md`

### Verification

```text
$ gofmt -w internal/codingagent/service_test.go internal/transport/server_test.go
$ git diff --check
(no output; passed)
$ go test ./internal/codingagent -run 'TestServiceFocusNextAgent(DoesNothingForEmptyStore|RepeatedlySelectsOnlyIdleAgent)$' -count=1
ok  \tzellij-with-codeagent/internal/codingagent\t0.190s
$ go test ./internal/transport -run '^TestServerFocusNextAgentReturnsSuccessfulNoOp$' -count=1
ok  \tzellij-with-codeagent/internal/transport\t0.188s
```

### Mutation Coverage

- `TestServiceFocusNextAgentDoesNothingForEmptyStore` catches selecting or
  focusing an agent from an empty store, returning `Focused: true`, returning a
  non-zero agent ID, or advancing the cursor during the no-op.
- `TestServiceFocusNextAgentRepeatedlySelectsOnlyIdleAgent` catches selecting a
  non-idle record, failing to wrap a single eligible record, omitting runtime
  focus, or failing to retain the selected cursor.
- `TestServerFocusNextAgentReturnsSuccessfulNoOp` catches omitting the
  `focused` JSON key or serializing it as true. It deliberately supplies an
  agent while `focused` is false so the test remains dependent only on the
  normative flag.

### Self-review

- Production behavior and JSON tags were left unchanged.
- The contract now identifies only the explicit `focused: false` flag as
  normative for no-op responses; clients must ignore `agent` in that case.
- The diff is limited to final-review coverage and contract clarification.
