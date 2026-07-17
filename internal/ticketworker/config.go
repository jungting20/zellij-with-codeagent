package ticketworker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	configVersion       = 1
	defaultMaxWorkers   = 3
	defaultPollInterval = 30 * time.Second
	configTemplate      = "version: 1\nmax_workers: 3\npoll_interval: 30s\nprompt_template: |\n  티켓 #{{ .ID }}을 구현해줘.\n\n  제목: {{ .Title }}\n  요약: {{ .Summary }}\n  설계: {{ .SpecPath }}\n  구현 계획: {{ .PlanPath }}\n"
)

type Config struct {
	Version        int
	MaxWorkers     int
	PollInterval   time.Duration
	PromptTemplate string
}

type diskConfig struct {
	Version        int    `yaml:"version"`
	MaxWorkers     *int   `yaml:"max_workers"`
	PollInterval   string `yaml:"poll_interval"`
	PromptTemplate string `yaml:"prompt_template"`
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

	cfg := Config{
		Version:        disk.Version,
		MaxWorkers:     defaultMaxWorkers,
		PollInterval:   defaultPollInterval,
		PromptTemplate: disk.PromptTemplate,
	}
	if disk.MaxWorkers != nil {
		cfg.MaxWorkers = *disk.MaxWorkers
	}
	if disk.PollInterval != "" {
		cfg.PollInterval, err = time.ParseDuration(disk.PollInterval)
		if err != nil {
			return Config{}, fmt.Errorf("poll_interval: %w", err)
		}
	}
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
	if strings.TrimSpace(cfg.PromptTemplate) == "" {
		return fmt.Errorf("prompt_template must not be empty")
	}
	if _, err := template.New("ticket-prompt").Option("missingkey=error").Parse(cfg.PromptTemplate); err != nil {
		return fmt.Errorf("prompt_template: %w", err)
	}
	return nil
}

func EnsureConfig(root string) (path string, created bool, err error) {
	path = ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, fmt.Errorf("create ticket-worker config directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return path, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("create ticket-worker config: %w", err)
	}

	complete := false
	defer func() {
		if !complete {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err := file.WriteString(configTemplate); err != nil {
		return "", false, fmt.Errorf("write ticket-worker config: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", false, fmt.Errorf("close ticket-worker config: %w", err)
	}
	complete = true
	return path, true, nil
}
