package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moying/agenthub/internal/agent"
)

func TestLoadSkillsSupportsNestedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "tiangong-troubleshoot")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---
name: tiangong-troubleshoot
description: 排查天工现场部署和运行故障
metadata:
  owner: test
---

# 天工故障排查

按症状给排查顺序。
`
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := agent.LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %d", len(skills))
	}
	s := skills[0]
	if s.Name != "tiangong-troubleshoot" {
		t.Fatalf("name = %q", s.Name)
	}
	if s.Description != "排查天工现场部署和运行故障" {
		t.Fatalf("description = %q", s.Description)
	}
	if containsFrontmatter(s.Content) {
		t.Fatalf("正文不应包含 frontmatter: %q", s.Content)
	}
}

func TestLoadSkillsSupportsFlatLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	content := "# 故障分析\n> 按证据排序\n\n步骤\n"
	if err := os.WriteFile(filepath.Join(dir, "incident.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := agent.LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "incident" {
		t.Fatalf("skills = %+v", skills)
	}
	if skills[0].Description != "按证据排序" {
		t.Fatalf("description = %q", skills[0].Description)
	}
}

func TestLoadSkillsIgnoresReferenceDocs(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "release")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# 发版\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "steps.md"), []byte("# 参考\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := agent.LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "release" {
		t.Fatalf("只应加载 SKILL.md，实际 %+v", skills)
	}
}

func TestLoadSkillsMissingDir(t *testing.T) {
	skills, err := agent.LoadSkills(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("缺失目录应返回空: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("skills = %+v", skills)
	}
}

func containsFrontmatter(s string) bool {
	return len(s) >= 3 && s[:3] == "---"
}

func TestLoadSkillsIgnoresReadme(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# 说明\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := agent.LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("README 不应作为技能: %+v", skills)
	}
}
