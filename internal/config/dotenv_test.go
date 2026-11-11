package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moying/agenthub/internal/config"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# comment\nFEISHU_TRANSPORT=webhook\nFEISHU_APP_ID=\"cli_test\"\n\nBAD_LINE\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := config.LoadDotEnv(path); err == nil {
		t.Fatal("缺少等号的行应报错")
	}
}

func TestLoadDotEnvValuesAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# comment\nFEISHU_TRANSPORT=webhook\nFEISHU_APP_ID=\"cli_test\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FEISHU_APP_ID", "cli_from_env")

	if err := config.LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("FEISHU_TRANSPORT"); got != "webhook" {
		t.Fatalf("transport = %q", got)
	}
	if got := os.Getenv("FEISHU_APP_ID"); got != "cli_from_env" {
		t.Fatalf("已有环境变量被覆盖: %q", got)
	}
}

func TestLoadDotEnvMissingFileIsOK(t *testing.T) {
	if err := config.LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatalf("缺失文件应忽略: %v", err)
	}
}
