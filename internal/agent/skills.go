package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill 是一个 Markdown 技能。
type Skill struct {
	Name        string
	Description string
	Content     string
	Path        string
}

// LoadSkills 读取目录下的 *.md 技能。
//
// @remarks 文件名（去扩展名）作为名称；首行 `# 标题` 覆盖名称，
// 紧随其后的 `> 描述` 作为描述。目录缺失时返回空列表。
func LoadSkills(dir string) ([]Skill, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Skill, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		skill := Skill{
			Name:    strings.TrimSuffix(e.Name(), ".md"),
			Content: strings.TrimSpace(string(raw)),
			Path:    path,
		}
		lines := strings.Split(skill.Content, "\n")
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
			skill.Name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "# "))
			lines = lines[1:]
		}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "> ") {
				skill.Description = strings.TrimSpace(strings.TrimPrefix(line, "> "))
				break
			}
			break
		}
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
