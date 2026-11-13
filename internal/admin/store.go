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

// Skill 是一个技能及其元数据。
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Status      string `json:"status"` // active | disabled | archived
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	Executable  bool   `json:"executable"`
	UpdatedAt   string `json:"updated_at"`
	References  int    `json:"references"`
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
// @remarks 技能状态通过目录隔离：active 在根目录，停用/归档分别在 .disabled / .archived，
// 加载器只读取 active，避免运行期误用。
type Store struct {
	skillsDir string
	mcpFile   string
}

var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// NewStore 构造文件存储。
func NewStore(dataDir string) (*Store, error) {
	return NewStoreWithSkills(dataDir, filepath.Join(dataDir, "skills"))
}

// NewStoreWithSkills 指定技能目录构造存储。
func NewStoreWithSkills(dataDir, skillsDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("数据目录为空")
	}
	dir := skillsDir
	if dir == "" {
		dir = filepath.Join(dataDir, "skills")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{skillsDir: dir, mcpFile: filepath.Join(dataDir, "mcp.yaml")}, nil
}

func (s *Store) scopeDir(status string) string {
	switch status {
	case "disabled":
		return filepath.Join(s.skillsDir, ".disabled")
	case "archived":
		return filepath.Join(s.skillsDir, ".archived")
	default:
		return s.skillsDir
	}
}

// ListSkills 返回全部技能（active / disabled / archived）。
func (s *Store) ListSkills() ([]Skill, error) {
	var out []Skill
	for _, status := range []string{"active", "disabled", "archived"} {
		dir := s.scopeDir(status)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			path := filepath.Join(dir, name)
			if entry.IsDir() {
				if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
					continue
				}
				skill := buildSkill(name, path, status)
				out = append(out, skill)
				continue
			}
			if status != "active" || !strings.HasSuffix(name, ".md") || !usableSkillFile(name) {
				continue
			}
			skill := buildSkill(strings.TrimSuffix(name, ".md"), path, status)
			out = append(out, skill)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func buildSkill(name, path, status string) Skill {
	skill := Skill{Name: name, Status: status, UpdatedAt: ""}
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			skill.UpdatedAt = info.ModTime().Format("2006-01-02 15:04:05")
			_ = filepath.WalkDir(path, func(p string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				if fi, err := e.Info(); err == nil {
					skill.Files++
					skill.Bytes += fi.Size()
					if fi.Mode()&0o111 != 0 {
						skill.Executable = true
					}
				}
				return nil
			})
			if raw, err := os.ReadFile(filepath.Join(path, "SKILL.md")); err == nil {
				skill.Content = string(raw)
			}
		} else {
			skill.UpdatedAt = info.ModTime().Format("2006-01-02 15:04:05")
			skill.Files = 1
			skill.Bytes = info.Size()
			if raw, err := os.ReadFile(path); err == nil {
				skill.Content = string(raw)
			}
		}
	}
	skill.Description = parseDescription(skill.Content)
	return skill
}

// parseDescription 从 frontmatter 或首行引用中提取描述。
func parseDescription(content string) string {
	lines := strings.Split(content, "\n")
	inFront := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i == 0 && trimmed == "---" {
			inFront = true
			continue
		}
		if inFront {
			if trimmed == "---" {
				break
			}
			if strings.HasPrefix(trimmed, "description:") {
				return strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
			}
			continue
		}
		if strings.HasPrefix(trimmed, "> ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "> "))
		}
	}
	return ""
}

// findSkill 定位技能文件/目录及其状态。
func (s *Store) findSkill(name string) (path, status string, err error) {
	for _, st := range []string{"active", "disabled", "archived"} {
		dir := s.scopeDir(st)
		dirPath := filepath.Join(dir, name)
		if info, statErr := os.Stat(dirPath); statErr == nil && info.IsDir() {
			return dirPath, st, nil
		}
		mdPath := filepath.Join(dir, name+".md")
		if info, statErr := os.Stat(mdPath); statErr == nil && !info.IsDir() {
			return mdPath, st, nil
		}
	}
	return "", "", os.ErrNotExist
}

// GetSkill 读取单个技能。
func (s *Store) GetSkill(name string) (Skill, error) {
	if !validName.MatchString(name) {
		return Skill{}, errors.New("技能名只允许字母数字与 ._-")
	}
	path, status, err := s.findSkill(name)
	if err != nil {
		return Skill{}, err
	}
	return buildSkill(name, path, status), nil
}

// PutSkill 新建或覆盖技能，统一写入 active 目录。
func (s *Store) PutSkill(name, content string) error {
	if !validName.MatchString(name) {
		return errors.New("技能名只允许字母数字与 ._-")
	}
	if path, status, err := s.findSkill(name); err == nil && status != "active" {
		_ = os.RemoveAll(path)
	}
	dir := filepath.Join(s.skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644)
}

// SetSkillStatus 在 active / disabled / archived 之间移动技能。
func (s *Store) SetSkillStatus(name, status string) error {
	if !validName.MatchString(name) {
		return errors.New("技能名只允许字母数字与 ._-")
	}
	if status != "active" && status != "disabled" && status != "archived" {
		return errors.New("状态只能是 active / disabled / archived")
	}
	path, current, err := s.findSkill(name)
	if err != nil {
		return err
	}
	if current == status {
		return nil
	}
	targetDir := s.scopeDir(status)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(targetDir, filepath.Base(path)))
}

// DeleteSkill 删除技能（任意状态）；不存在时视为成功。
func (s *Store) DeleteSkill(name string) error {
	if !validName.MatchString(name) {
		return errors.New("技能名只允许字母数字与 ._-")
	}
	path, _, err := s.findSkill(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.RemoveAll(path)
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

// SkillFile 是技能包内的一个文件。
type SkillFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// skillBaseDir 返回技能所在目录（扁平文件返回其所在目录）。
func (s *Store) skillBaseDir(name string) (string, error) {
	path, _, err := s.findSkill(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return path, nil
	}
	return "", errors.New("扁平技能不支持多文件")
}

// ListFiles 列出技能目录内的全部文件（相对路径）。
func (s *Store) ListFiles(name string) ([]SkillFile, error) {
	base, err := s.skillBaseDir(name)
	if err != nil {
		return nil, err
	}
	var out []SkillFile
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		out = append(out, SkillFile{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

// resolveSkillFile 把相对路径限制在技能目录内。
func (s *Store) resolveSkillFile(name, rel string) (string, error) {
	base, err := s.skillBaseDir(name)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", errors.New("非法文件路径")
	}
	full := filepath.Join(base, clean)
	if !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		return "", errors.New("非法文件路径")
	}
	return full, nil
}

// ReadFile 读取技能内的文件。
func (s *Store) ReadFile(name, rel string) (string, error) {
	full, err := s.resolveSkillFile(name, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if info.Size() > 512*1024 {
		return "", errors.New("文件超过 512KB，不支持在线编辑")
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// WriteFile 写入技能内的文件（仅文本，最大 512KB）。
func (s *Store) WriteFile(name, rel, content string) error {
	if len(content) > 512*1024 {
		return errors.New("内容超过 512KB")
	}
	full, err := s.resolveSkillFile(name, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

// DeleteFile 删除技能内的文件。
func (s *Store) DeleteFile(name, rel string) error {
	if filepath.ToSlash(rel) == "SKILL.md" {
		return errors.New("SKILL.md 不能单独删除，请删除整个技能")
	}
	full, err := s.resolveSkillFile(name, rel)
	if err != nil {
		return err
	}
	err = os.Remove(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
