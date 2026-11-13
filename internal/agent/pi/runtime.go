package pi

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moying/agenthub/internal/agent"
)

// Config 是 pi 运行时配置。
type Config struct {
	Bin          string
	Provider     string
	Model        string
	Tools        string
	AgentDir     string
	SessionDir   string
	WorkspaceDir string
	Isolation    string
	Approve      bool
	Timeout      time.Duration
	// AbortGrace 是发出 abort 后等待进程收敛的时间。
	AbortGrace time.Duration
	Logger     *slog.Logger
}

// Runtime 用 pi RPC 执行一次运行。
type Runtime struct {
	cfg Config
}

// New 构造 pi 运行时。
func New(cfg Config) *Runtime {
	if cfg.Bin == "" {
		cfg.Bin = "pi"
	}
	if cfg.Tools == "" {
		cfg.Tools = "read,bash,grep,find,ls,tool_search"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.AbortGrace <= 0 {
		cfg.AbortGrace = 3 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Runtime{cfg: cfg}
}

// Run 执行一个批次：预写会话文件、启动 pi、等待收敛。
func (r *Runtime) Run(ctx context.Context, in agent.Input) (agent.Output, error) {
	texts := make([]string, 0, len(in.Messages))
	attachments := make([]string, 0)
	for _, m := range in.Messages {
		texts = append(texts, m.Text)
		for _, a := range m.Attachments {
			if a.Name != "" {
				attachments = append(attachments, a.Name)
			}
		}
	}
	if len(texts) == 0 {
		return agent.Output{}, errors.New("pi 运行缺少输入")
	}

	base := sessionKey(in.ConversationID, in.Generation)
	ws := filepath.Join(r.cfg.WorkspaceDir, base)
	session := filepath.Join(r.cfg.SessionDir, base+".jsonl")

	if err := ensureWorkspace(ws, r.cfg.AgentDir, buildAgentsMD(in.ConversationID, in.Generation, attachments)); err != nil {
		return agent.Output{}, err
	}
	_, statErr := os.Stat(session)
	created := errors.Is(statErr, os.ErrNotExist)
	if _, err := ensureSessionFile(session, ws); err != nil {
		return agent.Output{}, err
	}
	if err := validateSession(session, ws, created); err != nil {
		return agent.Output{}, err
	}

	prepared := texts[:len(texts)-1]
	if len(prepared) > 0 {
		if _, err := appendUserMessages(session, prepared); err != nil {
			return agent.Output{}, err
		}
	}
	if err := appendBatchMarker(session, len(texts)); err != nil {
		return agent.Output{}, err
	}
	prompt := texts[len(texts)-1]

	out, err := r.runProcess(ctx, ws, session, prompt, in)
	if errors.Is(err, errAborted) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		_ = appendAbortMarker(session, in.RunID, "aborted")
	}
	return out, err
}

func sessionKey(conv string, generation int) string {
	sum := sha1.Sum([]byte(conv))
	return hex.EncodeToString(sum[:])[:16] + fmt.Sprintf("-g%d", generation)
}

var errAborted = errors.New("pi 运行被中止")

type rpcResponse struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

type rpcEvent struct {
	Type                  string          `json:"type"`
	Aborted               bool            `json:"aborted"`
	AssistantMessageEvent json.RawMessage `json:"assistantMessageEvent"`
	Message               json.RawMessage `json:"message"`
	ToolName              string          `json:"toolName"`
	ToolCallID            string          `json:"toolCallId"`
	IsError               bool            `json:"isError"`
	DurationMs            int64           `json:"durationMs"`
}

type assistantEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type assistantMessage struct {
	Role       string          `json:"role"`
	StopReason string          `json:"stopReason"`
	Content    json.RawMessage `json:"content"`
}

func (r *Runtime) runProcess(ctx context.Context, ws, session, prompt string, in agent.Input) (agent.Output, error) {
	args := make([]string, 0, 12)
	bin := r.cfg.Bin
	if r.cfg.Isolation == "namespace" && runtime.GOOS == "linux" {
		args = append(args, "--user", "--map-root-user", "--pid", "--fork", bin)
		bin = "unshare"
	}
	if r.cfg.Provider != "" && r.cfg.Model != "" {
		args = append(args, "--provider", r.cfg.Provider, "--model", r.cfg.Model)
	}
	args = append(args, "--mode", "rpc", "--session", session, "--tools", r.cfg.Tools)
	if !r.cfg.Approve {
		args = append(args, "--no-approve")
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = ws
	cmd.Env = append(os.Environ(),
		"PI_CODING_AGENT_DIR="+filepath.Join(ws, ".agenthub-agent"),
		"AGENTHUB_OUTBOX_DIR="+filepath.Join(ws, "outbox"),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Output{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agent.Output{}, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agent.Output{}, err
	}
	if err := cmd.Start(); err != nil {
		return agent.Output{}, fmt.Errorf("pi 启动失败: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = stdin.Write(append(raw, '\n'))
		return err
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			r.cfg.Logger.Debug("pi stderr", "line", truncate(sc.Text(), 300))
		}
	}()

	type lineMsg struct {
		line []byte
		err  error
	}
	lines := make(chan lineMsg, 32)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 8<<20)
		for sc.Scan() {
			lines <- lineMsg{line: append([]byte(nil), sc.Bytes()...)}
		}
		lines <- lineMsg{err: sc.Err()}
	}()

	if err := sendRequest(write, "prompt", map[string]any{"message": prompt}); err != nil {
		return agent.Output{}, err
	}

	var final []string
	var terminalErr string
	settled := false
	timer := time.NewTimer(r.cfg.Timeout)
	defer timer.Stop()

	settle := func(aborted bool) (agent.Output, error) {
		settled = true
		text := strings.Join(final, "\n\n")
		switch {
		case aborted:
			return agent.Output{Text: text}, errAborted
		case terminalErr != "":
			return agent.Output{Text: text}, fmt.Errorf("pi 运行失败: %s", terminalErr)
		case strings.TrimSpace(text) == "":
			return agent.Output{}, errors.New("pi 未返回有效结果")
		default:
			return agent.Output{Text: text}, nil
		}
	}

	for {
		select {
		case <-ctx.Done():
			r.abort(write, cmd)
			return settle(true)
		case <-timer.C:
			r.abort(write, cmd)
			return settle(true)
		case lm := <-lines:
			if lm.err != nil {
				return settle(false)
			}
			var ev rpcEvent
			if json.Unmarshal(lm.line, &ev) != nil {
				continue
			}
			switch ev.Type {
			case "agent_start":
				progress(in, "正在分析")
			case "message_update":
				var inner assistantEvent
				if json.Unmarshal(ev.AssistantMessageEvent, &inner) == nil && inner.Type == "text_delta" {
					// 只为进度展示，不累积最终结果。
					progress(in, "正在生成")
				}
			case "tool_execution_start":
				if in.OnTool != nil {
					in.OnTool(ev.ToolName, 0, nil)
				}
				progress(in, "正在使用工具")
			case "message_end":
				var m assistantMessage
				if json.Unmarshal(ev.Message, &m) == nil && m.Role == "assistant" {
					if m.StopReason == "error" || m.StopReason == "aborted" {
						terminalErr = m.StopReason
					} else if m.StopReason == "stop" {
						terminalErr = ""
						if text := messageText(m.Content); text != "" {
							final = append(final, text)
						}
					}
				}
			case "agent_settled":
				if ev.Aborted {
					return settle(true)
				}
				return settle(false)
			}
			_ = settled
		}
	}
}

func (r *Runtime) abort(write func(any) error, cmd *exec.Cmd) {
	_ = sendRequest(write, "clear_queue", nil)
	_ = sendRequest(write, "abort", nil)
	time.Sleep(r.cfg.AbortGrace)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func sendRequest(write func(any) error, typ string, extra map[string]any) error {
	req := map[string]any{"type": typ, "id": fmt.Sprintf("%d", time.Now().UnixNano())}
	for k, v := range extra {
		req[k] = v
	}
	return write(req)
}

func messageText(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

func progress(in agent.Input, text string) {
	if in.Progress != nil {
		in.Progress(text)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
