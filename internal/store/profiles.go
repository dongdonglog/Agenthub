package store

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Profile 是资源配置档案，记录引用的技能与 MCP。
type Profile struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Skills      []string `json:"skills" yaml:"skills"`
	MCPs        []string `json:"mcps" yaml:"mcps"`
}

// ProfileStore 用 YAML 保存 Profile。
type ProfileStore struct {
	path string
}

// NewProfileStore 构造 Profile 存储；文件缺失时返回空档案。
func NewProfileStore(dataDir string) *ProfileStore {
	return &ProfileStore{path: filepath.Join(dataDir, "profiles.yaml")}
}

// ListProfiles 读取全部档案。
func (s *ProfileStore) ListProfiles() ([]Profile, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Profile{}, nil
		}
		return nil, err
	}
	var out []Profile
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// PutProfile 新建或覆盖档案。
func (s *ProfileStore) PutProfile(p Profile) error {
	if p.Name == "" {
		return errors.New("Profile 名不能为空")
	}
	items, err := s.ListProfiles()
	if err != nil {
		return err
	}
	replaced := false
	for i := range items {
		if items[i].Name == p.Name {
			items[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, p)
	}
	return s.write(items)
}

// DeleteProfile 删除档案。
func (s *ProfileStore) DeleteProfile(name string) error {
	items, err := s.ListProfiles()
	if err != nil {
		return err
	}
	out := items[:0]
	for _, p := range items {
		if p.Name != name {
			out = append(out, p)
		}
	}
	return s.write(out)
}

func (s *ProfileStore) write(items []Profile) error {
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	raw, err := yaml.Marshal(items)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}

// ActiveProfile 是当前生效的资源配置。
type ActiveProfile struct {
	Name   string   `yaml:"name" json:"name"`
	Skills []string `yaml:"skills" json:"skills"`
	MCPs   []string `yaml:"mcps" json:"mcps"`
}

// activePath 返回当前配置文件名。
func (s *ProfileStore) activePath() string {
	return filepath.Join(filepath.Dir(s.path), "active_profile.yaml")
}

// Active 读取当前生效配置；不存在时返回空（表示不过滤）。
func (s *ProfileStore) Active() ActiveProfile {
	raw, err := os.ReadFile(s.activePath())
	if err != nil {
		return ActiveProfile{}
	}
	var out ActiveProfile
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return ActiveProfile{}
	}
	return out
}

// Apply 让指定档案生效。
func (s *ProfileStore) Apply(name string) error {
	items, err := s.ListProfiles()
	if err != nil {
		return err
	}
	for _, p := range items {
		if p.Name == name {
			raw, err := yaml.Marshal(ActiveProfile{Name: p.Name, Skills: p.Skills, MCPs: p.MCPs})
			if err != nil {
				return err
			}
			return os.WriteFile(s.activePath(), raw, 0o644)
		}
	}
	return errors.New("Profile 不存在")
}
