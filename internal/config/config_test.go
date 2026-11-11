package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moying/agenthub/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Channel != config.ChannelOff {
		t.Fatalf("channel = %q, want off", cfg.Channel)
	}
	if cfg.HTTP.Addr != "127.0.0.1:8787" {
		t.Fatalf("http.addr = %q", cfg.HTTP.Addr)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Fatalf("log = %+v", cfg.Log)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("log:\n  level: warn\nhttp:\n  addr: 0.0.0.0:9999\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHUB_LOG_LEVEL", "debug")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("env should override file: %q", cfg.Log.Level)
	}
	if cfg.HTTP.Addr != "0.0.0.0:9999" {
		t.Fatalf("file value lost: %q", cfg.HTTP.Addr)
	}
}

func TestFeishuRequiresCredentials(t *testing.T) {
	t.Setenv("AGENTHUB_CHANNEL", "feishu")
	if _, err := config.Load(""); err == nil {
		t.Fatal("expected error for missing feishu credentials")
	}
}

func TestFeishuAcceptsCredentials(t *testing.T) {
	t.Setenv("AGENTHUB_CHANNEL", "feishu")
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("FEISHU_ALLOWED_USERS", "ou_a, ou_b")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Feishu.AllowedUsers) != 2 {
		t.Fatalf("allowed users = %v", cfg.Feishu.AllowedUsers)
	}
}

func TestRejectsInvalidDBURL(t *testing.T) {
	t.Setenv("AGENTHUB_DB_URL", "mysql://nope")
	if _, err := config.Load(""); err == nil {
		t.Fatal("expected error for non-postgres url")
	}
}

func TestErrorDoesNotLeakSecret(t *testing.T) {
	t.Setenv("AGENTHUB_CHANNEL", "feishu")
	t.Setenv("FEISHU_APP_ID", "cli_leak_check")
	t.Setenv("FEISHU_APP_SECRET", "")
	_, err := config.Load("")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); contains(got, "cli_leak_check") {
		t.Fatalf("error leaks field value: %s", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
