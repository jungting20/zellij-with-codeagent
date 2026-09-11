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
