package pi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AgentConfig 描述部署级 pi 配置的生成参数。
type AgentConfig struct {
	Dir          string
	BaseURL      string
	APIKey       string
	Model        string
	Fallback     string
	Vision       string
	SystemPrompt string
	SkillsSrc    string
	MCPFile      string
	// RouterURL/RouterToken 非空时使用内置模型路由。
	RouterURL      string
	RouterToken    string
	RouteName      string
	ContextWindow  int
	MaxTokens      int
	SupportsImages bool
}

type modelsFile struct {
	Schema    string                    `json:"$schema,omitempty"`
	Providers map[string]providerConfig `json:"providers"`
}

type providerConfig struct {
	BaseURL string        `json:"baseUrl"`
	API     string        `json:"api"`
	APIKey  string        `json:"apiKey"`
	Models  []modelConfig `json:"models"`
}

type modelConfig struct {
	ID            string   `json:"id"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Input         []string `json:"input,omitempty"`
}

// PrepareAgentDir 生成 models.json / SYSTEM.md 并迁移技能目录。
func PrepareAgentDir(cfg AgentConfig) error {
	if cfg.Dir == "" {
		return fmt.Errorf("agent dir 为空")
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return err
	}

	if cfg.RouterURL != "" {
		route := cfg.RouteName
		if route == "" {
			route = "main"
		}
		window := cfg.ContextWindow
		if window <= 0 {
			window = 128000
		}
		maxTokens := cfg.MaxTokens
		if maxTokens <= 0 {
			maxTokens = 8192
		}
		input := []string{"text"}
		if cfg.SupportsImages {
			input = append(input, "image")
		}
		file := modelsFile{
			Schema: "https://pi.dev/schemas/models.schema.json",
			Providers: map[string]providerConfig{
				"agenthub": {
					BaseURL: cfg.RouterURL,
					API:     "openai-completions",
					APIKey:  cfg.RouterToken,
					Models: []modelConfig{{
						ID: route, ContextWindow: window, MaxTokens: maxTokens, Input: input,
					}},
				},
			},
		}
		raw, _ := json.MarshalIndent(file, "", "  ")
		if err := os.WriteFile(filepath.Join(cfg.Dir, "models.json"), raw, 0o600); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(cfg.Dir, "models-store.json"))
	} else {
		seen := map[string]bool{}
		var models []modelConfig
		for _, id := range []string{cfg.Model, cfg.Fallback, cfg.Vision} {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			models = append(models, modelConfig{ID: id})
		}
		if len(models) > 0 {
			file := modelsFile{
				Schema: "https://pi.dev/schemas/models.schema.json",
				Providers: map[string]providerConfig{
					"agenthub": {BaseURL: cfg.BaseURL, API: "openai-completions", APIKey: cfg.APIKey, Models: models},
				},
			}
			raw, _ := json.MarshalIndent(file, "", "  ")
			if err := os.WriteFile(filepath.Join(cfg.Dir, "models.json"), raw, 0o600); err != nil {
				return err
			}
			_ = os.Remove(filepath.Join(cfg.Dir, "models-store.json"))
		}
	}

	if cfg.MCPFile != "" {
		if raw, err := os.ReadFile(cfg.MCPFile); err == nil {
			var servers []struct {
				Name      string   `yaml:"name"`
				Transport string   `yaml:"transport"`
				Command   string   `yaml:"command"`
				Args      []string `yaml:"args"`
				URL       string   `yaml:"url"`
				Enabled   bool     `yaml:"enabled"`
			}
			if err := yaml.Unmarshal(raw, &servers); err == nil && len(servers) > 0 {
				if err := writeMCPConfig(filepath.Join(cfg.Dir, "mcp.json"), servers); err != nil {
					return err
				}
			}
		}
	}

	if cfg.SystemPrompt != "" {
		if err := os.WriteFile(filepath.Join(cfg.Dir, "SYSTEM.md"), []byte(cfg.SystemPrompt), 0o644); err != nil {
			return err
		}
	}

	skillsDir := filepath.Join(cfg.Dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return err
	}
	if cfg.SkillsSrc != "" && cfg.SkillsSrc != skillsDir {
		if entries, err := os.ReadDir(skillsDir); err == nil && len(entries) == 0 {
			if _, err := os.Stat(cfg.SkillsSrc); err == nil {
				if err := copyTree(cfg.SkillsSrc, skillsDir); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// SystemPrompt 返回内置的角色与输出约定。
func SystemPrompt() string {
	return `你是部署在飞书/钉钉里的团队 AI 助手。

## 角色
- 面向同事，用中文回答；先给结论，再给必要依据。
- 不确定时说明缺少什么，不要编造。

## 输出约定
- 不要输出内部运行编号、工具参数或调试信息。
- 需要多条回复时，用单独一行 ---SPLIT--- 分隔。
- 附件在 attachments/ 下，只读；需要交付的文件写入 outbox/。
- 回答尽量简短；长内容用 Markdown 列表或表格。
`
}

type mcpServerFile struct {
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	URL      string   `json:"url,omitempty"`
	Exposure string   `json:"exposure,omitempty"`
}

type mcpFile struct {
	MCPServers map[string]mcpServerFile `json:"mcpServers"`
}

// writeMCPConfig 把管理台的 MCP 配置转换为 pi 的 mcp.json。
func writeMCPConfig(path string, servers []struct {
	Name      string   `yaml:"name"`
	Transport string   `yaml:"transport"`
	Command   string   `yaml:"command"`
	Args      []string `yaml:"args"`
	URL       string   `yaml:"url"`
	Enabled   bool     `yaml:"enabled"`
}) error {
	out := mcpFile{MCPServers: map[string]mcpServerFile{}}
	for _, srv := range servers {
		if srv.Name == "" || !srv.Enabled {
			continue
		}
		if srv.Transport == "http" && srv.URL != "" {
			out.MCPServers[srv.Name] = mcpServerFile{URL: srv.URL, Exposure: "deferred"}
			continue
		}
		if srv.Command != "" {
			args := make([]string, len(srv.Args))
			for i, arg := range srv.Args {
				// 相对文件路径按部署目录解析，避免 pi 在会话工作区里找不到脚本。
				if arg != "" && !filepath.IsAbs(arg) && !strings.HasPrefix(arg, "-") {
					if _, err := os.Stat(arg); err == nil {
						if abs, err := filepath.Abs(arg); err == nil {
							arg = abs
						}
					}
				}
				args[i] = arg
			}
			out.MCPServers[srv.Name] = mcpServerFile{Command: srv.Command, Args: args, Exposure: "deferred"}
		}
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return os.WriteFile(path, raw, 0o600)
}

// SyncAgentConfig 把部署配置同步到已有会话工作区（模型/系统提示/MCP/技能）。
func SyncAgentConfig(workspacesDir, agentDir string) error {
	entries, err := os.ReadDir(workspacesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	files := []string{"models.json", "SYSTEM.md", "mcp.json"}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		target := filepath.Join(workspacesDir, entry.Name(), ".agenthub-agent")
		if _, err := os.Stat(target); err != nil {
			continue
		}
		for _, name := range files {
			src := filepath.Join(agentDir, name)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			if err := copyFile(src, filepath.Join(target, name)); err != nil {
				return err
			}
		}
		_ = os.Remove(filepath.Join(target, "models-store.json"))
		srcSkills := filepath.Join(agentDir, "skills")
		if _, err := os.Stat(srcSkills); err == nil {
			if err := copyTree(srcSkills, filepath.Join(target, "skills")); err != nil {
				return err
			}
		}
	}
	return nil
}

// CleanupExpired 删除超过保留期的工作区与会话文件。
func CleanupExpired(workspacesDir, sessionDir string, cutoff time.Time) (int, error) {
	removed := 0
	entries, err := os.ReadDir(workspacesDir)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(workspacesDir, entry.Name())); err == nil {
			removed++
		}
	}
	sessions, err := os.ReadDir(sessionDir)
	if err != nil && !os.IsNotExist(err) {
		return removed, err
	}
	for _, entry := range sessions {
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(sessionDir, entry.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}
