package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill 是一个 Markdown 技能。
type Skill struct {
	// Name 是技能标识，优先取 frontmatter 的 name。
	Name string
	// Description 是一句话说明，优先取 frontmatter 的 description。
	Description string
	// Content 是去掉 frontmatter 后的正文。
	Content string
	// Path 是文件路径。
	Path string
}

// frontmatter 是 SKILL.md 的头部元数据。
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// LoadSkills 递归读取目录下的技能。
//
// @remarks 同时支持两种布局：
//   - `<dir>/<name>/SKILL.md`（Claude/DSH 习惯）
//   - `<dir>/<name>.md`（早期扁平格式）
//
// frontmatter 中的 name/description 优先，缺失时退回文件名与首行 `> 描述`。
// 目录不存在时返回空列表。
func LoadSkills(dir string) ([]Skill, error) {
	if dir == "" {
		return nil, nil
	}
	var out []Skill
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || !usableSkillFile(entry.Name()) {
			return nil
		}
		// 目录布局只认 SKILL.md，避免把参考文档当技能。
		if entry.Name() != "SKILL.md" && filepath.Dir(path) != filepath.Clean(dir) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		skill := parseSkill(string(raw), path)
		if skill.Name == "" {
			return nil
		}
		out = append(out, skill)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parseSkill(raw, path string) Skill {
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	if filepath.Base(path) == "SKILL.md" {
		name = filepath.Base(filepath.Dir(path))
	}
	content := strings.TrimSpace(raw)
	var meta frontmatter

	lines := strings.Split(raw, "\n")
	if len(lines) > 1 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				_ = yaml.Unmarshal([]byte(strings.Join(lines[1:i], "\n")), &meta)
				content = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
				break
			}
		}
	}

	if meta.Name != "" {
		name = meta.Name
	}
	description := strings.TrimSpace(meta.Description)
	if description == "" {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "# ") {
				continue
			}
			if strings.HasPrefix(line, "> ") {
				description = strings.TrimSpace(strings.TrimPrefix(line, "> "))
			}
			break
		}
	}
	if name == "SKILL" {
		name = filepath.Base(filepath.Dir(path))
	}
	return Skill{Name: name, Description: description, Content: content, Path: path}
}

// usableSkillFile 过滤说明文档，只把真正的技能文件当作技能。
//
// @remarks 目录布局只认 SKILL.md；扁平布局排除 README 与下划线开头文件。
func usableSkillFile(name string) bool {
	lower := strings.ToLower(name)
	if lower == "readme.md" {
		return false
	}
	return !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".")
}
