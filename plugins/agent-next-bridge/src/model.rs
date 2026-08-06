use zellij_tile::prelude::PaneId;

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
    executable: String,
    navigation: Navigation,
}

pub struct BridgeModel {
    permission: Permission,
    session_name: String,
    last_terminal: Option<u32>,
    queue: Vec<QueuedRequest>,
}

impl Default for BridgeModel {
    fn default() -> Self {
        Self {
            permission: Permission::Pending,
            session_name: String::new(),
            last_terminal: None,
            queue: Vec::new(),
        }
    }
}

impl BridgeModel {
    pub fn queue(&mut self, executable: impl AsRef<str>, navigation: Navigation) {
        let executable = executable.as_ref().trim();
        if executable.is_empty() {
            return;
        }
        self.queue.push(QueuedRequest {
            executable: executable.into(),
            navigation,
        });
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
        self.session_name = session_name.as_ref().into();
    }

    pub fn set_last_terminal(&mut self, last_terminal: Option<u32>) {
        self.last_terminal = last_terminal;
    }

    pub fn take_ready(&mut self) -> Vec<ReadyJob> {
        let session_name = self.session_name.trim();
        if self.permission != Permission::Granted || session_name.is_empty() {
            return Vec::new();
        }

        let session_name = session_name.to_owned();
        self.queue
            .drain(..)
            .map(|request| ReadyJob {
                executable: request.executable,
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

pub fn command_argv(executable: &str, navigation: Navigation) -> Vec<&str> {
    let mut argv = vec![executable, "agent", "next"];
    if navigation == Navigation::IdleOnly {
        argv.push("--idle-only");
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

#[cfg(test)]
mod tests {
    use super::*;
    use zellij_tile::prelude::PaneId;

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
    fn queues_requests_until_ready_then_drains_each_once() {
        let mut model = BridgeModel::default();
        model.queue("/opt/zellij-agent", Navigation::All);
        model.queue("/opt/zellij-agent", Navigation::IdleOnly);

        assert!(model.take_ready().is_empty());

        model.set_permission(true);
        assert!(model.take_ready().is_empty());

        model.set_session_name("  work  ");
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
                    navigation: Navigation::IdleOnly,
                },
            ]
        );
        assert!(model.take_ready().is_empty());
    }

    #[test]
    fn discards_queued_requests_when_permission_is_denied() {
        let mut model = BridgeModel::default();
        model.queue("/opt/zellij-agent", Navigation::All);
        model.set_permission(false);
        model.set_permission(true);
        model.set_session_name("work");

        assert!(model.take_ready().is_empty());
    }
}
