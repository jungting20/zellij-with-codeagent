pub fn command_argv(executable: &str) -> Vec<String> {
    vec![executable.into(), "agent".into(), "next".into()]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn runs_only_agent_next_without_flags_or_source_context() {
        assert_eq!(
            command_argv("/opt/zellij-agent"),
            ["/opt/zellij-agent", "agent", "next"]
        );
    }
}
