#[cfg_attr(not(target_family = "wasm"), allow(dead_code))]
mod model;

#[cfg(target_family = "wasm")]
use std::collections::BTreeMap;

#[cfg(target_family = "wasm")]
use model::{command_argv, parse_navigation, BridgeModel};
#[cfg(target_family = "wasm")]
use zellij_tile::prelude::*;

#[cfg(target_family = "wasm")]
#[derive(Default)]
struct AgentNextBridge {
    model: BridgeModel,
    request_sequence: u64,
}

#[cfg(target_family = "wasm")]
register_plugin!(AgentNextBridge);

#[cfg(target_family = "wasm")]
impl ZellijPlugin for AgentNextBridge {
    fn load(&mut self, configuration: BTreeMap<String, String>) {
        if configuration
            .get("executable_path")
            .map(|value| value.trim().is_empty())
            .unwrap_or(true)
        {
            eprintln!("agent-next bridge requires a non-empty executable_path configuration");
        }
        self.model = BridgeModel::new(configuration.get("executable_path").cloned());
        set_selectable(false);
        hide_self();
        subscribe(&[
            EventType::ModeUpdate,
            EventType::PaneUpdate,
            EventType::PermissionRequestResult,
            EventType::RunCommandResult,
        ]);
        request_permission(&[
            PermissionType::ReadApplicationState,
            PermissionType::RunCommands,
        ]);
    }

    fn pipe(&mut self, pipe_message: PipeMessage) -> bool {
        match parse_navigation(&pipe_message.name, pipe_message.payload.as_deref()) {
            Ok(navigation) => {
                self.model.queue(navigation);
                self.flush_ready();
            }
            Err(error) => eprintln!("agent-next bridge: {error}"),
        }
        false
    }

    fn update(&mut self, event: Event) -> bool {
        match event {
            Event::ModeUpdate(mode_info) => {
                self.model
                    .set_session_name(mode_info.session_name.as_deref().unwrap_or_default());
                self.flush_ready();
            }
            Event::PaneUpdate(pane_manifest) => {
                if let Some(pane) = pane_manifest
                    .panes
                    .values()
                    .flatten()
                    .find(|pane| pane.is_focused && !pane.is_plugin)
                {
                    self.model.set_last_terminal(Some(pane.id));
                }
            }
            Event::PermissionRequestResult(status) => {
                let granted = status == PermissionStatus::Granted;
                self.model.set_permission(granted);
                if !granted {
                    eprintln!("agent-next bridge permissions were denied; queued work discarded");
                }
                self.flush_ready();
            }
            Event::RunCommandResult(exit_code, _, stderr, context) => {
                if exit_code != Some(0) {
                    let request_id = context
                        .get("request_id")
                        .map(String::as_str)
                        .unwrap_or("unknown");
                    eprintln!(
                        "agent-next bridge request {request_id} failed: exit_code={exit_code:?} stderr={}",
                        String::from_utf8_lossy(&stderr)
                    );
                }
            }
            _ => {}
        }
        false
    }
}

#[cfg(target_family = "wasm")]
impl AgentNextBridge {
    fn flush_ready(&mut self) {
        for job in self.model.take_ready() {
            self.request_sequence += 1;
            let request_id = self.request_sequence.to_string();
            let focused = match get_focused_pane_info() {
                Ok((_, pane_id)) => pane_id,
                Err(error) => {
                    eprintln!(
                        "agent-next bridge request {request_id} dropped: focused pane unavailable: {error}"
                    );
                    continue;
                }
            };
            let source_pane_id = match self.model.resolve_source_pane(focused) {
                Ok(source_pane_id) => source_pane_id,
                Err(error) => {
                    eprintln!("agent-next bridge request {request_id} dropped: {error}");
                    continue;
                }
            };
            let argv = command_argv(&job.executable, job.navigation);
            run_command_with_env_variables_and_cwd(
                &argv.iter().map(String::as_str).collect::<Vec<_>>(),
                BTreeMap::from([
                    ("ZELLIJ_SESSION_NAME".into(), job.session_name),
                    ("ZELLIJ_PANE_ID".into(), source_pane_id),
                ]),
                get_plugin_ids().initial_cwd,
                BTreeMap::from([("request_id".into(), request_id)]),
            );
        }
    }
}
