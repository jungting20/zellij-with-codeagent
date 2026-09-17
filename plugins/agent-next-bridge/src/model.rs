pub fn command_argv(executable: &str, payload: Option<&str>) -> Option<Vec<String>> {
    let flags: &[&str] = match payload.unwrap_or("all") {
        "all" => &[],
        "idle-only" => &["--idle-only"],
        "pinned-only" => &["--pinned-only"],
        "idle-and-pinned" => &["--pinned-only", "--idle-only"],
        "unpinned-only" => &["--unpinned-only"],
        "idle-and-unpinned" => &["--unpinned-only", "--idle-only"],
        _ => return None,
    };
    let mut argv = vec![executable.into(), "agent".into(), "next".into()];
    argv.extend(flags.iter().map(|flag| (*flag).into()));
    Some(argv)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn disconnected_and_extra_client_instances_do_not_duplicate_navigation() {
        for connected in [vec![2], vec![2, 3], vec![3, 2], vec![]] {
            let mut executions = 0;
            for own_client_id in [1, 2, 3] {
                let mut queue = NavigationQueue::default();
                queue.push(command_argv("agent", Some("idle-and-pinned")).unwrap());
                assert!(queue.needs_client_check());
                executions += queue.resolve(own_client_id, &connected).len();
                assert!(queue.resolve(own_client_id, &connected).is_empty());
            }
            assert_eq!(executions, usize::from(!connected.is_empty()));
        }
    }

    #[test]
    fn client_check_preserves_distinct_keypresses_and_discards_stale_requests() {
        let mut queue = NavigationQueue::default();
        let first = command_argv("agent", Some("pinned-only")).unwrap();
        let second = command_argv("agent", Some("idle-and-pinned")).unwrap();
        queue.push(first.clone());
        assert!(queue.needs_client_check());
        queue.push(second.clone());
        assert!(!queue.needs_client_check());
        assert_eq!(queue.resolve(2, &[2]), vec![first.clone(), second]);
        queue.push(first.clone());
        assert!(queue.needs_client_check());
        assert!(queue.resolve(2, &[3]).is_empty());
        assert!(!queue.needs_client_check());
        queue.push(first.clone());
        assert!(queue.needs_client_check());
        assert_eq!(queue.resolve(2, &[2]), vec![first]);
    }

    #[test]
    fn maps_navigation_payloads_to_cli_filters() {
        for (payload, flags) in [
            (None, vec![]),
            (Some("all"), vec![]),
            (Some("idle-only"), vec!["--idle-only"]),
            (Some("pinned-only"), vec!["--pinned-only"]),
            (
                Some("idle-and-pinned"),
                vec!["--pinned-only", "--idle-only"],
            ),
            (Some("unpinned-only"), vec!["--unpinned-only"]),
            (
                Some("idle-and-unpinned"),
                vec!["--unpinned-only", "--idle-only"],
            ),
        ] {
            let mut expected = vec!["/opt/zellij-agent", "agent", "next"];
            expected.extend(flags);
            assert_eq!(
                command_argv("/opt/zellij-agent", payload).unwrap(),
                expected
            );
        }
    }

    #[test]
    fn unknown_payload_does_not_navigate_without_filters() {
        assert_eq!(command_argv("/opt/zellij-agent", Some("invalid")), None);
    }
}
use std::collections::VecDeque;

#[derive(Default)]
pub struct NavigationQueue {
    pending: VecDeque<Vec<String>>,
    checking_clients: bool,
}

impl NavigationQueue {
    pub fn push(&mut self, argv: Vec<String>) {
        self.pending.push_back(argv);
    }

    pub fn needs_client_check(&mut self) -> bool {
        if self.pending.is_empty() || self.checking_clients {
            return false;
        }
        self.checking_clients = true;
        true
    }

    pub fn resolve(&mut self, own_client_id: u16, connected: &[u16]) -> Vec<Vec<String>> {
        self.checking_clients = false;
        // Zellij retains plugin instances for disconnected clients and sends
        // MessagePlugin to all of them. Elect one connected instance per request.
        let pending = self.pending.drain(..);
        if connected.iter().min() == Some(&own_client_id) {
            pending.collect()
        } else {
            drop(pending);
            Vec::new()
        }
    }
}
