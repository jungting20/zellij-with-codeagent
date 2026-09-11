#[cfg_attr(not(target_family = "wasm"), allow(dead_code))]
mod model;

#[cfg(not(target_family = "wasm"))]
fn main() {}

#[cfg(any(test, target_family = "wasm"))]
fn required_permissions() -> &'static [zellij_tile::prelude::PermissionType] {
    use zellij_tile::prelude::PermissionType;

    &[PermissionType::RunCommands]
}

#[cfg(test)]
mod tests {
    use super::required_permissions;
    use zellij_tile::prelude::PermissionType;

    #[test]
    fn bridge_requests_only_the_permissions_it_uses() {
        assert_eq!(required_permissions(), &[PermissionType::RunCommands]);
    }
}

#[cfg(target_family = "wasm")]
use std::collections::BTreeMap;

#[cfg(target_family = "wasm")]
use model::{command_argv, parse_navigation, BridgeModel};
#[cfg(target_family = "wasm")]
use zellij_tile::prelude::*;

#[cfg(target_family = "wasm")]
#[derive(Default)]
struct AgentNavigationBridge {
    model: BridgeModel,
    request_sequence: u64,
}

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
            EventType::PermissionRequestResult,
            EventType::RunCommandResult,
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
            Event::PermissionRequestResult(status) => {
                let granted = status == PermissionStatus::Granted;
                self.model.set_permission(granted);
                if !granted {
                    eprintln!(
                        "agent navigation bridge permissions were denied; queued work discarded"
                    );
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
                        "agent navigation bridge request {request_id} failed: exit_code={exit_code:?} stderr={}",
                        String::from_utf8_lossy(&stderr)
                    );
                }
                self.flush_ready();
            }
            _ => {}
        }
        false
    }
}

#[cfg(target_family = "wasm")]
impl AgentNavigationBridge {
    fn flush_ready(&mut self) {
        // Each key launches its CLI immediately once permission is granted.
        // Source context and navigation belong to the CLI/runtime, not this bridge.
        while let Some(job) = self.model.next_ready() {
            self.request_sequence += 1;
            let argv = command_argv(&job.executable, job.navigation);
            run_command(
                &argv.iter().map(String::as_str).collect::<Vec<_>>(),
                BTreeMap::from([("request_id".into(), self.request_sequence.to_string())]),
            );
            self.model.complete_ready();
        }
    }
}
