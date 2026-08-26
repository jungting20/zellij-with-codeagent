use serde::Deserialize;
use std::path::Path;
use unicode_width::{UnicodeWidthChar, UnicodeWidthStr};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AgentRow {
    pub id: String,
    pub kind: String,
    pub state: String,
    pub project: String,
}

#[derive(Default)]
pub struct SidebarModel {
    rows: Vec<AgentRow>,
    selected: usize,
    interactive: bool,
    status: String,
}

#[derive(Deserialize)]
struct ListResponse {
    #[serde(default)]
    agents: Vec<AgentWithPane>,
}

#[derive(Deserialize)]
struct AgentWithPane {
    agent: Agent,
    pane: Pane,
}

#[derive(Deserialize)]
struct Agent {
    #[serde(default)]
    id: String,
    #[serde(default)]
    kind: String,
    #[serde(default)]
    state: String,
}

#[derive(Deserialize)]
struct Pane {
    #[serde(default)]
    cwd: String,
}

impl SidebarModel {
    pub fn replace_from_json(&mut self, input: &[u8]) -> Result<(), String> {
        let response: ListResponse =
            serde_json::from_slice(input).map_err(|error| error.to_string())?;
        let selected_id = self.selected_id().map(str::to_owned);
        self.rows = response
            .agents
            .into_iter()
            .filter(|entry| !entry.agent.id.trim().is_empty())
            .map(|entry| AgentRow {
                id: entry.agent.id,
                kind: value_or_dash(entry.agent.kind),
                state: value_or_dash(entry.agent.state),
                project: project_name(&entry.pane.cwd),
            })
            .collect();
        self.rows.sort_by(|left, right| {
            left.project
                .cmp(&right.project)
                .then(left.id.cmp(&right.id))
        });
        self.selected = selected_id
            .and_then(|id| self.rows.iter().position(|row| row.id == id))
            .unwrap_or(0)
            .min(self.rows.len().saturating_sub(1));
        self.status = format!("{} agents", self.rows.len());
        Ok(())
    }

    pub fn rows(&self) -> &[AgentRow] {
        &self.rows
    }

    pub fn selected(&self) -> usize {
        self.selected
    }

    pub fn selected_id(&self) -> Option<&str> {
        self.rows.get(self.selected).map(|row| row.id.as_str())
    }

    pub fn move_up(&mut self) {
        self.selected = self.selected.saturating_sub(1);
    }

    pub fn move_down(&mut self) {
        if self.selected + 1 < self.rows.len() {
            self.selected += 1;
        }
    }

    pub fn toggle_interactive(&mut self) -> bool {
        self.interactive = !self.interactive;
        self.interactive
    }

    pub fn deactivate(&mut self) {
        self.interactive = false;
    }

    pub fn interactive(&self) -> bool {
        self.interactive
    }

    pub fn status(&self) -> &str {
        &self.status
    }

    pub fn set_status(&mut self, status: impl Into<String>) {
        self.status = status.into();
    }
}

pub fn list_command(executable: &str) -> Vec<String> {
    vec![
        executable.into(),
        "agent".into(),
        "dashboard".into(),
        "--output".into(),
        "json".into(),
        "--timeout".into(),
        "3s".into(),
    ]
}

pub fn focus_command(executable: &str, agent_id: &str) -> Vec<String> {
    let mut command = list_command(executable);
    command.extend(["--focus".into(), agent_id.into()]);
    command
}

pub fn row_text(row: &AgentRow, width: usize) -> String {
    truncate_display(
        &format!("{}  {}  {}", row.project, row.kind, row.state),
        width,
    )
}

pub fn truncate_display(value: &str, width: usize) -> String {
    if value.width() <= width {
        return value.to_owned();
    }
    if width <= 1 {
        return "…".chars().take(width).collect();
    }
    let mut output = String::new();
    let mut used = 0;
    for character in value.chars() {
        let character_width = character.width().unwrap_or(0);
        if used + character_width + 1 > width {
            break;
        }
        output.push(character);
        used += character_width;
    }
    output.push('…');
    output
}

fn value_or_dash(value: String) -> String {
    let value = value.trim();
    if value.is_empty() {
        "-".into()
    } else {
        value.into()
    }
}

fn project_name(cwd: &str) -> String {
    Path::new(cwd.trim())
        .file_name()
        .and_then(|name| name.to_str())
        .filter(|name| !name.is_empty())
        .unwrap_or("-")
        .to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_dashboard_json_and_preserves_selection() {
        let mut model = SidebarModel::default();
        model
            .replace_from_json(br#"{"agents":[{"agent":{"id":"agent-2","kind":"claude","state":"busy"},"pane":{"cwd":"/tmp/zeta"}},{"agent":{"id":"agent-1","kind":"codex","state":"idle"},"pane":{"cwd":"/tmp/alpha"}}]}"#)
            .unwrap();
        assert_eq!(model.rows()[0].id, "agent-1");
        model.move_down();
        assert_eq!(model.selected_id(), Some("agent-2"));
        model
            .replace_from_json(br#"{"agents":[{"agent":{"id":"agent-3","kind":"gemini","state":"idle"},"pane":{"cwd":"/tmp/beta"}},{"agent":{"id":"agent-2","kind":"claude","state":"idle"},"pane":{"cwd":"/tmp/zeta"}}]}"#)
            .unwrap();
        assert_eq!(model.selected_id(), Some("agent-2"));
    }

    #[test]
    fn toggles_interactive_state() {
        let mut model = SidebarModel::default();
        assert!(model.toggle_interactive());
        assert!(!model.toggle_interactive());
    }

    #[test]
    fn builds_public_cli_commands() {
        assert_eq!(
            list_command("/opt/zellij-agent"),
            vec![
                "/opt/zellij-agent",
                "agent",
                "dashboard",
                "--output",
                "json",
                "--timeout",
                "3s"
            ]
        );
        assert_eq!(
            focus_command("za", "agent-7").last().map(String::as_str),
            Some("agent-7")
        );
    }

    #[test]
    fn truncates_by_display_width() {
        assert_eq!(truncate_display("가나다라마", 5), "가나…");
        assert_eq!(truncate_display("agent", 5), "agent");
    }
}
