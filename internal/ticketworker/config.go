package ticketworker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"zellij-with-codeagent/internal/codingagent"
)

const (
	configVersion                  = 1
	defaultMaxWorkers              = 3
	defaultPollInterval            = 30 * time.Second
	defaultVoiceNotificationPrefix = "ticket-manager"
	configTemplate                 = "version: 1\ndefault_agent: codex\nmax_workers: 3\npoll_interval: 30s\nvoice_notifications: true\nvoice_notification_prefix: ticket-manager\n"
)

type Config struct {
	DefaultAgent            string
	Version                 int
	MaxWorkers              int
	PollInterval            time.Duration
	VoiceNotifications      bool
	VoiceNotificationPrefix string
}

type diskConfig struct {
	DefaultAgent            *string `yaml:"default_agent"`
	Version                 int     `yaml:"version"`
	MaxWorkers              *int    `yaml:"max_workers"`
	PollInterval            string  `yaml:"poll_interval"`
	VoiceNotifications      *bool   `yaml:"voice_notifications"`
	VoiceNotificationPrefix *string `yaml:"voice_notification_prefix"`
}

// ConfigPath returns the user config path, or an empty string if it cannot be
// resolved. LoadConfig and EnsureConfig report the underlying resolution error.
func ConfigPath(root string) string {
	path, _, _ := configPaths(root)
	return path
}

func configPaths(root string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve ticket-worker home: %w", err)
	}
	project, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return "", "", fmt.Errorf("resolve ticket-worker project: %w", err)
	}
	// Linked worktrees point at a private Git directory whose commondir points
	// back to the main checkout's .git directory. Submodules have no commondir.
	marker := filepath.Join(project, ".git")
	info, err := os.Stat(marker)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", "", err
	}
	if err == nil && !info.IsDir() {
		data, err := os.ReadFile(marker)
		if err != nil {
			return "", "", err
		}
		gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
		if !ok {
			return "", "", fmt.Errorf("invalid Git worktree marker: %s", marker)
		}
		if !filepath.IsAbs(gitdir) {
			gitdir = filepath.Join(project, gitdir)
		}
		common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		if err == nil {
			commonDir := strings.TrimSpace(string(common))
			if !filepath.IsAbs(commonDir) {
				commonDir = filepath.Join(gitdir, commonDir)
			}
			commonDir, err = filepath.EvalSymlinks(commonDir)
			if err != nil {
				return "", "", err
			}
			project = filepath.Dir(commonDir)
		}
	}
	path := filepath.Join(home, ".zellij-ticket", strings.TrimLeft(project, string(filepath.Separator)), "config.yaml")
	legacy := filepath.Join(project, ".zellij-agent", "worker", "config.yaml")
	return path, legacy, nil
}

func LoadConfig(root string) (Config, error) {
	path, legacy, err := configPaths(root)
	if err != nil {
		return Config{}, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		// Only migrate an existing config; reading an uninitialized project
		// must not create default settings or a ticket database.
		data, err := os.ReadFile(legacy)
		if err != nil {
			return Config{}, err
		}
		if _, err := createConfig(path, data); err != nil {
			return Config{}, err
		}
	} else if err != nil {
		return Config{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	var disk diskConfig
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&disk); err != nil {
		return Config{}, fmt.Errorf("decode ticket-worker config: %w", err)
	}

	cfg := Config{
		DefaultAgent:            "codex",
		Version:                 disk.Version,
		MaxWorkers:              defaultMaxWorkers,
		PollInterval:            defaultPollInterval,
		VoiceNotifications:      true,
		VoiceNotificationPrefix: defaultVoiceNotificationPrefix,
	}
	if disk.DefaultAgent != nil {
		kind, err := codingagent.ParseKind(*disk.DefaultAgent)
		if err != nil {
			return Config{}, fmt.Errorf("default_agent: %w", err)
		}
		cfg.DefaultAgent = string(kind)
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
	if disk.VoiceNotifications != nil {
		cfg.VoiceNotifications = *disk.VoiceNotifications
	}
	if disk.VoiceNotificationPrefix != nil {
		cfg.VoiceNotificationPrefix = strings.TrimSpace(*disk.VoiceNotificationPrefix)
	}
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateConfig(cfg Config) error {
	if cfg.DefaultAgent != "" {
		if _, err := codingagent.ParseKind(cfg.DefaultAgent); err != nil {
			return fmt.Errorf("default_agent: %w", err)
		}
	}
	if cfg.Version != configVersion {
		return fmt.Errorf("version must be %d", configVersion)
	}
	if cfg.MaxWorkers <= 0 {
		return fmt.Errorf("max_workers must be positive")
	}
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("poll_interval must be positive")
	}
	if cfg.VoiceNotifications && strings.TrimSpace(cfg.VoiceNotificationPrefix) == "" {
		return fmt.Errorf("voice_notification_prefix must not be empty")
	}
	return nil
}

func EnsureConfig(root string) (path string, created bool, err error) {
	path, legacy, err := configPaths(root)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	data, err := os.ReadFile(legacy)
	if errors.Is(err, fs.ErrNotExist) {
		data = []byte(configTemplate)
	} else if err != nil {
		return "", false, fmt.Errorf("read legacy ticket-worker config: %w", err)
	}
	created, err = createConfig(path, data)
	return path, created, err
}

// Publish the complete file without replacing a config another process created.
func createConfig(path string, data []byte) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create ticket-worker config directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return false, err
	}
	if err := file.Chmod(0o644); err != nil {
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := os.Link(file.Name(), path); errors.Is(err, fs.ErrExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("publish ticket-worker config: %w", err)
	}
	return true, nil
}
