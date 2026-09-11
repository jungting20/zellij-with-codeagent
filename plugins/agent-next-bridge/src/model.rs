use std::collections::VecDeque;

const MAX_QUEUED_REQUESTS: usize = 32;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Navigation {
    Next(NavigationFilter),
    Previous(NavigationFilter),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum NavigationFilter {
    All,
    IdleOnly,
    PinnedOnly,
    IdleAndPinned,
    UnpinnedOnly,
    IdleAndUnpinned,
}

#[derive(Debug, Eq, PartialEq)]
pub struct ReadyJob {
    pub executable: String,
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

    pub fn next_ready(&self) -> Option<ReadyJob> {
        let Some(executable) = self.executable.as_ref() else {
            return None;
        };
        if self.permission != Permission::Granted {
            return None;
        }

        self.queue.front().map(|request| ReadyJob {
            executable: executable.clone(),
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
        ("agent-next", Some("all")) => Ok(Navigation::Next(NavigationFilter::All)),
        ("agent-next", Some("idle-only")) => Ok(Navigation::Next(NavigationFilter::IdleOnly)),
        ("agent-next", Some("pinned-only")) => Ok(Navigation::Next(NavigationFilter::PinnedOnly)),
        ("agent-next", Some("idle-and-pinned")) => {
            Ok(Navigation::Next(NavigationFilter::IdleAndPinned))
        }
        ("agent-next", Some("unpinned-only")) => {
            Ok(Navigation::Next(NavigationFilter::UnpinnedOnly))
        }
        ("agent-next", Some("idle-and-unpinned")) => {
            Ok(Navigation::Next(NavigationFilter::IdleAndUnpinned))
        }
        ("agent-prev", Some("unpinned-only")) => {
            Ok(Navigation::Previous(NavigationFilter::UnpinnedOnly))
        }
        ("agent-prev", Some("idle-and-unpinned")) => {
            Ok(Navigation::Previous(NavigationFilter::IdleAndUnpinned))
        }
        ("agent-prev", Some("all")) => Ok(Navigation::Previous(NavigationFilter::All)),
        ("agent-prev", Some("idle-only")) => Ok(Navigation::Previous(NavigationFilter::IdleOnly)),
        ("agent-prev", Some("pinned-only")) => {
            Ok(Navigation::Previous(NavigationFilter::PinnedOnly))
        }
        ("agent-prev", Some("idle-and-pinned")) => {
            Ok(Navigation::Previous(NavigationFilter::IdleAndPinned))
        }
        _ => Err(format!(
            "unsupported pipe message name={name:?} payload={payload:?}"
        )),
    }
}

pub fn command_argv(executable: &str, navigation: Navigation) -> Vec<String> {
    let (direction, filter) = match navigation {
        Navigation::Next(filter) => ("next", filter),
        Navigation::Previous(filter) => ("prev", filter),
    };
    let mut argv = vec![executable.into(), "agent".into(), direction.into()];
    match filter {
        NavigationFilter::UnpinnedOnly => argv.push("--unpinned-only".into()),
        NavigationFilter::IdleAndUnpinned => {
            argv.push("--idle-only".into());
            argv.push("--unpinned-only".into());
        }
        NavigationFilter::All => {}
        NavigationFilter::IdleOnly => argv.push("--idle-only".into()),
        NavigationFilter::PinnedOnly => argv.push("--pinned-only".into()),
        NavigationFilter::IdleAndPinned => {
            argv.push("--idle-only".into());
            argv.push("--pinned-only".into());
        }
    }
    argv
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_supported_navigation_messages() {
        assert_eq!(
            parse_navigation("agent-next", Some("all")),
            Ok(Navigation::Next(NavigationFilter::All))
        );
        assert_eq!(
            parse_navigation("agent-next", Some("idle-only")),
            Ok(Navigation::Next(NavigationFilter::IdleOnly))
        );
        assert_eq!(
            parse_navigation("agent-next", Some("pinned-only")),
            Ok(Navigation::Next(NavigationFilter::PinnedOnly))
        );
        assert_eq!(
            parse_navigation("agent-next", Some("idle-and-pinned")),
            Ok(Navigation::Next(NavigationFilter::IdleAndPinned))
        );
        assert_eq!(
            parse_navigation("agent-prev", Some("all")),
            Ok(Navigation::Previous(NavigationFilter::All))
        );
        assert_eq!(
            parse_navigation("agent-prev", Some("idle-only")),
            Ok(Navigation::Previous(NavigationFilter::IdleOnly))
        );
        assert_eq!(
            parse_navigation("agent-prev", Some("pinned-only")),
            Ok(Navigation::Previous(NavigationFilter::PinnedOnly))
        );
        assert_eq!(
            parse_navigation("agent-prev", Some("idle-and-pinned")),
            Ok(Navigation::Previous(NavigationFilter::IdleAndPinned))
        );
    }

    #[test]
    fn unpinned_payloads_build_expected_commands() {
        for (name, direction) in [("agent-next", "next"), ("agent-prev", "prev")] {
            for (payload, flags) in [
                ("unpinned-only", vec!["--unpinned-only"]),
                ("idle-and-unpinned", vec!["--idle-only", "--unpinned-only"]),
            ] {
                let navigation = parse_navigation(name, Some(payload)).unwrap();
                let mut expected = vec!["/opt/zellij-agent", "agent", direction];
                expected.extend(flags);
                assert_eq!(command_argv("/opt/zellij-agent", navigation), expected);
            }
        }
    }

    #[test]
    fn rejects_unsupported_navigation_messages() {
        assert!(parse_navigation("other", Some("all")).is_err());
    }

    #[test]
    fn builds_expected_agent_navigation_commands() {
        assert_eq!(
            command_argv("/opt/zellij-agent", Navigation::Next(NavigationFilter::All)),
            vec!["/opt/zellij-agent", "agent", "next"]
        );
        assert_eq!(
            command_argv(
                "/opt/zellij-agent",
                Navigation::Next(NavigationFilter::IdleOnly)
            ),
            vec!["/opt/zellij-agent", "agent", "next", "--idle-only"]
        );
        assert_eq!(
            command_argv(
                "/opt/zellij-agent",
                Navigation::Next(NavigationFilter::PinnedOnly)
            ),
            vec!["/opt/zellij-agent", "agent", "next", "--pinned-only"]
        );
        assert_eq!(
            command_argv(
                "/opt/zellij-agent",
                Navigation::Next(NavigationFilter::IdleAndPinned)
            ),
            vec![
                "/opt/zellij-agent",
                "agent",
                "next",
                "--idle-only",
                "--pinned-only"
            ]
        );
        assert_eq!(
            command_argv(
                "/opt/zellij-agent",
                Navigation::Previous(NavigationFilter::All)
            ),
            vec!["/opt/zellij-agent", "agent", "prev"]
        );
        assert_eq!(
            command_argv(
                "/opt/zellij-agent",
                Navigation::Previous(NavigationFilter::IdleOnly)
            ),
            vec!["/opt/zellij-agent", "agent", "prev", "--idle-only"]
        );
        assert_eq!(
            command_argv(
                "/opt/zellij-agent",
                Navigation::Previous(NavigationFilter::PinnedOnly)
            ),
            vec!["/opt/zellij-agent", "agent", "prev", "--pinned-only"]
        );
        assert_eq!(
            command_argv(
                "/opt/zellij-agent",
                Navigation::Previous(NavigationFilter::IdleAndPinned)
            ),
            vec![
                "/opt/zellij-agent",
                "agent",
                "prev",
                "--idle-only",
                "--pinned-only"
            ]
        );
    }

    #[test]
    fn permission_releases_six_keys_without_focus_or_completion_events() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        for _ in 0..6 {
            assert!(model.queue(Navigation::Next(NavigationFilter::PinnedOnly)));
        }
        assert!(model.next_ready().is_none());
        model.set_permission(true);
        let jobs = model.take_ready();
        assert_eq!(jobs.len(), 6);
        for job in jobs {
            assert_eq!(
                command_argv(&job.executable, job.navigation),
                ["/opt/zellij-agent", "agent", "next", "--pinned-only"]
            );
        }
        assert!(model.next_ready().is_none());
    }

    #[test]
    fn drains_ready_work_only_once() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.set_permission(true);
        model.queue(Navigation::Next(NavigationFilter::All));

        assert_eq!(model.take_ready().len(), 1);
        assert!(model.take_ready().is_empty());
    }

    #[test]
    fn keeps_ready_work_until_dispatched() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.set_permission(true);
        model.queue(Navigation::Next(NavigationFilter::All));

        let first = model.next_ready();
        assert_eq!(model.next_ready(), first);

        model.complete_ready();
        assert!(model.next_ready().is_none());
    }

    #[test]
    fn discards_queued_requests_when_permission_is_denied() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.queue(Navigation::Next(NavigationFilter::All));
        model.set_permission(false);
        model.set_permission(true);

        assert!(model.take_ready().is_empty());
    }

    #[test]
    fn ignores_whitespace_only_executable_configuration() {
        let mut model = BridgeModel::new(Some(" \t\n ".into()));
        model.set_permission(true);
        model.queue(Navigation::Next(NavigationFilter::All));

        assert!(model.take_ready().is_empty());
    }

    #[test]
    fn preserves_two_consecutive_keypresses_as_two_jobs() {
        let mut model = BridgeModel::new(Some("  /opt/zellij-agent  ".into()));
        model.set_permission(true);
        model.queue(Navigation::Next(NavigationFilter::All));
        model.queue(Navigation::Previous(NavigationFilter::PinnedOnly));

        assert_eq!(
            model.take_ready(),
            vec![
                ReadyJob {
                    executable: "/opt/zellij-agent".into(),
                    navigation: Navigation::Next(NavigationFilter::All),
                },
                ReadyJob {
                    executable: "/opt/zellij-agent".into(),
                    navigation: Navigation::Previous(NavigationFilter::PinnedOnly),
                },
            ]
        );
    }

    #[test]
    fn bounds_queued_navigation_requests() {
        let mut model = BridgeModel::new(Some("/opt/zellij-agent".into()));
        model.set_permission(true);

        for _ in 0..MAX_QUEUED_REQUESTS {
            assert!(model.queue(Navigation::Next(NavigationFilter::All)));
        }
        assert!(!model.queue(Navigation::Next(NavigationFilter::All)));
        assert_eq!(model.take_ready().len(), MAX_QUEUED_REQUESTS);
    }
}
