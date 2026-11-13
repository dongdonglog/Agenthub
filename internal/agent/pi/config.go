package pi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	ID string `json:"id"`
}

// PrepareAgentDir 生成 models.json / SYSTEM.md 并迁移技能目录。
func PrepareAgentDir(cfg AgentConfig) error {
	if cfg.Dir == "" {
		return fmt.Errorf("agent dir 为空")
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return err
	}

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
