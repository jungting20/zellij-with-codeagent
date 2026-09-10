#[cfg_attr(not(target_family = "wasm"), allow(dead_code))]
mod model;

#[cfg(not(target_family = "wasm"))]
fn main() {}

#[cfg(any(test, target_family = "wasm"))]
fn required_permissions() -> &'static [zellij_tile::prelude::PermissionType] {
    use zellij_tile::prelude::PermissionType;

    &[
        PermissionType::ReadApplicationState,
        PermissionType::RunCommands,
    ]
}

#[cfg(test)]
mod tests {
    use super::required_permissions;
    use zellij_tile::prelude::PermissionType;

    #[test]
    fn bridge_requests_only_the_permissions_it_uses() {
        assert_eq!(
            required_permissions(),
            &[
                PermissionType::ReadApplicationState,
                PermissionType::RunCommands,
            ]
        );
    }
}

#[cfg(target_family = "wasm")]
use std::collections::BTreeMap;

#[cfg(target_family = "wasm")]
use model::{command_argv, focused_client_pane, parse_navigation, BridgeModel};
#[cfg(target_family = "wasm")]
use zellij_tile::prelude::*;

#[cfg(target_family = "wasm")]
#[derive(Default)]
struct AgentNavigationBridge {
    model: BridgeModel,
    request_sequence: u64,
    command_in_flight: bool,
    retry_scheduled: bool,
    client_query_pending: bool,
}

#[cfg(target_family = "wasm")]
const RETRY_DELAY_SECONDS: f64 = 0.25;

#[cfg(target_family = "wasm")]
register_plugin!(AgentNavigationBridge);

#[cfg(target_family = "wasm")]
impl ZellijPlugin for AgentNavigationBridge {
    fn load(&mut self, configuration: BTreeMap<String, String>) {
        if configuration
            .get("executable_path")
            .map(|value| value.trim().is_empty())
            .unwrap_or(true)
        {
            eprintln!("agent navigation bridge requires a non-empty executable_path configuration");
        }
        self.model = BridgeModel::new(configuration.get("executable_path").cloned());
        set_selectable(false);
        hide_self();
        subscribe(&[
            EventType::ModeUpdate,
            EventType::PaneUpdate,
            EventType::TabUpdate,
            EventType::SessionUpdate,
            EventType::PermissionRequestResult,
            EventType::RunCommandResult,
            EventType::Timer,
            EventType::ListClients,
        ]);
        request_permission(required_permissions());
    }

    fn pipe(&mut self, pipe_message: PipeMessage) -> bool {
        match parse_navigation(&pipe_message.name, pipe_message.payload.as_deref()) {
            Ok(navigation) => {
                if self.model.queue(navigation) {
                    self.flush_ready();
                } else {
                    eprintln!("agent navigation bridge queue is full or disabled; request ignored");
                }
            }
            Err(error) => eprintln!("agent navigation bridge: {error}"),
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
                self.model.update_panes(pane_manifest);
                self.flush_ready();
            }
            Event::TabUpdate(tabs) => {
                self.model.update_tabs(&tabs);
                self.flush_ready();
            }
            Event::SessionUpdate(sessions, _) => {
                self.model.set_current_session(&sessions);
                self.flush_ready();
            }
            Event::PermissionRequestResult(status) => {
                let granted = status == PermissionStatus::Granted;
                self.model.set_permission(granted);
                if granted {
                    self.initialize_context();
                } else {
                    eprintln!(
                        "agent navigation bridge permissions were denied; queued work discarded"
                    );
                }
                self.flush_ready();
            }
            Event::RunCommandResult(exit_code, _, stderr, context) => {
                self.command_in_flight = false;
                if exit_code != Some(0) {
                    let request_id = context
                        .get("request_id")
                        .map(String::as_str)
                        .unwrap_or("unknown");
                    eprintln!(
                        "agent navigation bridge request {request_id} failed: exit_code={exit_code:?} stderr={}",
                        String::from_utf8_lossy(&stderr)
                    );
                }
                self.flush_ready();
            }
            Event::ListClients(clients) => {
                if self.client_query_pending {
                    self.client_query_pending = false;
                    if let Some(focused) = focused_client_pane(&clients) {
                        self.flush_with_focus(focused);
                    } else if clients.is_empty() {
                        // The client left this session. Do not replay old keys on reattach.
                        self.model.discard_pending();
                    } else {
                        self.schedule_retry();
                    }
                }
            }
            Event::Timer(_) => {
                self.retry_scheduled = false;
                self.flush_ready();
            }
            _ => {}
        }
        false
    }
}

#[cfg(target_family = "wasm")]
impl AgentNavigationBridge {
    fn initialize_context(&mut self) {
        let focused = get_focused_pane_info().ok().map(|(_, pane_id)| pane_id);
        self.model.initialize_focused_pane(focused);
    }

    fn flush_ready(&mut self) {
        if self.command_in_flight {
            return;
        }

        if self.client_query_pending || self.model.next_ready().is_none() {
            return;
        }
        match get_focused_pane_info() {
            Ok((_, focused)) => self.flush_with_focus(focused),
            Err(_) => {
                // A session switch can leave this plugin bound to a departed client ID.
                // Query live clients instead of retrying that stale ID forever.
                self.client_query_pending = true;
                list_clients();
            }
        }
    }

    fn flush_with_focus(&mut self, focused: PaneId) {
        if self.command_in_flight {
            return;
        }
        let Some(job) = self.model.next_ready() else {
            return;
        };
        self.model.initialize_focused_pane(Some(focused));
        let source_pane_id = match self.model.resolve_source_pane(focused) {
            Ok(source_pane_id) => source_pane_id,
            Err(error) => {
                eprintln!("agent navigation bridge delayed: {error}");
                self.schedule_retry();
                return;
            }
        };

        self.request_sequence += 1;
        let request_id = self.request_sequence.to_string();
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
        self.model.complete_ready();
        self.command_in_flight = true;
    }

    fn schedule_retry(&mut self) {
        if !self.retry_scheduled {
            self.retry_scheduled = true;
            set_timeout(RETRY_DELAY_SECONDS);
        }
    }
}
