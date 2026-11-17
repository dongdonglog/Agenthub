package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var prefixRE = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

type upstreamSpec struct {
	Name     string            `json:"name"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	Prefix   string            `json:"prefix"`
	ReadOnly bool              `json:"read_only"`
}

type upstream struct {
	spec  upstreamSpec
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
	mu    sync.Mutex
	next  int
	tools []tool
	dead  bool
}

func (s *server) startUpstreams() {
	raw := strings.TrimSpace(os.Getenv("AGENTHUB_MCP_UPSTREAMS"))
	if raw == "" {
		if path := os.Getenv("AGENTHUB_MCP_UPSTREAMS_FILE"); path != "" {
			content, err := os.ReadFile(path)
			if err != nil {
				log.Printf("mcp gateway: read upstream file failed: %v", err)
				return
			}
			raw = strings.TrimSpace(string(content))
		}
	}
	if raw == "" {
		return
	}
	var specs []upstreamSpec
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		log.Printf("mcp gateway: invalid AGENTHUB_MCP_UPSTREAMS: %v", err)
		return
	}
	allowWrite := strings.EqualFold(os.Getenv("AGENTHUB_MCP_ALLOW_WRITE"), "true")
	for _, spec := range specs {
		if !allowWrite && !spec.ReadOnly {
			log.Printf("mcp gateway: skip upstream %s (write tools disabled)", spec.Name)
			continue
		}
		u, err := startUpstream(spec)
		if err != nil {
			log.Printf("mcp gateway: upstream %s failed: %v", spec.Name, err)
			continue
		}
		s.upstreams = append(s.upstreams, u)
		log.Printf("mcp gateway: upstream %s ready tools=%d prefix=%s", spec.Name, len(u.tools), u.prefix())
	}
}

func startUpstream(spec upstreamSpec) (*upstream, error) {
	if spec.Command == "" {
		return nil, fmt.Errorf("command 为空")
	}
	cmd := exec.Command(spec.Command, spec.Args...)
	env := os.Environ()
	for key, value := range spec.Env {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	u := &upstream{spec: spec, cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, 1<<20), next: 1}
	if err := u.initialize(); err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	return u, nil
}

func (u *upstream) prefix() string {
	if u.spec.Prefix != "" {
		return prefixRE.ReplaceAllString(u.spec.Prefix, "_")
	}
	return prefixRE.ReplaceAllString(u.spec.Name, "_")
}

func (u *upstream) initialize() error {
	if _, err := u.request("initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agenthub-gateway", "version": "0.1.0"},
	}); err != nil {
		return err
	}
	if err := u.notify("notifications/initialized", map[string]any{}); err != nil {
		return err
	}
	raw, err := u.request("tools/list", map[string]any{})
	if err != nil {
		return err
	}
	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	for _, item := range result.Tools {
		if item.Name == "" {
			continue
		}
		u.tools = append(u.tools, tool{Name: u.prefix() + "__" + item.Name, Description: item.Description, InputSchema: item.InputSchema})
	}
	return nil
}

func (u *upstream) notify(method string, params any) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	_, err := u.stdin.Write(append(raw, '\n'))
	return err
}

func (u *upstream) request(method string, params any) (json.RawMessage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.dead {
		return nil, fmt.Errorf("upstream %s 不可用", u.spec.Name)
	}
	id := u.next
	u.next++
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := u.stdin.Write(append(raw, '\n')); err != nil {
		u.dead = true
		return nil, err
	}
	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			line, err := u.out.ReadBytes('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			var resp struct {
				ID     any             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *rpcError       `json:"error"`
			}
			if json.Unmarshal(line, &resp) != nil || fmt.Sprint(resp.ID) != fmt.Sprint(id) {
				continue
			}
			if resp.Error != nil {
				ch <- result{err: fmt.Errorf("%s", resp.Error.Message)}
				return
			}
			ch <- result{raw: resp.Result}
			return
		}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			u.dead = true
		}
		return got.raw, got.err
	case <-time.After(15 * time.Second):
		u.dead = true
		_ = u.cmd.Process.Kill()
		return nil, fmt.Errorf("upstream %s 超时", u.spec.Name)
	}
}

func (u *upstream) call(name string, arguments json.RawMessage) (any, error) {
	child := strings.TrimPrefix(name, u.prefix()+"__")
	var result any
	raw, err := u.request("tools/call", map[string]any{"name": child, "arguments": json.RawMessage(arguments)})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (u *upstream) close() {
	if u.cmd != nil && u.cmd.Process != nil {
		_ = u.cmd.Process.Kill()
	}
}
