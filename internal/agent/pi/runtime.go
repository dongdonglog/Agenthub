package pi

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// ModelFunc 返回当前主模型；为空时使用 Model。
	ModelFunc func() string
	// Extensions / Skills / Prompts 是随运行时加载的 pi 资源。
	Extensions []string
	Skills     []string
	Prompts    []string
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
		cfg.Tools = "read,bash,edit,write,grep,find,ls,tool_search"
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
	for i, path := range cfg.Extensions {
		if abs, err := filepath.Abs(path); err == nil {
			cfg.Extensions[i] = abs
		}
	}
	for i, path := range cfg.Skills {
		if abs, err := filepath.Abs(path); err == nil {
			cfg.Skills[i] = abs
		}
	}
	for i, path := range cfg.Prompts {
		if abs, err := filepath.Abs(path); err == nil {
			cfg.Prompts[i] = abs
		}
	}
	for _, key := range []string{"AgentDir", "SessionDir", "WorkspaceDir"} {
		var value string
		switch key {
		case "AgentDir":
			value = cfg.AgentDir
		case "SessionDir":
			value = cfg.SessionDir
		case "WorkspaceDir":
			value = cfg.WorkspaceDir
		}
		if value == "" {
			continue
		}
		if abs, err := filepath.Abs(value); err == nil {
			switch key {
			case "AgentDir":
				cfg.AgentDir = abs
			case "SessionDir":
				cfg.SessionDir = abs
			case "WorkspaceDir":
				cfg.WorkspaceDir = abs
			}
		}
	}
	return &Runtime{cfg: cfg}
}

// guardExtension 返回 guard 扩展的路径，供子调查进程复用。
//
// @remarks 按文件名匹配而不是取切片第一个元素：扩展清单的顺序属于装配细节，
// 让它承载「哪一个是 guard」的语义，一旦顺序变化就会静默失效——
// 只读模式还在，但子调查不再受同一个 guard 约束。
func (r *Runtime) guardExtension() string {
	for _, path := range r.cfg.Extensions {
		if strings.Contains(filepath.Base(path), "guard") {
			return path
		}
	}
	return ""
}

// Cleanup 删除某个会话代的工作区与会话文件。
func (r *Runtime) Cleanup(conv string, generation int) error {
	base := sessionKey(conv, generation)
	ws := filepath.Join(r.cfg.WorkspaceDir, base)
	session := filepath.Join(r.cfg.SessionDir, base+".jsonl")
	if err := os.RemoveAll(ws); err != nil {
		return err
	}
	if err := os.Remove(session); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Run 执行一个批次：预写会话文件、启动 pi、等待收敛。
func (r *Runtime) Run(ctx context.Context, in agent.Input) (agent.Output, error) {
	texts := make([]string, 0, len(in.Messages))
	for _, m := range in.Messages {
		texts = append(texts, m.Text)
	}
	if len(texts) == 0 {
		return agent.Output{}, errors.New("pi 运行缺少输入")
	}

	base := sessionKey(in.ConversationID, in.Generation)
	ws := filepath.Join(r.cfg.WorkspaceDir, base)
	session := filepath.Join(r.cfg.SessionDir, base+".jsonl")

	if err := ensureWorkspace(ws, r.cfg.AgentDir, ""); err != nil {
		return agent.Output{}, err
	}
	if err := applyProfileSelection(r.cfg.AgentDir, ws, in.SkillAllow, in.MCPAllow); err != nil {
		return agent.Output{}, fmt.Errorf("应用 Profile 选择失败: %w", err)
	}
	if err := linkSkillExecutables(ws); err != nil {
		return agent.Output{}, fmt.Errorf("同步 Skill 可执行文件失败: %w", err)
	}
	started := time.Now()
	reportDir := filepath.Join(ws, "reports")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return agent.Output{}, err
	}
	_ = os.Remove(filepath.Join(reportDir, "report.json"))
	// 审批申请目录每次运行清空，避免上一批的申请被重复提交。
	approvalDir := filepath.Join(ws, "approvals")
	if err := os.RemoveAll(approvalDir); err != nil {
		return agent.Output{}, err
	}
	if err := os.MkdirAll(approvalDir, 0o755); err != nil {
		return agent.Output{}, err
	}
	if err := prepareRunOutbox(ws, in.RunID); err != nil {
		return agent.Output{}, err
	}
	attachments := linkAttachments(ws, in.Messages)
	senders := make([]string, 0, len(in.Messages))
	for _, m := range in.Messages {
		name := strings.TrimSpace(m.SenderName)
		if name == "" {
			name = strings.TrimSpace(m.Sender)
		}
		if name == "" {
			continue
		}
		if m.SentByAdmin {
			name += "（管理员干预）"
		}
		senders = append(senders, name)
	}
	agentsMD := buildAgentsMD(in.ConversationID, in.Generation, attachments, senders)
	if err := ensureAgentsFile(ws, agentsMD); err != nil {
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

	out, err := r.runProcess(ctx, ws, session, prompt, in, agentsMD)
	if errors.Is(err, errAborted) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		_ = appendAbortMarker(session, in.RunID, "aborted")
	}
	if err == nil {
		out.Artifacts = collectRunArtifacts(ws, in.RunID, started)
		out.Approvals = collectApprovalRequests(approvalDir)
		if raw, readErr := os.ReadFile(filepath.Join(reportDir, "report.json")); readErr == nil {
			out.Report = raw
		} else if strings.TrimSpace(out.Text) != "" && in.CaseID != "" {
			out.Report, _ = json.Marshal(map[string]string{
				"problem":    "诊断已完成，待人工确认报告",
				"evidence":   out.Text,
				"root_cause": "",
			})
		}
	}
	return out, err
}

// collectApprovalRequests 读取 pi 提交的动作申请。
//
// @remarks 只接受本次运行目录下的普通 .json 文件；出错或超大文件直接跳过，
// 让调用方按空申请处理，不影响主运行结果。
func collectApprovalRequests(dir string) []json.RawMessage {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []json.RawMessage
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 64<<10 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		if !json.Valid(raw) {
			continue
		}
		out = append(out, json.RawMessage(raw))
	}
	return out
}

func sessionKey(conv string, generation int) string {
	sum := sha1.Sum([]byte(conv))
	return hex.EncodeToString(sum[:])[:16] + fmt.Sprintf("-g%d", generation)
}

// mergeEnv 把 base 与 override 合并为 KEY=VALUE 列表，同名时 override 生效。
//
// @remarks 子进程环境必须是完整的（否则会丢掉 HOME 等），所以以 os.Environ() 为底；
// 但同一批注入的变量必须真的生效，不能被基底里的同名值覆盖后再拼接出重复项。
func mergeEnv(base, override []string) []string {
	merged := make(map[string]string, len(base)+len(override))
	order := make([]string, 0, len(base)+len(override))
	put := func(item string) {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			return
		}
		if _, seen := merged[key]; !seen {
			order = append(order, key)
		}
		merged[key] = item
	}
	for _, item := range base {
		put(item)
	}
	for _, item := range override {
		put(item)
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, merged[key])
	}
	return out
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
	Usage      struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	} `json:"usage"`
}

func (r *Runtime) runProcess(ctx context.Context, ws, session, prompt string, in agent.Input, agentsMD string) (agent.Output, error) {
	args := make([]string, 0, 12)
	bin := r.cfg.Bin
	if r.cfg.Isolation == "namespace" && runtime.GOOS == "linux" {
		args = append(args, "--user", "--map-root-user", "--pid", "--fork", bin)
		bin = "unshare"
	}
	model := r.cfg.Model
	if r.cfg.ModelFunc != nil {
		if current := r.cfg.ModelFunc(); current != "" {
			model = current
		}
	}
	provider := r.cfg.Provider
	if provider == "" {
		provider = "agenthub"
	}
	if model != "" {
		args = append(args, "--provider", provider, "--model", model)
	}
	tools := r.cfg.Tools
	if len(in.ToolAllow) > 0 {
		tools = strings.Join(in.ToolAllow, ",")
	}
	args = append(args, "--mode", "rpc", "--session", session, "--tools", tools,
		"--no-context-files", "--append-system-prompt", agentsMD)
	for _, extension := range r.cfg.Extensions {
		args = append(args, "--extension", extension)
	}
	for _, skill := range r.cfg.Skills {
		args = append(args, "--skill", skill)
	}
	for _, prompt := range r.cfg.Prompts {
		args = append(args, "--prompt-template", prompt)
	}
	if !r.cfg.Approve {
		args = append(args, "--no-approve")
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = ws
	pathValue := filepath.Join(ws, ".agenthub-bin")
	if current := os.Getenv("PATH"); current != "" {
		pathValue += string(os.PathListSeparator) + current
	}
	env := []string{
		"PATH=" + pathValue,
		"PI_PROVIDER=" + provider,
		"PI_MODEL=" + model,
		"PI_BIN=" + r.cfg.Bin,
		"PI_CODING_AGENT_DIR=" + filepath.Join(ws, ".agenthub-agent"),
		"AGENTHUB_OUTBOX_DIR=" + filepath.Join(ws, "outbox"),
		"AGENTHUB_REPORT_DIR=" + filepath.Join(ws, "reports"),
		"AGENTHUB_APPROVAL_DIR=" + filepath.Join(ws, "approvals"),
		"AGENTHUB_CASE_ID=" + in.CaseID,
	}
	if guard := r.guardExtension(); guard != "" {
		env = append(env, "AGENTHUB_GUARD_EXTENSION="+guard)
	}
	if in.ReadOnly {
		env = append(env, "AGENTHUB_READONLY=1")
	}
	cmd.Env = mergeEnv(os.Environ(), env)
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
	var stderrTail string
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := sc.Text()
			stderrTail = truncate(line, 500)
			r.cfg.Logger.Debug("pi stderr", "line", truncate(line, 300))
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
		if err := sc.Err(); err != nil {
			lines <- lineMsg{err: err}
			return
		}
		lines <- lineMsg{err: io.EOF}
	}()

	if err := sendRequest(write, "prompt", map[string]any{"message": prompt}); err != nil {
		return agent.Output{}, err
	}

	var final []string
	var terminalErr string
	var usage agent.Usage
	settled := false
	timer := time.NewTimer(r.cfg.Timeout)
	defer timer.Stop()

	settle := func(aborted bool) (agent.Output, error) {
		settled = true
		text := strings.Join(final, "\n\n")
		switch {
		case aborted:
			return agent.Output{Text: text, Usage: usage}, errAborted
		case terminalErr != "":
			return agent.Output{Text: text, Usage: usage}, fmt.Errorf("pi 运行失败: %s", terminalErr)
		case strings.TrimSpace(text) == "":
			return agent.Output{Usage: usage}, errors.New("pi 未返回有效结果")
		default:
			return agent.Output{Text: text, Usage: usage}, nil
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
				if lm.err == io.EOF || lm.err == context.Canceled {
					if terminalErr == "" && len(final) == 0 && stderrTail != "" {
						terminalErr = "进程提前退出：" + stderrTail
					}
					return settle(false)
				}
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
					usage.InputTokens += m.Usage.Input
					usage.OutputTokens += m.Usage.Output
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
