package work

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDetectProjectGo(t *testing.T) {
	tests := []struct {
		name    string
		markers []string
	}{
		{name: "module", markers: []string{"go.mod"}},
		{name: "workspace", markers: []string{"go.work"}},
		{name: "workspace and module", markers: []string{"go.mod", "go.work"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeProjectFiles(t, root, map[string]string{})
			for _, marker := range tt.markers {
				writeProjectFiles(t, root, map[string]string{marker: ""})
			}

			got, err := DetectProject(root)
			if err != nil {
				t.Fatalf("DetectProject() error = %v", err)
			}
			want := ProjectDetection{
				Profile:         ProjectProfileGo,
				Markers:         tt.markers,
				TestCommand:     []string{"go", "test", "./..."},
				BuildCommand:    []string{"go", "build", "./..."},
				FeedbackEnabled: true,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("DetectProject() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestDetectProjectRust(t *testing.T) {
	root := t.TempDir()
	writeProjectFiles(t, root, map[string]string{"Cargo.toml": "[package]\nname = \"demo\"\n"})

	got, err := DetectProject(root)
	if err != nil {
		t.Fatalf("DetectProject() error = %v", err)
	}
	want := ProjectDetection{
		Profile:         ProjectProfileRust,
		Markers:         []string{"Cargo.toml"},
		TestCommand:     []string{"cargo", "test"},
		BuildCommand:    []string{"cargo", "check"},
		FeedbackEnabled: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectProject() = %#v, want %#v", got, want)
	}
}

func TestDetectProjectNodeProfiles(t *testing.T) {
	tests := []struct {
		name         string
		files        map[string]string
		wantProfile  ProjectProfile
		wantMarkers  []string
		wantTest     []string
		wantBuild    []string
		wantEnabled  bool
		wantReasonIn string
	}{
		{
			name:        "npm without lockfile",
			files:       map[string]string{"package.json": `{"scripts":{"test":"vitest","build":"vite build"}}`},
			wantProfile: ProjectProfileNPM,
			wantMarkers: []string{"package.json"},
			wantTest:    []string{"npm", "test"},
			wantBuild:   []string{"npm", "run", "build"},
			wantEnabled: true,
		},
		{
			name: "pnpm",
			files: map[string]string{
				"package.json":   `{"scripts":{"test":"vitest","build":"vite build"}}`,
				"pnpm-lock.yaml": "lockfileVersion: '9.0'\n",
			},
			wantProfile: ProjectProfilePNPM,
			wantMarkers: []string{"package.json", "pnpm-lock.yaml"},
			wantTest:    []string{"pnpm", "test"},
			wantBuild:   []string{"pnpm", "build"},
			wantEnabled: true,
		},
		{
			name: "yarn",
			files: map[string]string{
				"package.json": `{"scripts":{"test":"jest","build":"webpack"}}`,
				"yarn.lock":    "# yarn lockfile\n",
			},
			wantProfile: ProjectProfileYarn,
			wantMarkers: []string{"package.json", "yarn.lock"},
			wantTest:    []string{"yarn", "test"},
			wantBuild:   []string{"yarn", "build"},
			wantEnabled: true,
		},
		{
			name:         "missing test script",
			files:        map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`},
			wantProfile:  ProjectProfileNPM,
			wantMarkers:  []string{"package.json"},
			wantBuild:    []string{"npm", "run", "build"},
			wantReasonIn: "--test-command",
		},
		{
			name:         "malformed package json",
			files:        map[string]string{"package.json": `{"scripts":`},
			wantProfile:  ProjectProfileNPM,
			wantMarkers:  []string{"package.json"},
			wantReasonIn: "invalid package.json",
		},
		{
			name: "conflicting managers",
			files: map[string]string{
				"package.json":      `{"scripts":{"test":"vitest"}}`,
				"package-lock.json": "{}",
				"pnpm-lock.yaml":    "lockfileVersion: '9.0'\n",
			},
			wantProfile:  ProjectProfileUnknown,
			wantMarkers:  []string{"package-lock.json", "package.json", "pnpm-lock.yaml"},
			wantReasonIn: "multiple Node package managers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeProjectFiles(t, root, tt.files)

			got, err := DetectProject(root)
			if err != nil {
				t.Fatalf("DetectProject() error = %v", err)
			}
			if got.Profile != tt.wantProfile || !reflect.DeepEqual(got.Markers, tt.wantMarkers) {
				t.Fatalf("DetectProject() profile/markers = %q/%#v, want %q/%#v", got.Profile, got.Markers, tt.wantProfile, tt.wantMarkers)
			}
			if !reflect.DeepEqual(got.TestCommand, tt.wantTest) || !reflect.DeepEqual(got.BuildCommand, tt.wantBuild) {
				t.Fatalf("DetectProject() commands = %#v/%#v, want %#v/%#v", got.TestCommand, got.BuildCommand, tt.wantTest, tt.wantBuild)
			}
			if got.FeedbackEnabled != tt.wantEnabled {
				t.Fatalf("DetectProject() FeedbackEnabled = %v, want %v", got.FeedbackEnabled, tt.wantEnabled)
			}
			if tt.wantReasonIn != "" && !strings.Contains(got.DisabledReason, tt.wantReasonIn) {
				t.Fatalf("DetectProject() DisabledReason = %q, want substring %q", got.DisabledReason, tt.wantReasonIn)
			}
		})
	}
}

func TestDetectProjectUnknown(t *testing.T) {
	got, err := DetectProject(t.TempDir())
	if err != nil {
		t.Fatalf("DetectProject() error = %v", err)
	}
	if got.Profile != ProjectProfileUnknown || got.FeedbackEnabled || len(got.TestCommand) != 0 {
		t.Fatalf("DetectProject() = %#v, want disabled unknown project", got)
	}
	if !strings.Contains(got.DisabledReason, "--profile") || !strings.Contains(got.DisabledReason, "--test-command") {
		t.Fatalf("DisabledReason = %q, want profile and test-command overrides", got.DisabledReason)
	}
}

func TestDetectProjectMixedFamilies(t *testing.T) {
	root := t.TempDir()
	writeProjectFiles(t, root, map[string]string{
		"go.mod":       "module example.com/demo\n",
		"package.json": `{"scripts":{"test":"vitest"}}`,
		"Cargo.toml":   "[package]\nname = \"demo\"\n",
	})

	got, err := DetectProject(root)
	if err != nil {
		t.Fatalf("DetectProject() error = %v", err)
	}
	wantMarkers := []string{"Cargo.toml", "go.mod", "package.json"}
	if got.Profile != ProjectProfileUnknown || !reflect.DeepEqual(got.Markers, wantMarkers) || got.FeedbackEnabled {
		t.Fatalf("DetectProject() = %#v, want disabled mixed project with markers %#v", got, wantMarkers)
	}
	if !strings.Contains(got.DisabledReason, "multiple project families") {
		t.Fatalf("DisabledReason = %q, want mixed-family explanation", got.DisabledReason)
	}
}

func TestDetectProjectIgnoresNestedMarkers(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatalf("Mkdir(nested) error = %v", err)
	}
	writeProjectFiles(t, nested, map[string]string{
		"go.mod":       "module example.com/nested\n",
		"package.json": `{"scripts":{"test":"vitest"}}`,
	})

	got, err := DetectProject(root)
	if err != nil {
		t.Fatalf("DetectProject() error = %v", err)
	}
	if got.Profile != ProjectProfileUnknown || len(got.Markers) != 0 || got.FeedbackEnabled {
		t.Fatalf("DetectProject() = %#v, want nested markers ignored", got)
	}
}

func writeProjectFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
}
