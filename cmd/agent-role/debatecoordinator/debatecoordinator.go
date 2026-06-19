package debatecoordinator

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	synthesisBegin  = "<<<DEBATE_SYNTHESIS_BEGIN>>>"
	synthesisEnd    = "<<<DEBATE_SYNTHESIS_END>>>"
	markerPrefix    = "Completion-Marker:"
	markerB64Prefix = "Completion-Marker-Base64:"
)

type synthesisBlock struct {
	CompletionMarker string
	Prompt           string
}

func readSynthesisBlock(r io.Reader) (synthesisBlock, error) {
	scanner := bufio.NewScanner(r)
	inBlock := false
	var marker string
	var lines []string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if trimmed == synthesisBegin {
				inBlock = true
			}
			continue
		}
		if trimmed == synthesisEnd {
			if marker == "" {
				return synthesisBlock{}, errors.New("debate coordinator: completion marker is required")
			}
			return synthesisBlock{
				CompletionMarker: marker,
				Prompt:           strings.TrimSpace(strings.Join(lines, "\n")) + "\n",
			}, nil
		}
		if strings.HasPrefix(line, markerPrefix) {
			marker = strings.TrimSpace(strings.TrimPrefix(line, markerPrefix))
			continue
		}
		if strings.HasPrefix(line, markerB64Prefix) {
			encoded := strings.TrimSpace(strings.TrimPrefix(line, markerB64Prefix))
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return synthesisBlock{}, fmt.Errorf("debate coordinator: decode completion marker: %w", err)
			}
			marker = string(decoded)
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return synthesisBlock{}, err
	}
	if inBlock {
		return synthesisBlock{}, fmt.Errorf("debate coordinator: missing %s", synthesisEnd)
	}
	return synthesisBlock{}, fmt.Errorf("debate coordinator: missing %s", synthesisBegin)
}

func prepareCodexCommand(path string, prompt string) (*exec.Cmd, error) {
	repoPath, err := resolveRepositoryPath(path)
	if err != nil {
		return nil, err
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex executable not found on PATH")
	}
	cmd := exec.Command(codexPath, "exec", "--cd", repoPath, "-")
	cmd.Dir = repoPath
	cmd.Stdin = strings.NewReader(prompt)
	return cmd, nil
}

func resolveRepositoryPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("path %q is not accessible: %w", absPath, err)
	}
	searchPath := absPath
	if !info.IsDir() {
		searchPath = filepath.Dir(absPath)
	}
	for {
		if _, err := os.Stat(filepath.Join(searchPath, ".git")); err == nil {
			return searchPath, nil
		}
		parent := filepath.Dir(searchPath)
		if parent == searchPath {
			break
		}
		searchPath = parent
	}
	return "", fmt.Errorf("path %q is not inside a git repository", absPath)
}

func Run(args []string) int {
	return runWithIO(args, os.Stdin, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Error: usage: agent-role debate-coordinator <path>")
		return 1
	}
	fmt.Fprintln(stdout, "debate_coordinator_ready")
	fmt.Fprintln(stdout, "waiting for synthesis input...")

	block, err := readSynthesisBlock(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	cmd, err := prepareCodexCommand(args[0], block.Prompt)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "Error running codex: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, block.CompletionMarker)
	return 0
}
