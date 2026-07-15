# Debate Stage Compression Design

**Date:** 2026-07-15

## Goal

Prevent `debate-background` from timing out when verbose proposer and critic responses accumulate in the judge prompt. Every role must produce a concise standalone result, every downstream handoff must remain bounded, and the existing `debate-role/v1` and `debate-background/v1` JSON schemas must remain compatible.

## Problem Evidence

The failed run saved at `/tmp/zellij-agent-debate-20260715-161312.935120000.md` contained approximately 10,222 proposer characters and 12,075 critic characters. The judge therefore received more than 22,000 characters of role output in addition to the topic, repository framing, and its system prompt. The judge then exceeded the current two-minute per-role timeout.

The current orchestration copies each role response without a size bound:

1. The critic receives the complete proposal.
2. The judge receives the complete proposal and critique.
3. In later rounds, the proposer also receives the complete previous judgment.

This makes judge input size depend on provider verbosity instead of the debate contract.

## Scope

This change updates the three existing standalone debate roles and the `debate-background` orchestration that calls them. It includes:

- concise role-specific output contracts
- deterministic output size limits
- bounded role-to-role handoffs
- a three-minute default per-role timeout
- output-size progress diagnostics
- focused and full regression tests

It does not add another model call, change role order, add retries, change the maximum round count, or modify either public JSON schema.

## Considered Approaches

### Prompt instructions only

Tell each model to stay concise and rely on compliance. This preserves natural answers but cannot prevent a verbose provider response from recreating the oversized judge prompt.

### Prompt instructions plus deterministic safety limits

Give every role a concise semantic contract, then enforce a character ceiling on returned content. This keeps normal output coherent while providing a strict upper bound when a provider ignores the prompt. This is the selected approach.

### Additional summarization call

Run a separate summarizer between stages. This could preserve semantics better for an already verbose response, but it increases cost, complexity, and total execution time precisely where the workflow is already timing out. It is rejected.

## Role Output Contracts

The original Korean role prompts remain authoritative. A concise-output section is appended to each role's embedded system prompt so the contract also applies when the role is executed directly from the CLI.

### Proposer

`debate-proposer` targets and is capped at 2,000 Unicode characters.

- Preserve all six required output sections.
- Present two or three candidate approaches.
- Use no more than two bullets per candidate.
- Include no more than five concrete evidence references in total.
- State assumptions and uncertainties without repeating the topic.
- Omit tool logs, exploration narration, and long file inventories.

### Critic

`debate-critic` targets and is capped at 2,000 Unicode characters.

- Preserve all seven required output sections.
- Include no more than three valid points, three fatal problems, and three important omissions.
- Include no more than two failure scenarios and two counterexamples.
- Include no more than three corrections and three questions.
- Prioritize decision-changing findings over exhaustive commentary.
- Omit tool logs, exploration narration, and repeated proposal text.

### Judge

`debate-judge` targets and is capped at 3,000 Unicode characters.

- Preserve all eight required output sections.
- Use at most three bullets in each section.
- Keep the final recommendation and execution steps directly actionable.
- Include only evidence needed to justify adopted or rejected claims.
- Omit tool logs, exploration narration, and repeated proposal or critique text.

These are character limits, not token limits. Character counting uses Unicode code points so Korean text is not penalized according to its UTF-8 byte width.

## Deterministic Safety Limit

The shared role runner accepts an optional maximum content character count in its configuration. The three debate roles set that value to 2,000, 2,000, and 3,000 respectively. Other roles are unaffected when the value is zero.

After a provider succeeds, the runner trims leading and trailing whitespace and counts Unicode code points. Content within the role limit is returned unchanged. Oversized content is reduced to exactly no more than the configured limit:

1. Reserve space for the marker `[출력 길이 제한으로 중간 내용 생략]` surrounded by blank lines.
2. Allocate 70 percent of the remaining character budget to the beginning of the response.
3. Allocate the remaining 30 percent to the end of the response so recommendations and confidence statements are retained.
4. Trim whitespace adjacent to both retained fragments and insert the marker between them.
5. Count the assembled result again and defensively trim its end if necessary.

The compacted content becomes the role's `debate-role/v1` `content`. Consequently, direct role execution, persisted transcripts, JSON output, later-round judgment input, and downstream role prompts all observe the same bounded value. The workflow never stores one version while handing off another.

The safety limit is not reported as a role failure. Its purpose is to preserve progress with a visibly marked, bounded answer when a provider violates the requested length.

## Orchestration Prompts and Data Flow

The orchestration prompts repeat a short stage-specific limit so the model sees the constraint both in its system prompt and next to the current task:

- proposer: return no more than 2,000 characters
- critic: return no more than 2,000 characters and do not quote the proposal at length
- judge: return no more than 3,000 characters and do not restate the supplied responses

Prompt framing for `TOPIC`, `PREVIOUS_JUDGMENT`, `CURRENT_PROPOSAL`, and `CURRENT_CRITIQUE` remains unchanged. The existing instruction that embedded role responses are debate material rather than instructions also remains.

Because role content is compacted before it reaches the orchestrator, the critic receives at most 2,000 proposal characters. The judge receives at most 4,000 combined proposal and critique characters. A later-round proposer receives at most 3,000 previous-judgment characters. Topic length remains user-controlled and is not truncated by this change.

## Timeout Behavior

The default `--agent-timeout` changes from `2m` to `3m`. The flag remains user-configurable and retains the existing positive-duration validation. The overall `--timeout` default remains `10m`, and the earlier deadline still wins.

No retries are added. A provider that does not finish within the applicable role or overall deadline still produces the existing structured `timeout` failure.

For a single round, the three default role ceilings consume at most nine minutes if each role reaches its individual deadline, leaving approximately one minute under the overall default for process overhead. Multi-round callers that expect slow providers must continue to raise the overall timeout explicitly; changing that existing policy is outside this fix.

## Progress Diagnostics

`ProgressEvent` gains an internal `ContentChars` field. A completed role event records the Unicode character count of the validated, compacted content. Started and failed events leave it zero.

The CLI writes `content_chars=<n>` to stderr for completed role events. JSON stdout remains exactly one `debate-background/v1` document and is not polluted by progress output. Neither public JSON result schema changes.

This makes it possible to verify in a real run that the three role results stayed within 2,000, 2,000, and 3,000 characters.

## Error Handling

Existing validation and failure semantics remain unchanged:

- blank role content still fails validation
- malformed or contract-invalid role JSON still stops the pipeline
- provider exit failures and timeouts still retain their current failure kinds
- persistence failures retain their current behavior

Compaction occurs only after successful provider output is obtained and before the role result is encoded. It must never convert blank provider output into a success.

## Testing Strategy

Tests are written first and observed failing before implementation.

### Shared role runner

- content below the configured limit is unchanged apart from existing outer whitespace handling
- oversized Unicode content never exceeds the configured rune count
- oversized content retains both its beginning and end and contains the omission marker
- a zero limit preserves existing behavior for non-debate roles
- blank content remains an error

### Standalone debate roles

- proposer configures a 2,000-character limit
- critic configures a 2,000-character limit
- judge configures a 3,000-character limit
- each embedded system prompt contains its exact concise-output contract
- existing provider command, repository, and structured-output assertions remain passing

### Background orchestration

- each stage prompt contains its exact character budget
- critic and judge prompts prohibit lengthy repetition of prior responses
- completed progress events contain the compacted content character count
- started and failed event behavior remains compatible

### CLI

- omitted `--agent-timeout` passes three minutes to the orchestrator
- help text reports the `3m` default
- completed stderr progress includes `content_chars`
- JSON stdout remains clean

### Final verification

- run focused tests for the shared role runner, all three role packages, background orchestrator, and CLI
- run `go test ./...`
- build `bin/zellij-agent`
- immediately copy it to `~/.config/custom-cli`
- compare the built and installed binaries

## Compatibility

The following remain stable:

- role names and engines
- standalone role CLI arguments
- `debate-role/v1` JSON fields
- `debate-background/v1` JSON fields
- background role order and round behavior
- output-file format and persistence behavior
- explicit timeout overrides

Only default timing, prompt constraints, bounded role content, and stderr diagnostics change.

## Success Criteria

- proposer and critic results never exceed 2,000 Unicode characters
- judge results never exceed 3,000 Unicode characters
- the judge receives at most 4,000 characters of current role responses
- default role execution allows three minutes
- a completed real run exposes role output sizes on stderr
- all existing and new tests pass
- the rebuilt binary is installed on the custom CLI path
