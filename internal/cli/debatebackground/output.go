package debatebackground

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func resolveOutputPath(path, outputFormat string, now time.Time) (string, error) {
	target := strings.TrimSpace(path)
	if target == "" {
		target = defaultOutputPath
	}
	info, err := os.Stat(target)
	if err == nil && info.IsDir() {
		return filepath.Join(target, generatedOutputFilename(outputFormat, now)), nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err != nil && strings.HasSuffix(target, string(os.PathSeparator)) {
		return filepath.Join(target, generatedOutputFilename(outputFormat, now)), nil
	}
	return target, nil
}

func generatedOutputFilename(outputFormat string, now time.Time) string {
	extension := ".md"
	if outputFormat == "json" {
		extension = ".json"
	}
	return fmt.Sprintf("zellij-agent-debate-%s%s", now.Format("20060102-150405.000000000"), extension)
}

func writeAtomic(path string, data []byte) (err error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".debate-background-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return nil
}
