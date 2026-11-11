// Package mcp 提供 MCP（Model Context Protocol）stdio 客户端与工具适配。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// protocolVersion 是客户端声明的 MCP 版本；服务端可返回其支持的版本。
const protocolVersion = "2024-11-05"

// ToolInfo 是 MCP 服务端声明的工具。
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Client 是一个 MCP stdio 连接。
type Client struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	log     *slog.Logger
	mu      sync.Mutex
	nextID  atomic.Int64
	pending map[int64]chan rpcReply
	closed  bool

	tools []ToolInfo
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Start 启动 MCP 服务端进程并完成初始化。
//
// @param name 服务名，用于工具前缀
// @param command 可执行文件
// @param args 参数
// @param env 追加环境变量，形如 KEY=VALUE
// @throws 进程启动失败、初始化或 tools/list 失败时返回错误
func Start(ctx context.Context, name, command string, args, env []string, log *slog.Logger) (*Client, error) {
	if command == "" {
		return nil, errors.New("MCP command 为空")
	}
	if log == nil {
		log = slog.Default()
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 MCP %s 失败: %w", name, err)
	}

	c := &Client{name: name, cmd: cmd, stdin: stdin, log: log, pending: make(map[int64]chan rpcReply)}
	go c.readLoop(stdout)
	go func() {
		// 服务端 stderr 只写日志，不进入模型上下文。
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			log.Debug("mcp stderr", "server", name, "line", scanner.Text())
		}
	}()

	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := c.request(initCtx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agenthub", "version": "0.1.0"},
	}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("MCP %s 初始化失败: %w", name, err)
	}
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		_ = c.Close()
		return nil, err
	}

	raw, err := c.request(initCtx, "tools/list", map[string]any{})
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("MCP %s tools/list 失败: %w", name, err)
	}
	var listed struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		_ = c.Close()
		return nil, err
	}
	c.tools = listed.Tools
	log.Info("mcp ready", "server", name, "tools", len(c.tools))
	return c, nil
}

// Tools 返回服务端声明的工具。
func (c *Client) Tools() []ToolInfo { return c.tools }

// CallTool 调用一个工具并返回文本结果。
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var arguments any
	if err := json.Unmarshal(args, &arguments); err != nil {
		return "", err
	}
	raw, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return string(raw), nil
	}
	parts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		if item.Type == "text" && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	if len(parts) == 0 {
		return string(raw), nil
	}
	text := strings.Join(parts, "\n")
	if result.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

// Close 终止服务端进程；重复调用无副作用。
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}

func (c *Client) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg struct {
			ID     *int64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(line, &msg); err != nil || msg.ID == nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[*msg.ID]
		delete(c.pending, *msg.ID)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if msg.Error != nil {
			ch <- rpcReply{err: fmt.Errorf("MCP 错误 %d: %s", msg.Error.Code, msg.Error.Message)}
			continue
		}
		ch <- rpcReply{result: msg.Result}
	}
	// 进程退出：唤醒所有等待者。
	c.mu.Lock()
	for id, ch := range c.pending {
		ch <- rpcReply{err: errors.New("MCP 连接已关闭")}
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan rpcReply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("MCP 连接已关闭")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		return nil, err
	}

	select {
	case reply := <-ch:
		return reply.result, reply.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *Client) notify(method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(raw, '\n'))
	return err
}
