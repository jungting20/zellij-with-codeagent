# Agent follow-up instructions

Follow-ups continue the selected agent's existing conversation. The daemon owns
the queue and sends one instruction at a time through `RuntimeService`; the
dashboard does not need to remain open. No new role or worker pane is created.

## Dashboard

In `zellij-agent agent dashboard`, select a Codex or Claude agent and press `f`.
The agent row shows the pending count and pause/attention indicators.

| Key | Action |
| --- | --- |
| `a` | Add a follow-up instruction |
| `e` | Edit the selected queued instruction |
| `d` | Cancel a queued instruction, or exclude an uncertain delivery |
| `p` | Pause/resume automatic delivery; an empty queue can be paused before adding instructions |
| `c` | Mark an uncertain delivery as handled after inspecting the agent; keep the queue paused |
| `r` | Refresh the list |
| Up/Down | Select an instruction |
| Enter / Alt+Enter | Save an edit / insert a newline |
| Esc | Cancel editing or close the list |

For example, add “로그인 실패 케이스 테스트 추가” and then “API 사용법 문서 작성”.
If the agent is already ready, the first instruction can be sent on the next
daemon scan. Pause the queue first when preparing several instructions together.
Only queued instructions can be edited; sending instructions cannot be recalled.

```text
후속 지시 · 로그인 구현
자동 전달
  1. [전송됨] 로그인 실패 케이스 테스트 추가
> 2. [대기] API 사용법 문서 작성
a 추가 · e 수정 · d 취소 · p 일시정지/재개
```

## Delivery rules

- Delivery uses a fresh pane snapshot, an explicit idle signal, and an empty
  input prompt. Working, blocked, unknown, startup grace, transcript overlays,
  interrupted conversations and idle fallback do not permit automatic input.
- User drafts defer delivery. Recognized, styled placeholder text can count as
  an empty prompt; unfamiliar prompt rendering is conservatively held. Pause
  automatic delivery when composing directly in the agent terminal.
- The initial supported agents are Codex and Claude. Gemini and Cursor currently
  infer idle from unmatched output, and Hermes has no state monitor, so automatic
  follow-ups are unavailable for them.
- After one delivery, the daemon waits for a new working observation and then a
  fresh ready prompt before sending another. Monitor revisions retain this
  evidence even if event subscribers miss a transition. If work start cannot be
  confirmed within 30 seconds, the queue pauses for inspection.
- `전송됨` means the terminal input operation succeeded, not that the requested
  work succeeded. Follow-ups do not run tests or judge task completion themselves.
- Runtime input serialization covers immediate API input, pane messages and
  follow-ups. Readiness and ownership are checked again inside that serialization
  boundary. Native keyboard activity is outside the daemon's input lock.
- Each queue allows up to 100 pending instructions and 64 KiB per instruction.
  Instructions run in insertion order. A stable request ID prevents duplicate
  additions when an API response is lost; the dashboard retains that ID on retry.

## Failure and restart

The daemon commits the sending record before attempting external input. If
paste/Enter fails, its result may be partial; the queue shows `전송 확인 필요`
and never retries that instruction automatically. Inspect the actual agent, then
use `c` to acknowledge the delivery or `d` to exclude it. Both keep the queue
paused; use `p` when ready to continue. Exclusion cannot undo already sent input.

SQLite preserves pending instructions, edits, cancellations, history and pause
state. Outstanding delivery/work-cycle observations are paused on daemon restart.
An exited or missing agent's queue remains stored under its original ID and is
never assigned to a new agent. The list remains accessible through the API using
that ID, even though the closed agent is absent from the dashboard.

An already running daemon needs to be restarted with the updated binary to expose
this feature. Restart recovery does not close existing managed panes. The database
upgrade preserves existing data; see [daemon-persistence.md](daemon-persistence.md).

## Local API

- `GET /v1/agents/{agent_id}/followups` returns `{ "queue": ... }`.
- `POST /v1/agents/{agent_id}/followups` accepts `action` (`add`, `edit`, `cancel`,
  `pause`, `resume`, `resolve`), `item_id`, `text`, and optional `request_id` for
  idempotent adds. Supply only the fields needed for the action.
- `GET /v1/agents` includes `followup_count`, `followup_paused`, and
  `followup_attention` in each agent record.

The API uses the existing local Unix socket. A daemon without queue support
returns an actionable unsupported response instead of silently dropping a request.
