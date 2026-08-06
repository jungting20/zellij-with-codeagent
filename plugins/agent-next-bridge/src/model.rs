use zellij_tile::prelude::{get_focused_pane, PaneId, PaneManifest};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Navigation {
    All,
    IdleOnly,
}

#[derive(Debug, Eq, PartialEq)]
pub struct ReadyJob {
    pub executable: String,
    pub session_name: String,
    pub navigation: Navigation,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Permission {
    Pending,
    Granted,
    Denied,
}

struct QueuedRequest {
    navigation: Navigation,
}

pub struct BridgeModel {
    executable: Option<String>,
    permission: Permission,
    session_name: String,
    last_terminal: Option<u32>,
    queue: Vec<QueuedRequest>,
}

impl Default for BridgeModel {
    fn default() -> Self {
        Self::new(None)
    }
}

impl BridgeModel {
    pub fn new(executable: Option<String>) -> Self {
        let executable = executable
            .map(|value| value.trim().to_owned())
            .filter(|value| !value.is_empty());
        Self {
            executable,
            permission: Permission::Pending,
            session_name: String::new(),
            last_terminal: None,
            queue: Vec::new(),
        }
    }

    pub fn queue(&mut self, navigation: Navigation) {
        if self.executable.is_none() {
            return;
        }
        self.queue.push(QueuedRequest { navigation });
    }

    pub fn set_permission(&mut self, granted: bool) {
        self.permission = if granted {
            Permission::Granted
        } else {
            self.queue.clear();
            Permission::Denied
        };
    }

    pub fn set_session_name(&mut self, session_name: impl AsRef<str>) {
        self.session_name = session_name.as_ref().trim().into();
    }

    pub fn set_last_terminal(&mut self, last_terminal: Option<u32>) {
        self.last_terminal = last_terminal;
    }

    pub fn resolve_source_pane(&self, focused: PaneId) -> Result<String, String> {
        source_pane_id(focused, self.last_terminal)
    }

    pub fn take_ready(&mut self) -> Vec<ReadyJob> {
        let Some(executable) = self.executable.as_ref() else {
            return Vec::new();
        };
        if self.permission != Permission::Granted || self.session_name.is_empty() {
            return Vec::new();
        }

        let executable = executable.clone();
        let session_name = self.session_name.clone();
        self.queue
            .drain(..)
            .map(|request| ReadyJob {
                executable: executable.clone(),
                session_name: session_name.clone(),
                navigation: request.navigation,
            })
            .collect()
    }
}

pub fn parse_navigation(name: &str, payload: Option<&str>) -> Result<Navigation, String> {
    match (name, payload) {
        ("agent-next", Some("all")) => Ok(Navigation::All),
        ("agent-next", Some("idle-only")) => Ok(Navigation::IdleOnly),
        _ => Err(format!(
            "unsupported pipe message name={name:?} payload={payload:?}"
        )),
    }
}

pub fn command_argv(executable: &str, navigation: Navigation) -> Vec<String> {
    let mut argv = vec![executable.into(), "agent".into(), "next".into()];
    if navigation == Navigation::IdleOnly {
        argv.push("--idle-only".into());
    }
    argv
}

pub fn source_pane_id(focused: PaneId, last_terminal: Option<u32>) -> Result<String, String> {
    match focused {
        PaneId::Terminal(id) => Ok(format!("terminal_{id}")),
        PaneId::Plugin(_) => last_terminal
            .map(|id| format!("terminal_{id}"))
            .ok_or_else(|| "a terminal pane must be focused first".into()),
    }
}

pub fn focused_terminal_in_tab(tab_index: usize, pane_manifest: &PaneManifest) -> Option<u32> {
    get_focused_pane(tab_index, pane_manifest).map(|pane| pane.id)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use zellij_tile::prelude::{PaneId, PaneInfo, PaneManifest};

    #[test]
    fn parses_supported_navigation_messages() {
        assert_eq!(
            parse_navigation("agent-next", Some("all")),
            Ok(Navigation::All)
        );
        assert_eq!(
            parse_navigation("agent-next", Some("idle-only")),
            Ok(Navigation::IdleOnly)
        );
    }

    #[test]
    fn rejects_unsupported_navigation_messages() {
        assert!(parse_navigation("other", Some("all")).is_err());
    }

    #[test]
    fn builds_expected_agent_next_command() {
        assert_eq!(
            command_argv("/opt/zellij-agent", Navigation::All),
            vec!["/opt/zellij-agent", "agent", "next"]
        );
        assert_eq!(
            command_argv("/opt/zellij-agent", Navigation::IdleOnly),
            vec!["/opt/zellij-agent", "agent", "next", "--idle-only"]
        );
    }

    #[test]
    fn resolves_terminal_source_panes() {
        assert_eq!(
            source_pane_id(PaneId::Terminal(7), None),
            Ok("terminal_7".into())
        );
        assert_eq!(
            source_pane_id(PaneId::Plugin(2), Some(7)),
            Ok("terminal_7".into())
        );
        assert!(source_pane_id(PaneId::Plugin(2), None).is_err());
    }

    #[test]
    fn resolves_plugin_focus_from_remembered_terminal() {
        let mut model = BridgeModel::default();
        model.set_last_terminal(Some(7));

        assert_eq!(
            model.resolve_source_pane(PaneId::Plugin(2)),
            Ok("terminal_7".into())
        );
    }

    #[test]
    fn focused_terminal_is_scoped_to_current_tab() {
        let pane_manifest = PaneManifest {
            panes: HashMap::from([
                (
                    0,
                    vec![PaneInfo {
                        id: 7,
                        is_focused: true,
                        ..Default::default()
                    }],
                ),
                (
                    1,
                    vec![PaneInfo {
                        id: 9,
                        is_plugin: true,
                        is_focused: true,
                        ..Default::default()
                    }],
                ),
            ]),
        };

        assert_eq!(focused_terminal_in_tab(1, &pane_manifest), None);
        assert_eq!(focused_terminal_in_tab(0, &pane_manifest), Some(7));
    }

    #[test]
    fn permission_before_session_releases_queued_work() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.queue(Navigation::All);
        model.set_permission(true);
        assert!(model.take_ready().is_empty());
        model.set_session_name("work");

        assert_eq!(
            model.take_ready(),
            vec![ReadyJob {
                executable: "/opt/zellij-agent".into(),
                session_name: "work".into(),
                navigation: Navigation::All,
            }]
        );
    }

    #[test]
    fn session_before_permission_releases_queued_work() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.queue(Navigation::IdleOnly);
        model.set_session_name("work");
        assert!(model.take_ready().is_empty());
        model.set_permission(true);

        assert_eq!(
            model.take_ready(),
            vec![ReadyJob {
                executable: "/opt/zellij-agent".into(),
                session_name: "work".into(),
                navigation: Navigation::IdleOnly,
            }]
        );
    }

    #[test]
    fn drains_ready_work_only_once() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.set_permission(true);
        model.set_session_name("work");
        model.queue(Navigation::All);

        assert_eq!(model.take_ready().len(), 1);
        assert!(model.take_ready().is_empty());
    }

    #[test]
    fn discards_queued_requests_when_permission_is_denied() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.queue(Navigation::All);
        model.set_permission(false);
        model.set_permission(true);
        model.set_session_name("work");

        assert!(model.take_ready().is_empty());
    }

    #[test]
    fn ignores_whitespace_only_executable_configuration() {
        let mut model = BridgeModel::new(Some(" \t\n ".into()));
        model.set_permission(true);
        model.set_session_name("work");
        model.queue(Navigation::All);

        assert!(model.take_ready().is_empty());
    }

    #[test]
    fn preserves_two_consecutive_keypresses_as_two_jobs() {
        let mut model = BridgeModel::new(Some("  /opt/zellij-agent  ".into()));
        model.set_permission(true);
        model.set_session_name("  work  ");
        model.queue(Navigation::All);
        model.queue(Navigation::All);

        assert_eq!(
            model.take_ready(),
            vec![
                ReadyJob {
                    executable: "/opt/zellij-agent".into(),
                    session_name: "work".into(),
                    navigation: Navigation::All,
                },
                ReadyJob {
                    executable: "/opt/zellij-agent".into(),
                    session_name: "work".into(),
                    navigation: Navigation::All,
                },
            ]
        );
    }
}
