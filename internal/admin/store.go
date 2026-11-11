// Package admin 提供管理页面与只读/可写管理 API。
package admin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill 是一个 Markdown 技能。
type Skill struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// MCP 是一个 MCP 服务配置。
type MCP struct {
	Name      string   `json:"name" yaml:"name"`
	Transport string   `json:"transport" yaml:"transport"`
	Command   string   `json:"command,omitempty" yaml:"command,omitempty"`
	Args      []string `json:"args,omitempty" yaml:"args,omitempty"`
	URL       string   `json:"url,omitempty" yaml:"url,omitempty"`
	Enabled   bool     `json:"enabled" yaml:"enabled"`
}

// Store 用文件保存技能与 MCP 配置。
//
// @remarks 选择文件而非数据库，是为了让技能可以直接用编辑器维护；
// 后续接入 PostgreSQL 时替换实现即可。
type Store struct {
	skillsDir string
	mcpFile   string
}

var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// NewStore 构造文件存储。
func NewStore(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("数据目录为空")
	}
	dir := filepath.Join(dataDir, "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{skillsDir: dir, mcpFile: filepath.Join(dataDir, "mcp.yaml")}, nil
}

// ListSkills 返回全部技能，支持 `<name>/SKILL.md` 与 `<name>.md` 两种布局。
func (s *Store) ListSkills() ([]Skill, error) {
	var out []Skill
	err := filepath.WalkDir(s.skillsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || !usableSkillFile(entry.Name()) {
			return nil
		}
		if entry.Name() != "SKILL.md" && filepath.Dir(path) != filepath.Clean(s.skillsDir) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		if entry.Name() == "SKILL.md" {
			name = filepath.Base(filepath.Dir(path))
		}
		out = append(out, Skill{Name: name, Content: string(raw)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetSkill 读取单个技能。
func (s *Store) GetSkill(name string) (Skill, error) {
	if !validName.MatchString(name) {
		return Skill{}, errors.New("技能名只允许字母数字与 ._-")
	}
	for _, path := range []string{
		filepath.Join(s.skillsDir, name, "SKILL.md"),
		filepath.Join(s.skillsDir, name+".md"),
	} {
		raw, err := os.ReadFile(path)
		if err == nil {
			return Skill{Name: name, Content: string(raw)}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Skill{}, err
		}
	}
	return Skill{}, os.ErrNotExist
}

// PutSkill 新建或覆盖技能，统一写入 `<name>/SKILL.md`。
func (s *Store) PutSkill(name, content string) error {
	if !validName.MatchString(name) {
		return errors.New("技能名只允许字母数字与 ._-")
	}
	dir := filepath.Join(s.skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644)
}

// DeleteSkill 删除技能（目录或扁平文件）；不存在时视为成功。
func (s *Store) DeleteSkill(name string) error {
	if !validName.MatchString(name) {
		return errors.New("技能名只允许字母数字与 ._-")
	}
	dir := filepath.Join(s.skillsDir, name)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return os.RemoveAll(dir)
	}
	err := os.Remove(filepath.Join(s.skillsDir, name+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ListMCPs 返回全部 MCP 配置。
func (s *Store) ListMCPs() ([]MCP, error) {
	raw, err := os.ReadFile(s.mcpFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []MCP{}, nil
		}
		return nil, err
	}
	var out []MCP
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("mcp.yaml 解析失败: %w", err)
	}
	if out == nil {
		out = []MCP{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// PutMCP 新建或覆盖一个 MCP 配置。
func (s *Store) PutMCP(item MCP) error {
	if !validName.MatchString(item.Name) {
		return errors.New("MCP 名只允许字母数字与 ._-")
	}
	if item.Transport != "stdio" && item.Transport != "http" {
		return errors.New("transport 只能是 stdio 或 http")
	}
	if item.Transport == "stdio" && item.Command == "" {
		return errors.New("stdio 模式需要 command")
	}
	if item.Transport == "http" && item.URL == "" {
		return errors.New("http 模式需要 url")
	}
	items, err := s.ListMCPs()
	if err != nil {
		return err
	}
	replaced := false
	for i := range items {
		if items[i].Name == item.Name {
			items[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, item)
	}
	return s.writeMCPs(items)
}

// DeleteMCP 删除一个 MCP 配置。
func (s *Store) DeleteMCP(name string) error {
	items, err := s.ListMCPs()
	if err != nil {
		return err
	}
	out := items[:0]
	for _, item := range items {
		if item.Name != name {
			out = append(out, item)
		}
	}
	return s.writeMCPs(out)
}

// usableSkillFile 过滤说明文档。
func usableSkillFile(name string) bool {
	lower := strings.ToLower(name)
	if lower == "readme.md" {
		return false
	}
	return !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".")
}

func (s *Store) writeMCPs(items []MCP) error {
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	raw, err := yaml.Marshal(items)
	if err != nil {
		return err
	}
	return os.WriteFile(s.mcpFile, raw, 0o644)
}
