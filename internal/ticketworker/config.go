package ticketworker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	configVersion       = 1
	defaultMaxWorkers   = 3
	defaultPollInterval = 30 * time.Second
	configTemplate      = "version: 1\nmax_workers: 3\npoll_interval: 30s\nworker:\n  command: [\"go\", \"run\", \"./cmd/ticket-worker\"]\n  completion_marker: \"ZELLIJ_AGENT_WORKER_DONE\"\n"
)

type WorkerConfig struct {
	Command          []string `yaml:"command"`
	CompletionMarker string   `yaml:"completion_marker"`
}

type Config struct {
	Version      int
	MaxWorkers   int
	PollInterval time.Duration
	Worker       WorkerConfig
}

type diskConfig struct {
	Version      int          `yaml:"version"`
	MaxWorkers   *int         `yaml:"max_workers"`
	PollInterval string       `yaml:"poll_interval"`
	Worker       WorkerConfig `yaml:"worker"`
}

func ConfigPath(root string) string {
	return filepath.Join(root, ".zellij-agent", "worker", "config.yaml")
}

func LoadConfig(root string) (Config, error) {
	file, err := os.Open(ConfigPath(root))
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	var disk diskConfig
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&disk); err != nil {
		return Config{}, fmt.Errorf("decode ticket-worker config: %w", err)
	}

	maxWorkers := defaultMaxWorkers
	if disk.MaxWorkers != nil {
		maxWorkers = *disk.MaxWorkers
	}
	cfg := Config{
		Version:    disk.Version,
		MaxWorkers: maxWorkers,
		Worker:     disk.Worker,
	}
	if disk.PollInterval == "" {
		cfg.PollInterval = defaultPollInterval
	} else {
		cfg.PollInterval, err = time.ParseDuration(disk.PollInterval)
		if err != nil {
			return Config{}, fmt.Errorf("poll_interval: %w", err)
		}
	}
	return cfg, validateConfig(cfg)
}

func validateConfig(cfg Config) error {
	if cfg.Version != configVersion {
		return fmt.Errorf("version must be %d", configVersion)
	}
	if cfg.MaxWorkers <= 0 {
		return fmt.Errorf("max_workers must be positive")
	}
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("poll_interval must be positive")
	}
	if len(cfg.Worker.Command) == 0 {
		return fmt.Errorf("worker.command must not be empty")
	}
	for i, arg := range cfg.Worker.Command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("worker.command[%d] must not be empty", i)
		}
	}
	marker := cfg.Worker.CompletionMarker
	if marker != strings.TrimSpace(marker) {
		return fmt.Errorf("worker.completion_marker must not have surrounding whitespace")
	}
	if strings.ContainsAny(marker, "\r\n") {
		return fmt.Errorf("worker.completion_marker must be a single line")
	}
	if marker == "" {
		return fmt.Errorf("worker.completion_marker must not be empty")
	}
	return nil
}

func InitConfig(root string, force bool) (string, error) {
	path := ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if force {
		if err := replaceConfig(path); err != nil {
			return "", err
		}
		return path, nil
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(configTemplate); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func replaceConfig(path string) (err error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".config.yaml-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o644); err != nil {
		return err
	}
	if _, err := temp.WriteString(configTemplate); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
