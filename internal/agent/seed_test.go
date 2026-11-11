package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moying/agenthub/internal/agent"
)

func TestSeedSkillsCopiesWhenEmpty(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(src, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "demo", "SKILL.md"), []byte("---\nname: demo\n---\n正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "flat.md"), []byte("# 扁平\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	copied, err := agent.SeedSkills(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if copied != 2 {
		t.Fatalf("copied = %d", copied)
	}
	if _, err := os.Stat(filepath.Join(dst, "demo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSeedSkillsSkipsNonEmpty(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "a.md"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dst, "keep.md"), []byte("keep"), 0o644)

	copied, err := agent.SeedSkills(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if copied != 0 {
		t.Fatalf("目标非空时不应复制，copied = %d", copied)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.md")); err == nil {
		t.Fatal("不应写入新文件")
	}
}
