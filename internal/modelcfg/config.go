// Package modelcfg 定义多供应商模型配置与线路。
package modelcfg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是模型路由配置。
type Config struct {
	Version   int        `yaml:"version" json:"version"`
	Providers []Provider `yaml:"providers" json:"providers"`
	Models    []Model    `yaml:"models" json:"models"`
	Routes    []Route    `yaml:"routes" json:"routes"`
}

// Provider 是一个 OpenAI 兼容供应商。
type Provider struct {
	Name    string `yaml:"name" json:"name"`
	BaseURL string `yaml:"base_url" json:"base_url"`
	APIKey  string `yaml:"api_key" json:"api_key"`
}

// Model 是供应商下的一个上游模型。
type Model struct {
	ID            string   `yaml:"id" json:"id"`
	Provider      string   `yaml:"provider" json:"provider"`
	Model         string   `yaml:"model" json:"model"`
	Capabilities  []string `yaml:"capabilities" json:"capabilities"`
	ContextWindow int      `yaml:"context_window" json:"context_window"`
	MaxTokens     int      `yaml:"max_tokens" json:"max_tokens"`
}

// Retry 是线路重试配置。
type Retry struct {
	MaxAttempts int `yaml:"max_attempts" json:"max_attempts"`
	BackoffMS   int `yaml:"backoff_ms" json:"backoff_ms"`
}

// Circuit 是熔断配置。
type Circuit struct {
	Failures        int `yaml:"failures" json:"failures"`
	CooldownSeconds int `yaml:"cooldown_seconds" json:"cooldown_seconds"`
}

// Route 是一条有序候选线路。
type Route struct {
	Name       string   `yaml:"name" json:"name"`
	Candidates []string `yaml:"candidates" json:"candidates"`
	Retry      Retry    `yaml:"retry" json:"retry"`
	Circuit    Circuit  `yaml:"circuit" json:"circuit"`
}

// Store 负责读写配置。
type Store struct {
	path string
}

// NewStore 构造配置存储。
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path 返回配置文件路径。
func (s *Store) Path() string { return s.path }

// Load 读取配置；文件不存在时返回空配置。
func (s *Store) Load() (Config, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{Version: 2}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	cfg.normalize()
	return cfg, nil
}

// Save 写入配置（0600）。
func (s *Store) Save(cfg Config) error {
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.path, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(s.path, 0o600)
}

// Legacy 把旧版单供应商配置转换为 v2。
func Legacy(baseURL, apiKey, model, fallback, vision string) Config {
	provider := Provider{Name: "default", BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey}
	cfg := Config{Version: 2, Providers: []Provider{provider}}

	primary := Model{ID: "primary", Provider: "default", Model: model, Capabilities: []string{"text", "tools"}, ContextWindow: 128000, MaxTokens: 8192}
	cfg.Models = append(cfg.Models, primary)
	mainCandidates := []string{"primary"}

	if fallback != "" && fallback != model {
		cfg.Models = append(cfg.Models, Model{ID: "fallback", Provider: "default", Model: fallback, Capabilities: []string{"text", "tools"}, ContextWindow: 128000, MaxTokens: 8192})
		mainCandidates = append(mainCandidates, "fallback")
	}
	cfg.Routes = append(cfg.Routes, Route{Name: "main", Candidates: mainCandidates, Retry: Retry{MaxAttempts: 1, BackoffMS: 200}, Circuit: Circuit{Failures: 3, CooldownSeconds: 30}})

	if vision != "" && vision != model && vision != fallback {
		cfg.Models = append(cfg.Models, Model{ID: "vision", Provider: "default", Model: vision, Capabilities: []string{"text", "vision", "tools"}, ContextWindow: 128000, MaxTokens: 8192})
	}
	visionID := "primary"
	if vision != "" {
		for _, m := range cfg.Models {
			if m.Model == vision {
				visionID = m.ID
			}
		}
	}
	cfg.Routes = append(cfg.Routes, Route{Name: "vision", Candidates: []string{visionID}, Retry: Retry{MaxAttempts: 1, BackoffMS: 200}, Circuit: Circuit{Failures: 3, CooldownSeconds: 30}})
	return cfg
}

func (c *Config) normalize() {
	if c.Version == 0 {
		c.Version = 2
	}
	for i := range c.Models {
		if c.Models[i].ContextWindow <= 0 {
			c.Models[i].ContextWindow = 128000
		}
		if c.Models[i].MaxTokens <= 0 {
			c.Models[i].MaxTokens = 8192
		}
		if len(c.Models[i].Capabilities) == 0 {
			c.Models[i].Capabilities = []string{"text"}
		}
	}
	for i := range c.Routes {
		if c.Routes[i].Retry.MaxAttempts <= 0 {
			c.Routes[i].Retry.MaxAttempts = 1
		}
		if c.Routes[i].Circuit.Failures <= 0 {
			c.Routes[i].Circuit.Failures = 3
		}
		if c.Routes[i].Circuit.CooldownSeconds <= 0 {
			c.Routes[i].Circuit.CooldownSeconds = 30
		}
	}
}

// Validate 校验配置。
func (c Config) Validate() error {
	if len(c.Providers) == 0 {
		return errors.New("至少需要一个供应商")
	}
	providers := map[string]bool{}
	for _, p := range c.Providers {
		if p.Name == "" || p.BaseURL == "" {
			return errors.New("供应商名称与 base_url 不能为空")
		}
		if providers[p.Name] {
			return fmt.Errorf("供应商重名：%s", p.Name)
		}
		providers[p.Name] = true
	}
	models := map[string]bool{}
	for _, m := range c.Models {
		if m.ID == "" || m.Model == "" {
			return errors.New("模型 id 与上游 model 不能为空")
		}
		if models[m.ID] {
			return fmt.Errorf("模型重名：%s", m.ID)
		}
		if !providers[m.Provider] {
			return fmt.Errorf("模型 %s 引用了不存在的供应商 %s", m.ID, m.Provider)
		}
		models[m.ID] = true
	}
	if len(c.Routes) == 0 {
		return errors.New("至少需要一条线路")
	}
	routes := map[string]bool{}
	for _, r := range c.Routes {
		if r.Name == "" {
			return errors.New("线路名不能为空")
		}
		if routes[r.Name] {
			return fmt.Errorf("线路重名：%s", r.Name)
		}
		routes[r.Name] = true
		if len(r.Candidates) == 0 {
			return fmt.Errorf("线路 %s 没有候选模型", r.Name)
		}
		for _, id := range r.Candidates {
			if !models[id] {
				return fmt.Errorf("线路 %s 引用了不存在的模型 %s", r.Name, id)
			}
		}
	}
	return nil
}

// Route 按名称查找线路。
func (c Config) Route(name string) (Route, bool) {
	for _, r := range c.Routes {
		if r.Name == name {
			return r, true
		}
	}
	return Route{}, false
}

// Model 按 id 查找模型。
func (c Config) Model(id string) (Model, bool) {
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Provider 按名称查找供应商。
func (c Config) Provider(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// Supports 判断模型是否声明了某项能力。
func (m Model) Supports(capability string) bool {
	for _, item := range m.Capabilities {
		if item == capability {
			return true
		}
	}
	return false
}
