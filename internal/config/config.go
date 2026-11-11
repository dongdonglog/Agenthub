// Package config 负责加载并校验运行配置。
//
// 优先级从高到低为环境变量、配置文件、内置默认值。加载失败时返回带字段名的错误，
// 错误信息不包含字段值，避免密钥进入日志。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ChannelMode 是消息通道模式。
type ChannelMode string

const (
	// ChannelOff 只启动服务骨架（健康检查、管理面），不接消息。
	ChannelOff ChannelMode = "off"
	// ChannelFeishu 接入飞书自建应用。
	ChannelFeishu ChannelMode = "feishu"
)

// Config 是服务的全部运行配置。
type Config struct {
	Env     string        `yaml:"env"`
	Channel ChannelMode   `yaml:"channel"`
	HTTP    HTTPConfig    `yaml:"http"`
	Admin   AdminConfig   `yaml:"admin"`
	DB      DBConfig      `yaml:"db"`
	Storage StorageConfig `yaml:"storage"`
	// RetentionDays 是历史数据保留天数；0 表示不清理。
	RetentionDays int           `yaml:"retention_days"`
	Webhook       WebhookConfig `yaml:"webhook"`
	Feishu        FeishuConfig  `yaml:"feishu"`
	Agent         AgentConfig   `yaml:"agent"`
	Log           LogConfig     `yaml:"log"`
}

// HTTPConfig 是健康检查与指标监听配置。
type HTTPConfig struct {
	Addr string `yaml:"addr"`
}

// AdminConfig 是管理面监听配置。
//
// @remarks 管理面没有内置登录，部署时必须限制来源（内网或网关）。
type AdminConfig struct {
	Addr string `yaml:"addr"`
	// Token 非空时管理面要求 Bearer Token 或登录 Cookie；为空表示不鉴权。
	Token string `yaml:"token"`
}

// DBConfig 是 PostgreSQL 连接配置。
type DBConfig struct {
	URL      string `yaml:"url"`
	MaxConns int32  `yaml:"max_conns"`
}

// StorageConfig 是附件与产物存储配置。
type StorageConfig struct {
	Dir string `yaml:"dir"`
}

// WebhookConfig 是飞书事件回调监听配置。
//
// @remarks 该监听需要被飞书访问，部署时应前置网关并限制来源；
// 其余监听保持回环。
type WebhookConfig struct {
	Addr string `yaml:"addr"`
}

// FeishuConfig 是飞书通道配置。
type FeishuConfig struct {
	// Transport 是事件传输方式：websocket（长连接，默认）或 webhook（回调地址）。
	Transport string `yaml:"transport"`
	// Domain 是开放平台地址，Lark 海外部署可覆盖。
	Domain            string   `yaml:"domain"`
	AppID             string   `yaml:"app_id"`
	AppSecret         string   `yaml:"app_secret"`
	VerificationToken string   `yaml:"verification_token"`
	EncryptKey        string   `yaml:"encrypt_key"`
	BotOpenID         string   `yaml:"bot_open_id"`
	AllowedUsers      []string `yaml:"allowed_users"`
	AllowedChats      []string `yaml:"allowed_chats"`
	// RequireMention 为 true 时群聊必须 @机器人 才触发。
	RequireMention bool `yaml:"require_mention"`
	// MentionFreeChats 中的群聊不要求 @机器人（用于测试群）。
	MentionFreeChats []string `yaml:"mention_free_chats"`
	// CardActionEnabled 为 true 时卡片带停止按钮，并启用回调入口。
	CardActionEnabled bool `yaml:"card_action_enabled"`
}

// AgentConfig 是模型与执行配置。
type AgentConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	// BaseURL 是 OpenAI 兼容接口地址，例如 https://ark.cn-beijing.volces.com/api/v3。
	BaseURL string `yaml:"base_url"`
	// FallbackModel 在主模型失败时兜底，可为空。
	FallbackModel string `yaml:"fallback_model"`
	// VisionModel 用于带图片的运行；为空表示不启用图片理解。
	VisionModel string `yaml:"vision_model"`
	// APIKey 为空时退回回显执行器，不调用模型。
	APIKey string `yaml:"api_key"`
	// SystemPrompt 是基础系统提示词；技能内容会追加在其后。
	SystemPrompt string `yaml:"system_prompt"`
	// SkillsDir 是 Markdown 技能目录。
	SkillsDir string `yaml:"skills_dir"`
	// MaxRunSeconds 是单次运行上限。
	MaxRunSeconds int `yaml:"max_run_seconds"`
}

// LogConfig 是日志配置。
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Load 读取配置文件并应用环境变量覆盖。
//
// @param path 配置文件路径；为空或文件不存在时只使用默认值与环境变量。
// @returns 已校验的配置；校验失败时返回错误。
func Load(path string) (*Config, error) {
	cfg := defaults()
	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(raw, &cfg); err != nil {
				return nil, fmt.Errorf("配置文件解析失败 %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// 允许缺失：容器里常见只给环境变量。
		default:
			return nil, fmt.Errorf("配置文件读取失败 %s: %w", path, err)
		}
	}
	applyEnv(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func defaults() Config {
	return Config{
		Env:           "development",
		Channel:       ChannelOff,
		HTTP:          HTTPConfig{Addr: "127.0.0.1:8787"},
		Admin:         AdminConfig{Addr: "127.0.0.1:8788"},
		DB:            DBConfig{MaxConns: 10},
		Storage:       StorageConfig{Dir: "./data"},
		RetentionDays: 90,
		Webhook:       WebhookConfig{Addr: "127.0.0.1:8789"},
		Feishu: FeishuConfig{
			Transport:      "websocket",
			Domain:         "https://open.feishu.cn",
			RequireMention: true,
		},
		Agent: AgentConfig{
			Provider:      "openai",
			Model:         "gpt-4.1-mini",
			BaseURL:       "https://api.openai.com/v1",
			MaxRunSeconds: 1800,
		},
		Log: LogConfig{Level: "info", Format: "json"},
	}
}

func applyEnv(cfg *Config) {
	setStr(&cfg.Env, "AGENTHUB_ENV")
	setStr((*string)(&cfg.Channel), "AGENTHUB_CHANNEL")
	setStr(&cfg.HTTP.Addr, "AGENTHUB_HTTP_ADDR")
	setStr(&cfg.Admin.Addr, "AGENTHUB_ADMIN_ADDR")
	setStr(&cfg.Admin.Token, "AGENTHUB_ADMIN_TOKEN")
	setStr(&cfg.DB.URL, "AGENTHUB_DB_URL")
	setInt32(&cfg.DB.MaxConns, "AGENTHUB_DB_MAX_CONNS")
	setStr(&cfg.Storage.Dir, "AGENTHUB_DATA_DIR")
	setInt(&cfg.RetentionDays, "AGENTHUB_RETENTION_DAYS")
	setStr(&cfg.Webhook.Addr, "AGENTHUB_WEBHOOK_ADDR")
	setStr(&cfg.Feishu.Transport, "FEISHU_TRANSPORT")
	setStr(&cfg.Feishu.Domain, "FEISHU_DOMAIN")
	setStr(&cfg.Feishu.BotOpenID, "FEISHU_BOT_OPEN_ID")
	setStr(&cfg.Feishu.AppID, "FEISHU_APP_ID")
	setStr(&cfg.Feishu.AppSecret, "FEISHU_APP_SECRET")
	setStr(&cfg.Feishu.VerificationToken, "FEISHU_VERIFICATION_TOKEN")
	setStr(&cfg.Feishu.EncryptKey, "FEISHU_ENCRYPT_KEY")
	setList(&cfg.Feishu.AllowedUsers, "FEISHU_ALLOWED_USERS")
	setList(&cfg.Feishu.AllowedChats, "FEISHU_ALLOWED_CHATS")
	setBool(&cfg.Feishu.RequireMention, "FEISHU_REQUIRE_MENTION")
	setList(&cfg.Feishu.MentionFreeChats, "FEISHU_MENTION_FREE_CHATS")
	setBool(&cfg.Feishu.CardActionEnabled, "FEISHU_CARD_ACTION")
	setStr(&cfg.Agent.Provider, "AGENTHUB_AGENT_PROVIDER")
	setStr(&cfg.Agent.Model, "AGENTHUB_AGENT_MODEL")
	setStr(&cfg.Agent.BaseURL, "AGENTHUB_AGENT_BASE_URL")
	setStr(&cfg.Agent.FallbackModel, "AGENTHUB_AGENT_FALLBACK_MODEL")
	setStr(&cfg.Agent.VisionModel, "AGENTHUB_AGENT_VISION_MODEL")
	setStr(&cfg.Agent.APIKey, "AGENTHUB_AGENT_API_KEY")
	setStr(&cfg.Agent.SystemPrompt, "AGENTHUB_AGENT_SYSTEM_PROMPT")
	setStr(&cfg.Agent.SkillsDir, "AGENTHUB_AGENT_SKILLS_DIR")
	setInt(&cfg.Agent.MaxRunSeconds, "AGENTHUB_AGENT_MAX_RUN_SECONDS")
	setStr(&cfg.Log.Level, "AGENTHUB_LOG_LEVEL")
	setStr(&cfg.Log.Format, "AGENTHUB_LOG_FORMAT")
}

// Validate 校验配置的必要字段与取值范围。
//
// @throws 字段缺失或取值非法时返回错误，错误只包含字段名与原因。
func (c *Config) Validate() error {
	if c.HTTP.Addr == "" {
		return errors.New("http.addr 不能为空")
	}
	if c.Admin.Addr == "" {
		return errors.New("admin.addr 不能为空")
	}
	if c.Webhook.Addr == "" {
		return errors.New("webhook.addr 不能为空")
	}
	if c.Storage.Dir == "" {
		return errors.New("storage.dir 不能为空")
	}
	if c.DB.URL != "" && !strings.HasPrefix(c.DB.URL, "postgres://") && !strings.HasPrefix(c.DB.URL, "postgresql://") {
		return errors.New("db.url 必须是 postgres:// 或 postgresql:// 连接串")
	}
	if c.DB.MaxConns <= 0 {
		return errors.New("db.max_conns 必须为正整数")
	}
	if !validLevel(c.Log.Level) {
		return fmt.Errorf("log.level 非法：%s", c.Log.Level)
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		return fmt.Errorf("log.format 非法：%s", c.Log.Format)
	}
	switch c.Channel {
	case ChannelOff:
	case ChannelFeishu:
		if c.Feishu.AppID == "" {
			return errors.New("feishu.app_id 不能为空")
		}
		if c.Feishu.AppSecret == "" {
			return errors.New("feishu.app_secret 不能为空")
		}
		if c.Feishu.Transport != "websocket" && c.Feishu.Transport != "webhook" {
			return fmt.Errorf("feishu.transport 非法：%s", c.Feishu.Transport)
		}
		if c.Feishu.Transport == "websocket" && c.Feishu.Domain == "" {
			return errors.New("feishu.domain 不能为空")
		}
	default:
		return fmt.Errorf("channel 非法：%s", c.Channel)
	}
	if c.RetentionDays < 0 {
		return errors.New("retention_days 不能为负数")
	}
	if c.Agent.MaxRunSeconds <= 0 {
		return errors.New("agent.max_run_seconds 必须为正整数")
	}
	return nil
}

func validLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}

func setStr(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func setInt(dst *int, key string) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func setInt32(dst *int32, key string) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = int32(n)
		}
	}
}

func setBool(dst *bool, key string) {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

func setList(dst *[]string, key string) {
	if v := os.Getenv(key); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		*dst = out
	}
}
