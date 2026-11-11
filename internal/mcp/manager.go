package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/moying/agenthub/internal/agent"
)

// ServerConfig 是 mcp.yaml 中的一项配置。
type ServerConfig struct {
	Name      string   `yaml:"name" json:"name"`
	Transport string   `yaml:"transport" json:"transport"`
	Command   string   `yaml:"command,omitempty" json:"command,omitempty"`
	Args      []string `yaml:"args,omitempty" json:"args,omitempty"`
	Env       []string `yaml:"env,omitempty" json:"env,omitempty"`
	URL       string   `yaml:"url,omitempty" json:"url,omitempty"`
	Enabled   bool     `yaml:"enabled" json:"enabled"`
}

// LoadConfig 读取 MCP 配置文件；文件缺失时返回空列表。
func LoadConfig(path string) ([]ServerConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []ServerConfig
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Manager 管理所有已启动的 MCP 连接。
type Manager struct {
	clients []*Client
	tools   []agent.Tool
}

// StartAll 启动配置中启用的 stdio 服务并收集工具。
//
// @remarks 单个服务失败只记录日志，不影响其他服务与服务进程启动。
func StartAll(ctx context.Context, path string, log *slog.Logger) *Manager {
	m := &Manager{}
	servers, err := LoadConfig(path)
	if err != nil {
		log.Error("mcp config invalid", "error", err)
		return m
	}
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		if srv.Transport != "stdio" {
			log.Warn("mcp transport not supported yet", "server", srv.Name, "transport", srv.Transport)
			continue
		}
		client, err := Start(ctx, srv.Name, srv.Command, srv.Args, srv.Env, log)
		if err != nil {
			log.Error("mcp start failed", "server", srv.Name, "error", err)
			continue
		}
		m.clients = append(m.clients, client)
		for _, info := range client.Tools() {
			m.tools = append(m.tools, newTool(srv.Name, client, info))
		}
	}
	return m
}

// Tools 返回全部 MCP 工具。
func (m *Manager) Tools() []agent.Tool {
	if m == nil {
		return nil
	}
	return m.tools
}

// Close 关闭全部连接。
func (m *Manager) Close() {
	if m == nil {
		return
	}
	for _, c := range m.clients {
		_ = c.Close()
	}
}

// tool 把 MCP 工具适配为 agent.Tool。
type tool struct {
	alias  string
	client *Client
	info   ToolInfo
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func newTool(server string, client *Client, info ToolInfo) *tool {
	alias := "mcp_" + unsafeChars.ReplaceAllString(server, "_") + "_" + unsafeChars.ReplaceAllString(info.Name, "_")
	if len(alias) > 64 {
		alias = alias[:64]
	}
	return &tool{alias: alias, client: client, info: info}
}

func (t *tool) Name() string { return t.alias }

func (t *tool) Description() string {
	desc := t.info.Description
	if desc == "" {
		desc = t.info.Name
	}
	return desc
}

func (t *tool) Schema() map[string]any {
	if t.info.InputSchema == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return t.info.InputSchema
}

func (t *tool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	return t.client.CallTool(ctx, t.info.Name, args)
}

// ToolNames 返回工具名列表，便于日志与诊断。
func ToolNames(tools []agent.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}

// Summary 返回 "server: n tools" 形式的摘要。
func (m *Manager) Summary() string {
	if m == nil || len(m.clients) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.clients))
	for _, c := range m.clients {
		parts = append(parts, c.name+": "+itoa(len(c.tools))+" tools")
	}
	return strings.Join(parts, ", ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
