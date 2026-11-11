package store

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ModelSettings 是可在管理台修改的模型配置。
type ModelSettings struct {
	BaseURL       string `yaml:"base_url" json:"base_url"`
	APIKey        string `yaml:"api_key" json:"api_key"`
	Model         string `yaml:"model" json:"model"`
	FallbackModel string `yaml:"fallback_model" json:"fallback_model"`
	VisionModel   string `yaml:"vision_model" json:"vision_model"`
}

// ModelSettingsStore 把模型配置保存在数据目录，权限 0600。
type ModelSettingsStore struct {
	path string
}

// NewModelSettingsStore 构造配置存储。
func NewModelSettingsStore(dataDir string) *ModelSettingsStore {
	return &ModelSettingsStore{path: filepath.Join(dataDir, "settings.yaml")}
}

// Load 读取配置；第二个返回值表示文件是否存在。
func (s *ModelSettingsStore) Load() (ModelSettings, bool, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ModelSettings{}, false, nil
		}
		return ModelSettings{}, false, err
	}
	var out ModelSettings
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return ModelSettings{}, true, err
	}
	return out, true, nil
}

// Save 写入配置。
func (s *ModelSettingsStore) Save(v ModelSettings) error {
	raw, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(s.path, 0o600)
}
