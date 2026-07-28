package ticketworker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const expectedConfigTemplate = "version: 1\nmax_workers: 3\npoll_interval: 30s\nvoice_notifications: true\nvoice_notification_prefix: ticket-manager\n"

func TestConfigPathUsesWorkerDirectory(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	want := filepath.Join(root, ".zellij-agent", "worker", "config.yaml")
	if got := ConfigPath(root); got != want {
		t.Fatalf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestEnsureConfigWritesLoadableDefaults(t *testing.T) {
	root := t.TempDir()
	path, created, err := EnsureConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("EnsureConfig() created = false, want true")
	}
	if path != ConfigPath(root) {
		t.Fatalf("EnsureConfig() path = %q, want %q", path, ConfigPath(root))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != expectedConfigTemplate {
		t.Fatalf("config contents = %q, want %q", data, expectedConfigTemplate)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("config mode = %o, want 644", got)
	}

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 || cfg.MaxWorkers != 3 || cfg.PollInterval != 30*time.Second || !cfg.VoiceNotifications || cfg.VoiceNotificationPrefix != "ticket-manager" {
		t.Fatalf("loaded config = %+v", cfg)
	}
}

func TestEnsureConfigPreservesExistingAndRecreatesDeletedConfig(t *testing.T) {
	root := t.TempDir()
	path, _, err := EnsureConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	custom := "version: 1\nmax_workers: 5\npoll_interval: 1m\n"
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	gotPath, created, err := EnsureConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second EnsureConfig() created = true, want false")
	}
	if gotPath != path {
		t.Fatalf("second EnsureConfig() path = %q, want %q", gotPath, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != custom {
		t.Fatalf("preserved contents = %q, want %q", data, custom)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, created, err = EnsureConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("EnsureConfig() after delete created = false, want true")
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != expectedConfigTemplate {
		t.Fatalf("recreated contents = %q, want defaults", data)
	}
}

func TestLoadConfigVoiceDefaults(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, "version: 1\n")

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxWorkers != 3 || cfg.PollInterval != 30*time.Second {
		t.Fatalf("defaults = %d, %s; want 3, 30s", cfg.MaxWorkers, cfg.PollInterval)
	}
	if !cfg.VoiceNotifications || cfg.VoiceNotificationPrefix != "ticket-manager" {
		t.Fatalf("voice config = enabled:%v prefix:%q", cfg.VoiceNotifications, cfg.VoiceNotificationPrefix)
	}
}

func TestLoadConfigVoiceExplicitValues(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, "version: 1\nvoice_notifications: false\nvoice_notification_prefix: \" project-a \"\n")

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VoiceNotifications || cfg.VoiceNotificationPrefix != "project-a" {
		t.Fatalf("voice config = enabled:%v prefix:%q", cfg.VoiceNotifications, cfg.VoiceNotificationPrefix)
	}
}

func TestLoadConfigVoiceRejectsWhitespacePrefix(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, "version: 1\nvoice_notifications: true\nvoice_notification_prefix: \"   \"\n")

	_, err := LoadConfig(root)
	if err == nil || !strings.Contains(err.Error(), "voice_notification_prefix must not be empty") {
		t.Fatalf("LoadConfig() error = %v, want empty voice notification prefix error", err)
	}
}

func TestLoadConfigVoiceAllowsEmptyPrefixWhenDisabled(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, "version: 1\nvoice_notifications: false\nvoice_notification_prefix: \"   \"\n")

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VoiceNotifications || cfg.VoiceNotificationPrefix != "" {
		t.Fatalf("voice config = enabled:%v prefix:%q, want disabled with empty prefix", cfg.VoiceNotifications, cfg.VoiceNotificationPrefix)
	}
}

func TestLoadConfigIgnoresLegacyPromptTemplate(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, "version: 1\nmax_workers: 2\nprompt_template: legacy template\n")

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxWorkers != 2 || cfg.PollInterval != 30*time.Second {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := map[string]string{
		"missing version":        "max_workers: 3\n",
		"unsupported version":    "version: 2\n",
		"zero max workers":       "version: 1\nmax_workers: 0\n",
		"negative max workers":   "version: 1\nmax_workers: -1\n",
		"bad poll interval":      "version: 1\npoll_interval: soon\n",
		"zero poll interval":     "version: 1\npoll_interval: 0s\n",
		"negative poll interval": "version: 1\npoll_interval: -1s\n",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeConfigFile(t, root, body)
			if _, err := LoadConfig(root); err == nil {
				t.Fatal("LoadConfig() error = nil, want validation error")
			}
		})
	}
}

func TestValidateConfigRequiresPrefixOnlyWhenVoiceNotificationsEnabled(t *testing.T) {
	base := Config{Version: 1, MaxWorkers: 1, PollInterval: time.Second}
	tests := []struct {
		name    string
		enabled bool
		prefix  string
		wantErr string
	}{
		{name: "enabled empty", enabled: true, wantErr: "voice_notification_prefix must not be empty"},
		{name: "enabled whitespace", enabled: true, prefix: " \t ", wantErr: "voice_notification_prefix must not be empty"},
		{name: "disabled legacy empty", enabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			cfg.VoiceNotifications = tt.enabled
			cfg.VoiceNotificationPrefix = tt.prefix
			err := validateConfig(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateConfig() error = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("validateConfig() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func writeConfigFile(t *testing.T, root, body string) {
	t.Helper()
	path := ConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
