package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ProjectProfile string

const (
	ProjectProfileUnknown ProjectProfile = "unknown"
	ProjectProfileGo      ProjectProfile = "go"
	ProjectProfileNPM     ProjectProfile = "npm"
	ProjectProfilePNPM    ProjectProfile = "pnpm"
	ProjectProfileYarn    ProjectProfile = "yarn"
	ProjectProfileRust    ProjectProfile = "rust"
)

type ProjectDetection struct {
	Profile         ProjectProfile
	Markers         []string
	TestCommand     []string
	BuildCommand    []string
	FeedbackEnabled bool
	DisabledReason  string
}

var projectMarkerNames = []string{
	"Cargo.toml",
	"go.mod",
	"go.work",
	"package-lock.json",
	"package.json",
	"npm-shrinkwrap.json",
	"pnpm-lock.yaml",
	"yarn.lock",
}

func DetectProject(root string) (ProjectDetection, error) {
	markers, err := detectProjectMarkers(root)
	if err != nil {
		return ProjectDetection{}, err
	}

	hasGo := containsMarker(markers, "go.mod") || containsMarker(markers, "go.work")
	hasNode := containsMarker(markers, "package.json")
	hasRust := containsMarker(markers, "Cargo.toml")
	families := 0
	for _, present := range []bool{hasGo, hasNode, hasRust} {
		if present {
			families++
		}
	}
	if families > 1 {
		return ProjectDetection{
			Profile:        ProjectProfileUnknown,
			Markers:        markers,
			DisabledReason: "multiple project families detected; use --profile or --test-command",
		}, nil
	}

	switch {
	case hasGo:
		return ProjectDetection{
			Profile:         ProjectProfileGo,
			Markers:         markers,
			TestCommand:     []string{"go", "test", "./..."},
			BuildCommand:    []string{"go", "build", "./..."},
			FeedbackEnabled: true,
		}, nil
	case hasRust:
		return ProjectDetection{
			Profile:         ProjectProfileRust,
			Markers:         markers,
			TestCommand:     []string{"cargo", "test"},
			BuildCommand:    []string{"cargo", "check"},
			FeedbackEnabled: true,
		}, nil
	case hasNode:
		return detectNodeProject(root, markers)
	default:
		return ProjectDetection{
			Profile:        ProjectProfileUnknown,
			Markers:        markers,
			DisabledReason: "project type not detected; use --profile or --test-command",
		}, nil
	}
}

func detectNodeProject(root string, markers []string) (ProjectDetection, error) {
	hasNPM := containsMarker(markers, "package-lock.json") || containsMarker(markers, "npm-shrinkwrap.json")
	hasPNPM := containsMarker(markers, "pnpm-lock.yaml")
	hasYarn := containsMarker(markers, "yarn.lock")
	managerCount := 0
	for _, present := range []bool{hasNPM, hasPNPM, hasYarn} {
		if present {
			managerCount++
		}
	}
	if managerCount > 1 {
		return ProjectDetection{
			Profile:        ProjectProfileUnknown,
			Markers:        markers,
			DisabledReason: "multiple Node package managers detected; use --profile or --test-command",
		}, nil
	}

	profile := ProjectProfileNPM
	if hasPNPM {
		profile = ProjectProfilePNPM
	} else if hasYarn {
		profile = ProjectProfileYarn
	}
	result := ProjectDetection{Profile: profile, Markers: markers}

	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ProjectDetection{}, fmt.Errorf("read package.json: %w", err)
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		result.DisabledReason = fmt.Sprintf("invalid package.json: %v", err)
		return result, nil
	}

	hasTest := strings.TrimSpace(manifest.Scripts["test"]) != ""
	hasBuild := strings.TrimSpace(manifest.Scripts["build"]) != ""
	switch profile {
	case ProjectProfilePNPM:
		if hasTest {
			result.TestCommand = []string{"pnpm", "test"}
		}
		if hasBuild {
			result.BuildCommand = []string{"pnpm", "build"}
		}
	case ProjectProfileYarn:
		if hasTest {
			result.TestCommand = []string{"yarn", "test"}
		}
		if hasBuild {
			result.BuildCommand = []string{"yarn", "build"}
		}
	default:
		if hasTest {
			result.TestCommand = []string{"npm", "test"}
		}
		if hasBuild {
			result.BuildCommand = []string{"npm", "run", "build"}
		}
	}

	result.FeedbackEnabled = hasTest
	if !hasTest {
		result.DisabledReason = "package.json has no test script; use --test-command to enable feedback"
	}
	return result, nil
}

func detectProjectMarkers(root string) ([]string, error) {
	markers := make([]string, 0, len(projectMarkerNames))
	for _, name := range projectMarkerNames {
		_, err := os.Stat(filepath.Join(root, name))
		switch {
		case err == nil:
			markers = append(markers, name)
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, fmt.Errorf("inspect project marker %s: %w", name, err)
		}
	}
	sort.Strings(markers)
	return markers, nil
}

func containsMarker(markers []string, want string) bool {
	index := sort.SearchStrings(markers, want)
	return index < len(markers) && markers[index] == want
}
