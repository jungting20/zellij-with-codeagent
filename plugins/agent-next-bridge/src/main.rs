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
use model::command_argv;
#[cfg(target_family = "wasm")]
use zellij_tile::prelude::*;

#[cfg(target_family = "wasm")]
#[derive(Default)]
struct AgentNavigationBridge {
    executable: Option<String>,
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
        self.executable = configuration
            .get("executable_path")
            .map(|value| value.trim().to_owned())
            .filter(|value| !value.is_empty());
        set_selectable(false);
        hide_self();
        subscribe(&[
            EventType::PermissionRequestResult,
            EventType::RunCommandResult,
        ]);
        request_permission(required_permissions());
    }

    fn pipe(&mut self, pipe_message: PipeMessage) -> bool {
        if pipe_message.name != "agent-next" {
            return false;
        }
        let Some(executable) = self.executable.as_deref() else {
            eprintln!("agent navigation bridge executable is missing; request ignored");
            return false;
        };
        let Some(argv) = command_argv(executable, pipe_message.payload.as_deref()) else {
            eprintln!("agent navigation bridge received an unknown filter; request ignored");
            return false;
        };
        self.request_sequence += 1;
        // Zellij enforces RunCommands permission at the host boundary.
        run_command(
            &argv.iter().map(String::as_str).collect::<Vec<_>>(),
            BTreeMap::from([("request_id".into(), self.request_sequence.to_string())]),
        );
        false
    }

    fn update(&mut self, event: Event) -> bool {
        match event {
            Event::PermissionRequestResult(status) => {
                let granted = status == PermissionStatus::Granted;
                if !granted {
                    eprintln!("agent navigation bridge permissions were denied");
                }
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
            }
            _ => {}
        }
        false
    }
}
