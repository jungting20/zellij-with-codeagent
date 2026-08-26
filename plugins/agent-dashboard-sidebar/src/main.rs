#[cfg_attr(not(target_family = "wasm"), allow(dead_code))]
mod model;

#[cfg(not(target_family = "wasm"))]
fn main() {}

#[cfg(target_family = "wasm")]
use model::{focus_command, list_command, row_text, SidebarModel};
#[cfg(target_family = "wasm")]
use std::collections::BTreeMap;
#[cfg(target_family = "wasm")]
use zellij_tile::prelude::*;

#[cfg(target_family = "wasm")]
const TOGGLE_MESSAGE: &str = "toggle-agent-dashboard";
#[cfg(target_family = "wasm")]
const REFRESH_SECONDS: f64 = 2.0;

#[cfg(any(test, target_family = "wasm"))]
fn required_permissions() -> &'static [zellij_tile::prelude::PermissionType] {
    use zellij_tile::prelude::PermissionType;

    &[
        PermissionType::ReadApplicationState,
        PermissionType::ChangeApplicationState,
        PermissionType::RunCommands,
    ]
}

#[cfg(target_family = "wasm")]
#[derive(Default)]
struct AgentDashboardSidebar {
    model: SidebarModel,
    executable: Option<String>,
    plugin_id: Option<u32>,
    session_name: String,
    last_terminal: Option<u32>,
    own_tab_index: Option<usize>,
    active_tab_index: Option<usize>,
    permission_granted: bool,
    list_in_flight: bool,
    request_sequence: u64,
}

#[cfg(target_family = "wasm")]
register_plugin!(AgentDashboardSidebar);

#[cfg(target_family = "wasm")]
impl ZellijPlugin for AgentDashboardSidebar {
    fn load(&mut self, configuration: BTreeMap<String, String>) {
        self.executable = configuration
            .get("executable_path")
            .map(|value| value.trim().to_owned())
            .filter(|value| !value.is_empty());
        self.plugin_id = Some(get_plugin_ids().plugin_id);
        // Keep the pane selectable until Zellij's permission prompt is answered.
        // Otherwise the permission UI itself cannot receive focus.
        set_selectable(true);
        subscribe(&[
            EventType::Key,
            EventType::ModeUpdate,
            EventType::PaneUpdate,
            EventType::TabUpdate,
            EventType::SessionUpdate,
            EventType::PermissionRequestResult,
            EventType::RunCommandResult,
            EventType::Timer,
        ]);
        request_permission(required_permissions());
        set_timeout(0.1);
    }

    fn pipe(&mut self, message: PipeMessage) -> bool {
        if message.name != TOGGLE_MESSAGE {
            return false;
        }
        if self.own_tab_index.is_none() || self.own_tab_index != self.active_tab_index {
            return false;
        }
        if self.model.interactive() {
            self.deactivate_and_restore();
        } else {
            self.remember_current_terminal();
            self.model.toggle_interactive();
            set_selectable(true);
            if let Some(plugin_id) = self.plugin_id {
                focus_pane_with_id(PaneId::Plugin(plugin_id), false, false);
            }
        }
        true
    }

    fn update(&mut self, event: Event) -> bool {
        match event {
            Event::Key(key) if self.model.interactive() => self.handle_key(key),
            Event::ModeUpdate(mode_info) => {
                self.session_name = mode_info.session_name.unwrap_or_default();
                false
            }
            Event::PaneUpdate(panes) => {
                self.update_own_tab_index(&panes);
                if !self.model.interactive() {
                    self.remember_terminal_from_manifest(&panes);
                }
                false
            }
            Event::TabUpdate(tabs) => {
                self.active_tab_index = tabs.iter().position(|tab| tab.active);
                false
            }
            Event::SessionUpdate(sessions, _) => {
                if let Some(session) = sessions.iter().find(|session| session.is_current_session) {
                    self.session_name = session.name.clone();
                }
                false
            }
            Event::PermissionRequestResult(status) => {
                self.permission_granted = status == PermissionStatus::Granted;
                if self.permission_granted {
                    self.remember_current_terminal();
                    self.refresh();
                    if !self.model.interactive() {
                        set_selectable(false);
                    }
                } else {
                    self.model.set_status("permissions denied");
                    self.deactivate_and_restore();
                }
                true
            }
            Event::RunCommandResult(exit_code, stdout, stderr, context) => {
                self.handle_command_result(exit_code, stdout, stderr, context)
            }
            Event::Timer(_) => {
                self.refresh();
                set_timeout(REFRESH_SECONDS);
                false
            }
            _ => false,
        }
    }

    fn render(&mut self, rows: usize, cols: usize) {
        if rows == 0 || cols == 0 {
            return;
        }
        let title = if self.model.interactive() {
            " Agents · CONTROL "
        } else {
            " Agents · Alt+z "
        };
        print_text_with_coordinates(Text::new(title).selected(), 0, 0, Some(cols), Some(1));
        if rows > 1 {
            print_text_with_coordinates(
                Text::new(model::truncate_display(
                    self.model.status(),
                    cols.saturating_sub(1),
                ))
                .opaque(),
                1,
                1,
                Some(cols.saturating_sub(1)),
                Some(1),
            );
        }
        let available = rows.saturating_sub(3);
        for (index, row) in self.model.rows().iter().take(available).enumerate() {
            let text = format!(
                "{} {}",
                if index == self.model.selected() {
                    "›"
                } else {
                    " "
                },
                row_text(row, cols.saturating_sub(3))
            );
            let text = Text::new(text);
            let text = if index == self.model.selected() && self.model.interactive() {
                text.selected()
            } else if row.state == "idle" {
                text.color_all(2).opaque()
            } else {
                text.opaque()
            };
            print_text_with_coordinates(text, 0, index + 2, Some(cols), Some(1));
        }
        if rows > 2 {
            let footer = if self.model.interactive() {
                " j/k move · Enter focus · Esc "
            } else {
                " "
            };
            print_text_with_coordinates(
                Text::new(model::truncate_display(footer, cols)).opaque(),
                0,
                rows - 1,
                Some(cols),
                Some(1),
            );
        }
    }
}

#[cfg(target_family = "wasm")]
impl AgentDashboardSidebar {
    fn handle_key(&mut self, key: KeyWithModifier) -> bool {
        match key.bare_key {
            BareKey::Up if key.has_no_modifiers() => self.model.move_up(),
            BareKey::Char('k') if key.has_no_modifiers() => self.model.move_up(),
            BareKey::Down if key.has_no_modifiers() => self.model.move_down(),
            BareKey::Char('j') if key.has_no_modifiers() => self.model.move_down(),
            BareKey::Enter if key.has_no_modifiers() => {
                if let Some(agent_id) = self.model.selected_id().map(str::to_owned) {
                    self.run_focus(&agent_id);
                    self.model.deactivate();
                    set_selectable(false);
                }
            }
            BareKey::Esc if key.has_no_modifiers() => self.deactivate_and_restore(),
            _ => return false,
        }
        true
    }

    fn deactivate_and_restore(&mut self) {
        self.model.deactivate();
        if let Some(terminal_id) = self.last_terminal {
            focus_pane_with_id(PaneId::Terminal(terminal_id), false, false);
        }
        set_selectable(false);
    }

    fn remember_current_terminal(&mut self) {
        if let Ok((_, PaneId::Terminal(terminal_id))) = get_focused_pane_info() {
            self.last_terminal = Some(terminal_id);
        }
    }

    fn remember_terminal_from_manifest(&mut self, panes: &PaneManifest) {
        if let Ok((tab_index, _)) = get_focused_pane_info() {
            if let Some(pane) = get_focused_pane(tab_index, panes) {
                if !pane.is_plugin {
                    self.last_terminal = Some(pane.id);
                }
            }
        }
    }

    fn update_own_tab_index(&mut self, panes: &PaneManifest) {
        let Some(plugin_id) = self.plugin_id else {
            return;
        };
        self.own_tab_index = panes.panes.iter().find_map(|(tab_index, tab_panes)| {
            tab_panes
                .iter()
                .any(|pane| pane.is_plugin && pane.id == plugin_id)
                .then_some(*tab_index)
        });
    }

    fn refresh(&mut self) {
        if !self.permission_granted || self.list_in_flight {
            return;
        }
        let Some(executable) = self.executable.as_deref() else {
            self.model.set_status("missing executable_path");
            return;
        };
        self.list_in_flight = true;
        self.run_command(list_command(executable), "list");
    }

    fn run_focus(&mut self, agent_id: &str) {
        let Some(executable) = self.executable.as_deref() else {
            self.model.set_status("missing executable_path");
            return;
        };
        self.run_command(focus_command(executable, agent_id), "focus");
    }

    fn run_command(&mut self, argv: Vec<String>, kind: &str) {
        self.request_sequence += 1;
        let mut environment = BTreeMap::new();
        if !self.session_name.is_empty() {
            environment.insert("ZELLIJ_SESSION_NAME".into(), self.session_name.clone());
        }
        if let Some(terminal_id) = self.last_terminal {
            environment.insert("ZELLIJ_PANE_ID".into(), format!("terminal_{terminal_id}"));
        }
        run_command_with_env_variables_and_cwd(
            &argv.iter().map(String::as_str).collect::<Vec<_>>(),
            environment,
            get_plugin_ids().initial_cwd,
            BTreeMap::from([
                ("kind".into(), kind.into()),
                ("request_id".into(), self.request_sequence.to_string()),
            ]),
        );
    }

    fn handle_command_result(
        &mut self,
        exit_code: Option<i32>,
        stdout: Vec<u8>,
        stderr: Vec<u8>,
        context: BTreeMap<String, String>,
    ) -> bool {
        let kind = context.get("kind").map(String::as_str).unwrap_or("unknown");
        if kind == "list" {
            self.list_in_flight = false;
        }
        if exit_code != Some(0) {
            let error = String::from_utf8_lossy(&stderr);
            self.model
                .set_status(format!("{kind} failed: {}", error.trim()));
            return true;
        }
        match kind {
            "list" => {
                if let Err(error) = self.model.replace_from_json(&stdout) {
                    self.model
                        .set_status(format!("invalid agent data: {error}"));
                }
            }
            "focus" => self.model.set_status("agent focused"),
            _ => {}
        }
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use zellij_tile::prelude::PermissionType;

    #[test]
    fn sidebar_requests_only_required_permissions() {
        assert_eq!(
            required_permissions(),
            &[
                PermissionType::ReadApplicationState,
                PermissionType::ChangeApplicationState,
                PermissionType::RunCommands,
            ]
        );
    }
}
