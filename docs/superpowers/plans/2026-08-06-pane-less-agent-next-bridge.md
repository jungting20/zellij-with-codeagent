# Pane-less Agent Next Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `Alt+o`/`Alt+p` Zellij `Run` bindings with a background WASM bridge that invokes agent navigation without creating terminal panes.

**Architecture:** A small `zellij-tile` plugin receives `MessagePlugin` pipe messages, resolves the source Zellij session and terminal pane for its client, and runs the existing `zellij-agent agent next` CLI in the host background with explicit Zellij environment variables. The existing CLI, Unix-socket transport, daemon cursor, idle filter, and `RuntimeService` focus boundary remain authoritative.

**Tech Stack:** Rust 1.88, `zellij-tile = 0.44.1`, `wasm32-wasip1`, Bash, Zellij 0.44.1, and the existing Go test suite.

## Global Constraints

- Never create a terminal or floating command pane for either shortcut.
- Keep `Alt+o` mapped to all managed agents and `Alt+p` mapped to idle agents only.
- Keep both bindings active in every current mode except `locked`.
- Invoke the public CLI; do not duplicate navigation or bypass the daemon/runtime boundary.
- Pin `zellij-tile` exactly to `=0.44.1`, matching installed Zellij `0.44.1`.
- Build plugins with Zellij's official `wasm32-wasip1` target.
- Package the plugin as a WASI executable binary that exports `_start`, not as a `cdylib`/`rlib` module.
- Treat this as background logic, so no new default role is required.
- Install WASM atomically at `~/.config/zellij/plugins/agent-next-bridge.wasm`.
- Configure `/Users/in05908_mac/.config/custom-cli/zellij-agent` explicitly instead of relying on `PATH`.
- Preserve unrelated user changes and write every commit message in Korean.

## File Structure

- Create `plugins/agent-next-bridge/Cargo.toml` and `Cargo.lock`: isolated, reproducible WASM crate.
- Create `plugins/agent-next-bridge/src/model.rs`: pure parsing, queue, argv, and source-pane fallback behavior.
- Create `plugins/agent-next-bridge/src/main.rs` as a named WASI binary target with the test module entry, then extend it into the thin Zellij lifecycle and host-command adapter.
- Create `scripts/install-agent-next-bridge.sh`: release build and atomic local installation.
- Modify `.gitignore`, `README.md`, and `docs/manual-smoke-test.md`.
- Modify the untracked user file `~/.config/zellij/config.kdl` only after creating an exact backup.

---

### Task 1: Add the bridge model and reproducible Rust crate

**Files:**
- Create: `plugins/agent-next-bridge/Cargo.toml`
- Create: `plugins/agent-next-bridge/Cargo.lock`
- Create: `plugins/agent-next-bridge/src/main.rs`
- Create: `plugins/agent-next-bridge/src/model.rs`
- Modify: `.gitignore`

**Interfaces:**
- Produces: `Navigation::{All, IdleOnly}`.
- Produces: `ReadyJob { executable: String, session_name: String, navigation: Navigation }`.
- Produces: `parse_navigation(name, payload)`, `command_argv(executable, navigation)`, and `source_pane_id(focused, last_terminal)`.
- Produces: `BridgeModel: Default` and `BridgeModel::{queue, set_permission, set_session_name, set_last_terminal, take_ready}` where `take_ready() -> Vec<ReadyJob>`.

- [ ] **Step 1: Create the manifest and failing model tests**

Create `Cargo.toml` with:

```toml
[package]
name = "agent-next-bridge"
version = "0.1.0"
edition = "2021"
publish = false

[[bin]]
name = "agent_next_bridge"
path = "src/main.rs"

[dependencies]
zellij-tile = "=0.44.1"

[profile.release]
opt-level = "z"
lto = true
codegen-units = 1
strip = true
```

Add `/plugins/agent-next-bridge/target/` to `.gitignore`. In `model.rs`, add tests for:

```rust
assert_eq!(parse_navigation("agent-next", Some("all")), Ok(Navigation::All));
assert_eq!(parse_navigation("agent-next", Some("idle-only")), Ok(Navigation::IdleOnly));
assert!(parse_navigation("other", Some("all")).is_err());
assert_eq!(command_argv("/opt/zellij-agent", Navigation::All), vec!["/opt/zellij-agent", "agent", "next"]);
assert_eq!(source_pane_id(PaneId::Terminal(7), None), Ok("terminal_7".into()));
assert_eq!(source_pane_id(PaneId::Plugin(2), Some(7)), Ok("terminal_7".into()));
assert!(source_pane_id(PaneId::Plugin(2), None).is_err());
```

Create `src/main.rs` with `mod model;` and a native-only empty `main` so Cargo has a native test target before host integration is added. Keep the binary target name `agent_next_bridge` so the WASI artifact remains `agent_next_bridge.wasm` for the installer.

Also assert that requests remain queued until permission and a non-empty session name exist, are drained exactly once, are not coalesced, and are discarded when permission is denied.

- [ ] **Step 2: Run tests and confirm failure**

Run `cargo test --manifest-path plugins/agent-next-bridge/Cargo.toml`.

Expected: FAIL because the model symbols do not exist.

- [ ] **Step 3: Implement minimal pure behavior**

Implement exact message mapping:

```rust
match (name, payload) {
    ("agent-next", Some("all")) => Ok(Navigation::All),
    ("agent-next", Some("idle-only")) => Ok(Navigation::IdleOnly),
    _ => Err(format!("unsupported pipe message name={name:?} payload={payload:?}")),
}
```

Build argv as `[executable, "agent", "next"]`, adding `"--idle-only"` only for `IdleOnly`. Resolve `PaneId::Terminal(id)` as `terminal_N`; for `PaneId::Plugin(_)`, use the remembered terminal or return an error.

Use internal permission states `Pending`, `Granted`, and `Denied`. `take_ready` returns `Vec<ReadyJob>` and drains the full queue only when executable, granted permission, and trimmed session name are available. Each job owns the trimmed executable and session name so host dispatch never re-reads mutable readiness state.

- [ ] **Step 4: Generate lockfile and verify tests**

```bash
cargo generate-lockfile --manifest-path plugins/agent-next-bridge/Cargo.toml
cargo test --manifest-path plugins/agent-next-bridge/Cargo.toml
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add .gitignore plugins/agent-next-bridge
git commit -m "feat: 에이전트 순회 브리지 모델 추가"
```

---

### Task 2: Wire the model to the Zellij plugin lifecycle

**Files:**
- Modify: `plugins/agent-next-bridge/src/main.rs`
- Modify: `plugins/agent-next-bridge/src/model.rs`

**Interfaces:**
- Consumes Task 1 model interfaces.
- Consumes plugin configuration key `executable_path`.
- Emits `ZELLIJ_SESSION_NAME=<session>` and `ZELLIJ_PANE_ID=terminal_N` for the host command.

- [ ] **Step 1: Add lifecycle state tests**

Test permission-before-session and session-before-permission ordering, one-time draining, denied permission, whitespace-only configuration, and two consecutive queued keys producing two jobs.

- [ ] **Step 2: Run focused tests and confirm failure**

Run `cargo test --manifest-path plugins/agent-next-bridge/Cargo.toml model::tests`.

Expected: FAIL until lifecycle setters and trimming match the tests.

- [ ] **Step 3: Implement the plugin adapter**

Create `AgentNextBridge { model: BridgeModel, request_sequence: u64 }`, register it with `register_plugin!`, and implement:

```rust
fn load(&mut self, configuration: BTreeMap<String, String>) {
    self.model = BridgeModel::new(configuration.get("executable_path").cloned());
    set_selectable(false);
    hide_self();
    subscribe(&[
        EventType::ModeUpdate,
        EventType::PaneUpdate,
        EventType::SessionUpdate,
        EventType::PermissionRequestResult,
        EventType::RunCommandResult,
    ]);
    request_permission(&[
        PermissionType::ReadApplicationState,
        PermissionType::RunCommands,
    ]);
}
```

`pipe` must accept only `agent-next/all` and `agent-next/idle-only`, queue each accepted message, flush ready jobs, return `false`, and log unsupported messages.

`update` must:

- store `ModeUpdate.session_name` when available and use the
  `SessionUpdate.is_current_session` entry as the startup-safe source, then flush;
- remember a focused non-plugin pane from `PaneUpdate`, preserving it when a plugin becomes focused;
- grant or deny queued work on `PermissionRequestResult` without duplicate dispatch;
- log non-zero `RunCommandResult` exit and stderr without showing UI.

For each ready job, `flush_ready` calls `get_focused_pane_info`, resolves the terminal source with the remembered fallback, constructs argv, and calls:

```rust
run_command_with_env_variables_and_cwd(
    &argv.iter().map(String::as_str).collect::<Vec<_>>(),
    BTreeMap::from([
        ("ZELLIJ_SESSION_NAME".into(), session_name),
        ("ZELLIJ_PANE_ID".into(), source_pane_id),
    ]),
    get_plugin_ids().initial_cwd,
    BTreeMap::from([("request_id".into(), request_id)]),
);
```

Drop and log only the failed job when context resolution fails. Do not retry, render, or call Zellij focus commands directly.

- [ ] **Step 4: Verify native and WASM builds**

```bash
cargo test --manifest-path plugins/agent-next-bridge/Cargo.toml
cargo build --release --target wasm32-wasip1 --manifest-path plugins/agent-next-bridge/Cargo.toml
test -s plugins/agent-next-bridge/target/wasm32-wasip1/release/agent_next_bridge.wasm
strings plugins/agent-next-bridge/target/wasm32-wasip1/release/agent_next_bridge.wasm | grep -qx '_start'
```

Expected: all commands exit 0.

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/plans/2026-08-06-pane-less-agent-next-bridge.md plugins/agent-next-bridge/src
git commit -m "feat: 백그라운드 에이전트 순회 플러그인 추가"
```

---

### Task 3: Add atomic installation and operator documentation

**Files:**
- Create: `scripts/install-agent-next-bridge.sh`
- Modify: `README.md`
- Modify: `docs/manual-smoke-test.md`

**Interfaces:**
- Installs the release artifact as `${ZELLIJ_PLUGIN_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/zellij/plugins}/agent-next-bridge.wasm`.
- Documents configuration identity, one-time permissions, and pane-count verification.

- [ ] **Step 1: Prove the missing installer fails**

```bash
AGENT_NEXT_TEST_DIR="$(mktemp -d)"
ZELLIJ_PLUGIN_DIR="$AGENT_NEXT_TEST_DIR" ./scripts/install-agent-next-bridge.sh
```

Expected: FAIL because the script does not exist. Remove only the exact temporary directory.

- [ ] **Step 2: Implement strict atomic installation**

Create a `set -euo pipefail` script that resolves the repository relative to itself, builds the exact manifest with `--release --target wasm32-wasip1`, creates the destination directory, copies into `mktemp` inside that directory, applies mode `0644`, and uses `mv -f` for atomic replacement. A trap must remove only the resolved temporary file on failure. Print the final absolute path; do not edit config.

- [ ] **Step 3: Verify installer behavior**

```bash
zsh -n scripts/install-agent-next-bridge.sh
AGENT_NEXT_TEST_DIR="$(mktemp -d)"
ZELLIJ_PLUGIN_DIR="$AGENT_NEXT_TEST_DIR" ./scripts/install-agent-next-bridge.sh
test -s "$AGENT_NEXT_TEST_DIR/agent-next-bridge.wasm"
```

Expected: PASS. Remove only the exact temporary directory.

- [ ] **Step 4: Document the operator contract**

In `README.md`, state that the two shortcuts use a background bridge, create no terminal panes, request `ReadApplicationState` and `RunCommands` once, and delegate to existing CLI commands.

In `docs/manual-smoke-test.md`, require before/after `zellij action list-panes --all` inventories, all-agent wrap, idle-only wrap, zero-idle no-op, multiple non-locked modes, and locked-mode exclusion.

- [ ] **Step 5: Verify and commit**

```bash
rg -n 'agent-next-bridge|Alt\+o|Alt\+p|ReadApplicationState|RunCommands|list-panes' README.md docs/manual-smoke-test.md
git diff --check
git add scripts/install-agent-next-bridge.sh README.md docs/manual-smoke-test.md
git commit -m "docs: 패인 없는 순회 설치와 검증 절차 추가"
```

---

### Task 4: Install, switch the live config, and verify pane stability

**Files:**
- Modify outside Git: `/Users/in05908_mac/.config/zellij/config.kdl`
- Back up outside Git: `/Users/in05908_mac/.config/zellij/config.kdl.agent-next-bridge.bak`
- Install outside Git: `/Users/in05908_mac/.config/zellij/plugins/agent-next-bridge.wasm`

**Interfaces:**
- Plugin URL: `file:/Users/in05908_mac/.config/zellij/plugins/agent-next-bridge.wasm`.
- Configuration: `executable_path "/Users/in05908_mac/.config/custom-cli/zellij-agent"`.
- Pipe name `agent-next`, payloads `all` and `idle-only`.

- [ ] **Step 1: Run full pre-rollout verification**

```bash
cargo test --manifest-path plugins/agent-next-bridge/Cargo.toml
cargo build --release --target wasm32-wasip1 --manifest-path plugins/agent-next-bridge/Cargo.toml
strings plugins/agent-next-bridge/target/wasm32-wasip1/release/agent_next_bridge.wasm | grep -qx '_start'
go test ./...
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 2: Install WASM and back up config**

```bash
./scripts/install-agent-next-bridge.sh
test -s /Users/in05908_mac/.config/zellij/plugins/agent-next-bridge.wasm
test -e /Users/in05908_mac/.config/zellij/config.kdl.agent-next-bridge.bak || cp \
  /Users/in05908_mac/.config/zellij/config.kdl \
  /Users/in05908_mac/.config/zellij/config.kdl.agent-next-bridge.bak
```

- [ ] **Step 3: Replace `Run` with identical plugin identities**

Use this exact binding block:

```kdl
shared_except "locked" {
    bind "Alt o" {
        MessagePlugin "file:/Users/in05908_mac/.config/zellij/plugins/agent-next-bridge.wasm" {
            executable_path "/Users/in05908_mac/.config/custom-cli/zellij-agent"
            name "agent-next"
            payload "all"
        }
    }
    bind "Alt p" {
        MessagePlugin "file:/Users/in05908_mac/.config/zellij/plugins/agent-next-bridge.wasm" {
            executable_path "/Users/in05908_mac/.config/custom-cli/zellij-agent"
            name "agent-next"
            payload "idle-only"
        }
    }
}
```

Use this exact background-load block:

```kdl
load_plugins {
    "file:/Users/in05908_mac/.config/zellij/plugins/agent-next-bridge.wasm" {
        executable_path "/Users/in05908_mac/.config/custom-cli/zellij-agent"
    }
}
```

The URL and `executable_path` must match in all three places; otherwise Zellij treats them as different plugin identities and can launch duplicates.

- [ ] **Step 4: Validate config**

```bash
zellij setup --check
rg -n -A18 -B3 'bind "Alt o"|bind "Alt p"|load_plugins|agent-next-bridge' /Users/in05908_mac/.config/zellij/config.kdl
```

Expected: parse succeeds, neither binding contains `Run`, and all plugin identities match.

- [ ] **Step 5: Load and approve the bridge**

Start a new session or run:

```bash
zellij action start-or-reload-plugin \
  --configuration 'executable_path=/Users/in05908_mac/.config/custom-cli/zellij-agent' \
  file:/Users/in05908_mac/.config/zellij/plugins/agent-next-bridge.wasm
```

Approve only `ReadApplicationState` and `RunCommands`. Expected: one-time permission UI, then a hidden background plugin and no visible terminal pane.

- [ ] **Step 6: Verify navigation without terminal-pane changes**

```bash
zellij action list-panes --all > /tmp/agent-next-before.txt
# Press Alt+o once.
zellij action list-panes --all > /tmp/agent-next-after-all.txt
# Press Alt+p once.
zellij action list-panes --all > /tmp/agent-next-after-idle.txt
```

Compare `TYPE=terminal` pane IDs across all inventories. Expected: no terminal pane is added or removed. Confirm all-agent wrap, idle-only wrap, zero-idle no-op, normal/pane/tab modes, and locked-mode exclusion.

- [ ] **Step 7: Final verification**

```bash
git status --short
git log -4 --oneline
test -s /Users/in05908_mac/.config/zellij/plugins/agent-next-bridge.wasm
zellij-agent ctl health
```

Expected: clean tree, Korean implementation commits, non-empty installed WASM, and healthy daemon.
