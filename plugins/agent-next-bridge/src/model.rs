use std::collections::VecDeque;
use zellij_tile::prelude::{get_focused_pane, PaneId, PaneManifest, SessionInfo};

const MAX_QUEUED_REQUESTS: usize = 32;

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
    queue: VecDeque<QueuedRequest>,
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
            queue: VecDeque::new(),
        }
    }

    pub fn queue(&mut self, navigation: Navigation) -> bool {
        if self.executable.is_none() || self.queue.len() >= MAX_QUEUED_REQUESTS {
            return false;
        }
        self.queue.push_back(QueuedRequest { navigation });
        true
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

    pub fn initialize_focused_pane(&mut self, focused: Option<PaneId>) {
        if let Some(PaneId::Terminal(pane_id)) = focused {
            self.set_last_terminal(Some(pane_id));
        }
    }

    pub fn set_current_session(&mut self, sessions: &[SessionInfo]) {
        if let Some(session) = sessions.iter().find(|session| session.is_current_session) {
            self.set_session_name(&session.name);
        }
    }

    pub fn resolve_source_pane(&self, focused: PaneId) -> Result<String, String> {
        source_pane_id(focused, self.last_terminal)
    }

    pub fn next_ready(&self) -> Option<ReadyJob> {
        let Some(executable) = self.executable.as_ref() else {
            return None;
        };
        if self.permission != Permission::Granted || self.session_name.is_empty() {
            return None;
        }

        self.queue.front().map(|request| ReadyJob {
            executable: executable.clone(),
            session_name: self.session_name.clone(),
            navigation: request.navigation,
        })
    }

    pub fn complete_ready(&mut self) {
        self.queue.pop_front();
    }

    #[cfg(test)]
    fn take_ready(&mut self) -> Vec<ReadyJob> {
        let mut jobs = Vec::new();
        while let Some(job) = self.next_ready() {
            jobs.push(job);
            self.complete_ready();
        }
        jobs
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
    use zellij_tile::prelude::{PaneId, PaneInfo, PaneManifest, SessionInfo};

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
    fn startup_context_remembers_the_initial_terminal_pane() {
        let mut model = BridgeModel::default();
        model.initialize_focused_pane(Some(PaneId::Terminal(7)));

        assert_eq!(
            model.resolve_source_pane(PaneId::Plugin(2)),
            Ok("terminal_7".into())
        );
    }

    #[test]
    fn first_session_update_releases_navigation_without_inherited_zellij_environment() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.queue(Navigation::All);
        model.set_permission(true);

        model.set_current_session(&[
            SessionInfo {
                name: "other".into(),
                is_current_session: false,
                ..Default::default()
            },
            SessionInfo {
                name: "fresh".into(),
                is_current_session: true,
                ..Default::default()
            },
        ]);

        assert_eq!(
            model.take_ready(),
            vec![ReadyJob {
                executable: "/opt/zellij-agent".into(),
                session_name: "fresh".into(),
                navigation: Navigation::All,
            }]
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
    fn keeps_ready_work_until_completion_is_acknowledged() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.set_permission(true);
        model.set_session_name("work");
        model.queue(Navigation::All);

        let first = model.next_ready();
        assert_eq!(model.next_ready(), first);

        model.complete_ready();
        assert!(model.next_ready().is_none());
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

    #[test]
    fn bounds_queued_navigation_requests() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.set_permission(true);
        model.set_session_name("work");

        for _ in 0..MAX_QUEUED_REQUESTS {
            assert!(model.queue(Navigation::All));
        }
        assert!(!model.queue(Navigation::All));
        assert_eq!(model.take_ready().len(), MAX_QUEUED_REQUESTS);
    }
}
