package dashboard

import (
	"sort"
	"strings"

	"zellij-with-codeagent/internal/transport"
)

const ungrouped = "ungrouped"

type treeNode struct {
	kind     string
	key      string
	label    string
	pane     *transport.Pane
	children []*treeNode
}

type treeRow struct {
	node  *treeNode
	depth int
}

func buildTree(panes []transport.Pane) []*treeNode {
	var tree []*treeNode
	for _, source := range panes {
		pane := source
		sessionID := groupID(pane.SessionID)
		taskID := groupID(pane.TaskID)
		tabID := groupID(pane.TabID)

		sessionKey := "session\x00" + sessionID
		session := findOrAppend(&tree, sessionKey, "session", sessionID)
		taskKey := sessionKey + "\x00task\x00" + taskID
		task := findOrAppend(&session.children, taskKey, "task", taskID)
		tabKey := taskKey + "\x00tab\x00" + tabID
		tab := findOrAppend(&task.children, tabKey, "tab", tabLabel(pane.TabName, tabID))
		tab.children = append(tab.children, &treeNode{
			kind:  "pane",
			key:   tabKey + "\x00pane\x00" + pane.ID,
			label: pane.ID,
			pane:  &pane,
		})
	}

	sortTree(tree)
	return tree
}

func groupID(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return ungrouped
}

func tabLabel(name, id string) string {
	name = strings.TrimSpace(name)
	if name == "" || id == ungrouped {
		return id
	}
	return name + " (" + id + ")"
}

func findOrAppend(nodes *[]*treeNode, key, kind, label string) *treeNode {
	for _, node := range *nodes {
		if node.key == key {
			return node
		}
	}
	node := &treeNode{kind: kind, key: key, label: label}
	*nodes = append(*nodes, node)
	return node
}

func sortTree(nodes []*treeNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].label == nodes[j].label {
			return nodes[i].key < nodes[j].key
		}
		return nodes[i].label < nodes[j].label
	})
	for _, node := range nodes {
		sortTree(node.children)
	}
}

func defaultExpanded(tree []*treeNode) map[string]bool {
	expanded := make(map[string]bool)
	var walk func([]*treeNode)
	walk = func(nodes []*treeNode) {
		for _, node := range nodes {
			if node.kind != "pane" {
				expanded[node.key] = true
				walk(node.children)
			}
		}
	}
	walk(tree)
	return expanded
}

func flattenTree(tree []*treeNode, expanded map[string]bool) []treeRow {
	var rows []treeRow
	var walk func([]*treeNode, int)
	walk = func(nodes []*treeNode, depth int) {
		for _, node := range nodes {
			rows = append(rows, treeRow{node: node, depth: depth})
			if node.kind != "pane" && expanded[node.key] {
				walk(node.children, depth+1)
			}
		}
	}
	walk(tree, 0)
	return rows
}
