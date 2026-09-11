package agentdashboard

import (
	"strings"
	"zellij-with-codeagent/internal/transport"
)

// Keep existing panel/session/tab groups, then order each group depth first.
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
		if ok && p != i && sameHierarchyGroup(row, original[p]) {
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

func sameHierarchyGroup(a, b transport.AgentWithPane) bool {
	return a.Agent.Pinned == b.Agent.Pinned && sessionName(a) == sessionName(b) && tabKey(a) == tabKey(b)
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
	if !ok || !sameHierarchyGroup(row, parent) {
		return "↳ [" + row.Pane.ParentPaneID + "] "
	}
	depth := 1
	seen := map[string]bool{row.Pane.ID: true}
	for ok && !seen[parent.Pane.ID] {
		seen[parent.Pane.ID] = true
		next, found := byPane[parent.Pane.ParentPaneID]
		if !found || !sameHierarchyGroup(row, next) {
			break
		}
		depth++
		parent, ok = next, found
	}
	return strings.Repeat("  ", minInt(depth, 4)) + "↳ "
}
