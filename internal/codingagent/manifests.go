package codingagent

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed manifests/*.yaml
var embeddedManifests embed.FS

var embeddedManifestFiles = []string{
	"codex.yaml",
	"claude.yaml",
	"gemini.yaml",
	"cursor.yaml",
}

func LoadEmbeddedDetector() (*Detector, map[Kind]error) {
	return loadEmbeddedDetector(embeddedManifests)
}

func loadEmbeddedDetector(source fs.FS) (*Detector, map[Kind]error) {
	manifests := make(map[Kind]Manifest, len(embeddedManifestFiles))
	loadErrors := make(map[Kind]error)
	for _, filename := range embeddedManifestFiles {
		profileKind, err := manifestKind(filename)
		if err != nil {
			continue
		}
		path := "manifests/" + filename
		contents, err := fs.ReadFile(source, path)
		if err != nil {
			loadErrors[profileKind] = fmt.Errorf("%s: %w", filename, err)
			continue
		}
		manifest, err := LoadManifest(contents)
		if err != nil {
			loadErrors[profileKind] = fmt.Errorf("%s: %w", filename, err)
			continue
		}
		if manifest.Agent != profileKind {
			loadErrors[profileKind] = fmt.Errorf("%s: manifest agent %q does not match expected kind %q", filename, manifest.Agent, profileKind)
			continue
		}
		manifests[profileKind] = manifest
	}

	detector, err := NewDetector(manifests)
	if err != nil {
		panic(fmt.Sprintf("construct embedded coding-agent detector: %v", err))
	}
	return detector, loadErrors
}

func manifestKind(filename string) (Kind, error) {
	for kind, profile := range profiles {
		if profile.Manifest == filename {
			return kind, nil
		}
	}
	return "", fmt.Errorf("no coding-agent profile for embedded manifest %q", filename)
}
