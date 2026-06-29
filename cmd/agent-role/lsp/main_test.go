package lsp

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintTreeOmitsFilePathForNodesInCurrentFile(t *testing.T) {
	analysis := &fileAnalysis{
		path: "/workspace/src/Page.tsx",
		declarations: []*declaration{
			{
				name:      "Page",
				kind:      symbolKindFunction,
				component: true,
				calls:     []string{"loadProducts"},
				jsxRoots: []*treeNode{
					{
						name:  "Header",
						props: []propValue{{Name: "title", Value: "Dashboard"}},
						children: []*treeNode{
							{name: "Logo"},
						},
					},
				},
			},
			{name: "loadProducts", kind: symbolKindFunction},
		},
		roots: []*declaration{
			{
				name:      "Page",
				kind:      symbolKindFunction,
				component: true,
				calls:     []string{"loadProducts"},
				jsxRoots: []*treeNode{
					{
						name:  "Header",
						props: []propValue{{Name: "title", Value: "Dashboard"}},
						children: []*treeNode{
							{name: "Logo"},
						},
					},
				},
			},
		},
		imports: map[string]string{},
	}
	analysis.declarations[0] = analysis.roots[0]

	output := captureStdout(t, func() {
		printTree(analysis, &analyzer{}, 1)
	})

	wantLines := []string{
		"Call/component tree: /workspace/src/Page.tsx",
		"Page",
		"|- loadProducts",
		"`- Header [title={Dashboard}]",
		"   `- Logo",
	}
	for _, want := range wantLines {
		if !strings.Contains(output, want) {
			t.Fatalf("printTree output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "[file=/workspace/src/Page.tsx]") {
		t.Fatalf("printTree output repeated current file path:\n%s", output)
	}
}

func TestPrintTreeShowsFilePathOnlyWhenCrossingIntoImportedFile(t *testing.T) {
	importedPath := "/workspace/src/Header.tsx"
	imported := &fileAnalysis{
		path: importedPath,
		declarations: []*declaration{
			{
				name:      "Header",
				kind:      symbolKindFunction,
				component: true,
				calls:     []string{"formatTitle"},
			},
			{name: "formatTitle", kind: symbolKindFunction},
		},
		imports: map[string]string{},
	}
	imported.roots = []*declaration{imported.declarations[0]}

	root := &declaration{
		name:      "Page",
		kind:      symbolKindFunction,
		component: true,
		jsxRoots:  []*treeNode{{name: "Header"}},
	}
	analysis := &fileAnalysis{
		path:         "/workspace/src/Page.tsx",
		declarations: []*declaration{root},
		roots:        []*declaration{root},
		imports:      map[string]string{"Header": importedPath},
	}
	analyzer := &analyzer{cache: map[string]*fileAnalysis{importedPath: imported}}

	output := captureStdout(t, func() {
		printTree(analysis, analyzer, 2)
	})

	wantLines := []string{
		"Page",
		"`- Header [file=/workspace/src/Header.tsx]",
		"   `- formatTitle",
	}
	for _, want := range wantLines {
		if !strings.Contains(output, want) {
			t.Fatalf("printTree output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "formatTitle [file=/workspace/src/Header.tsx]") {
		t.Fatalf("printTree output repeated imported file path inside imported subtree:\n%s", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stdout pipe reader: %v", err)
	}
	return buf.String()
}
