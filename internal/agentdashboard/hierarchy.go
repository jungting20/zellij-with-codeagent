package agentdashboard

import (
	"strings"
	"zellij-with-codeagent/internal/transport"
)

// Keep root ordering, then place descendants directly after their parent across sessions.
func orderChildren(rows []transport.AgentWithPane) {
	original := append([]transport.AgentWithPane(nil), rows...)
	byPane := make(map[string]int)
	for i, row := range original {
		if row.Pane.ID != "" {
			byPane[row.Pane.ID] = i
		}
	}
	children := make(map[int][]int)
	roots := []int{}
	for i, row := range original {
		p, ok := byPane[row.Pane.ParentPaneID]
		if ok && p != i {
			children[p] = append(children[p], i)
		} else {
			roots = append(roots, i)
		}
	}
	visited := make(map[int]bool)
	out := rows[:0]
	var visit func(int)
	visit = func(i int) {
		if visited[i] {
			return
		}
		visited[i] = true
		out = append(out, original[i])
		for _, child := range children[i] {
			visit(child)
		}
	}
	for _, root := range roots {
		visit(root)
	}
	for i := range original {
		visit(i)
	} // Defensively render malformed cycles once.
}

// hierarchyRoot supplies the displayed panel/session/tab without changing runtime data.
func (m Model) hierarchyRoot(row transport.AgentWithPane) transport.AgentWithPane {
	byPane := make(map[string]transport.AgentWithPane)
	for _, item := range m.rows {
		if item.Pane.ID != "" {
			byPane[item.Pane.ID] = item
		}
	}
	seen := make(map[string]bool)
	for row.Pane.ParentPaneID != "" {
		if seen[row.Pane.ID] {
			for id := range seen {
				if id < row.Pane.ID {
					row = byPane[id]
				}
			}
			return row
		}
		seen[row.Pane.ID] = true
		parent, ok := byPane[row.Pane.ParentPaneID]
		if !ok {
			break
		}
		row = parent
	}
	return row
}

func (m Model) childLabel(row transport.AgentWithPane) string {
	if row.Pane.ParentPaneID == "" {
		return ""
	}
	byPane := make(map[string]transport.AgentWithPane)
	for _, item := range m.rows {
		byPane[item.Pane.ID] = item
	}
	parent, ok := byPane[row.Pane.ParentPaneID]
	if !ok {
		return "↳ [" + row.Pane.ParentPaneID + "] "
	}
	depth := 1
	seen := map[string]bool{row.Pane.ID: true}
	for ok && !seen[parent.Pane.ID] {
		seen[parent.Pane.ID] = true
		next, found := byPane[parent.Pane.ParentPaneID]
		if !found {
			break
		}
		depth++
		parent, ok = next, found
	}
	return strings.Repeat("  ", minInt(depth, 4)) + "↳ "
}
