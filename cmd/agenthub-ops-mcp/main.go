// agenthub-ops-mcp 暴露只读 Prometheus/Loki 查询工具，供 pi 的 MCP 客户端使用。
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const protocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type server struct {
	promURL   string
	promAuth  string
	lokiURL   string
	lokiAuth  string
	tempoURL  string
	tempoAuth string
	kubectl   string
	http      *http.Client
	tools     []tool
	upstreams []*upstream
	allow     map[string]bool
	deny      map[string]bool
	audit     *os.File
}

func main() {
	s := &server{
		promURL:   strings.TrimRight(os.Getenv("AGENTHUB_PROMETHEUS_URL"), "/"),
		promAuth:  authHeader("AGENTHUB_PROMETHEUS_TOKEN", "AGENTHUB_PROMETHEUS_USER", "AGENTHUB_PROMETHEUS_PASSWORD"),
		lokiURL:   strings.TrimRight(os.Getenv("AGENTHUB_LOKI_URL"), "/"),
		lokiAuth:  authHeader("AGENTHUB_LOKI_TOKEN", "AGENTHUB_LOKI_USER", "AGENTHUB_LOKI_PASSWORD"),
		tempoURL:  strings.TrimRight(os.Getenv("AGENTHUB_TEMPO_URL"), "/"),
		tempoAuth: authHeader("AGENTHUB_TEMPO_TOKEN", "AGENTHUB_TEMPO_USER", "AGENTHUB_TEMPO_PASSWORD"),
		kubectl:   os.Getenv("AGENTHUB_KUBECTL_BIN"),
		http:      &http.Client{Timeout: 20 * time.Second},
	}
	s.loadPolicy()
	s.startUpstreams()
	s.tools = s.filterTools(s.listTools())
	log.SetOutput(os.Stderr)
	log.Printf("ops-mcp ready: prometheus=%v loki=%v tools=%d", s.promURL != "", s.lokiURL != "", len(s.tools))

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		var id any
		_ = json.Unmarshal(req.ID, &id)
		result, err := s.dispatch(req)
		resp := rpcResponse{JSONRPC: "2.0", ID: id}
		if err != nil {
			resp.Error = &rpcError{Code: -32603, Message: err.Error()}
		} else {
			resp.Result = result
		}
		_ = enc.Encode(resp)
	}
}

func (s *server) dispatch(req rpcRequest) (any, error) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agenthub-ops", "version": "0.1.0"},
		}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		return s.call(req.Params)
	default:
		return nil, fmt.Errorf("unknown method %s", req.Method)
	}
}

func (s *server) call(raw json.RawMessage) (any, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	args := map[string]any{}
	if len(params.Arguments) > 0 {
		_ = json.Unmarshal(params.Arguments, &args)
	}
	for _, u := range s.upstreams {
		if strings.HasPrefix(params.Name, u.prefix()+"__") {
			started := time.Now()
			result, err := u.call(params.Name, params.Arguments)
			s.auditCall(params.Name, started, err)
			if err != nil {
				return map[string]any{"content": []any{map[string]string{"type": "text", "text": "上游工具失败: " + err.Error()}}, "isError": true}, nil
			}
			return result, nil
		}
	}
	text, err := s.runTool(params.Name, args)
	if err != nil {
		return map[string]any{"content": []any{map[string]string{"type": "text", "text": "工具执行失败: " + err.Error()}}, "isError": true}, nil
	}
	return map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}}, nil
}

func (s *server) runTool(name string, args map[string]any) (string, error) {
	switch name {
	case "prom_query":
		if s.promURL == "" {
			return "", errors.New("未配置 AGENTHUB_PROMETHEUS_URL")
		}
		q := stringArg(args, "query")
		if q == "" {
			return "", errors.New("query 不能为空")
		}
		params := url.Values{"query": {q}}
		if t := stringArg(args, "time"); t != "" {
			params.Set("time", t)
		}
		return s.fetch(s.promURL+"/api/v1/query", params, s.promAuth)
	case "prom_query_range":
		if s.promURL == "" {
			return "", errors.New("未配置 AGENTHUB_PROMETHEUS_URL")
		}
		q := stringArg(args, "query")
		if q == "" {
			return "", errors.New("query 不能为空")
		}
		params := url.Values{"query": {q}, "start": {stringArg(args, "start")}, "end": {stringArg(args, "end")}, "step": {stringArg(args, "step")}}
		if params.Get("start") == "" || params.Get("end") == "" || params.Get("step") == "" {
			return "", errors.New("start/end/step 不能为空")
		}
		return s.fetch(s.promURL+"/api/v1/query_range", params, s.promAuth)
	case "prom_alerts":
		if s.promURL == "" {
			return "", errors.New("未配置 AGENTHUB_PROMETHEUS_URL")
		}
		return s.fetch(s.promURL+"/api/v1/alerts", nil, s.promAuth)
	case "loki_query":
		if s.lokiURL == "" {
			return "", errors.New("未配置 AGENTHUB_LOKI_URL")
		}
		q := stringArg(args, "query")
		if q == "" {
			return "", errors.New("query 不能为空")
		}
		params := url.Values{"query": {q}}
		for _, key := range []string{"start", "end", "direction"} {
			if value := stringArg(args, key); value != "" {
				params.Set(key, value)
			}
		}
		limit := intArg(args, "limit")
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		params.Set("limit", strconv.Itoa(limit))
		return s.fetch(s.lokiURL+"/loki/api/v1/query_range", params, s.lokiAuth)
	case "loki_labels":
		if s.lokiURL == "" {
			return "", errors.New("未配置 AGENTHUB_LOKI_URL")
		}
		return s.fetch(s.lokiURL+"/loki/api/v1/labels", nil, s.lokiAuth)
	case "tempo_search":
		if s.tempoURL == "" {
			return "", errors.New("未配置 AGENTHUB_TEMPO_URL")
		}
		params := url.Values{"tags": {stringArg(args, "tags")}}
		for _, key := range []string{"start", "end"} {
			if value := stringArg(args, key); value != "" {
				params.Set(key, value)
			}
		}
		limit := intArg(args, "limit")
		if limit <= 0 || limit > 100 {
			limit = 20
		}
		params.Set("limit", strconv.Itoa(limit))
		return s.fetch(s.tempoURL+"/api/search", params, s.tempoAuth)
	case "tempo_trace":
		if s.tempoURL == "" {
			return "", errors.New("未配置 AGENTHUB_TEMPO_URL")
		}
		id := stringArg(args, "trace_id")
		if id == "" {
			return "", errors.New("trace_id 不能为空")
		}
		return s.fetch(s.tempoURL+"/api/traces/"+url.PathEscape(id), nil, s.tempoAuth)
	case "k8s_get":
		if s.kubectl == "" {
			return "", errors.New("未启用 K8s 只读工具")
		}
		return s.kubectlGet(args)
	case "k8s_logs":
		if s.kubectl == "" {
			return "", errors.New("未启用 K8s 只读工具")
		}
		return s.kubectlLogs(args)
	case "k8s_events":
		if s.kubectl == "" {
			return "", errors.New("未启用 K8s 只读工具")
		}
		return s.kubectlEvents(args)
	case "loki_label_values":
		if s.lokiURL == "" {
			return "", errors.New("未配置 AGENTHUB_LOKI_URL")
		}
		label := stringArg(args, "label")
		if label == "" {
			return "", errors.New("label 不能为空")
		}
		return s.fetch(s.lokiURL+"/loki/api/v1/label/"+url.PathEscape(label)+"/values", nil, s.lokiAuth)
	default:
		return "", fmt.Errorf("未知工具 %s", name)
	}
}

func (s *server) fetch(endpoint string, params url.Values, auth string) (string, error) {
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("查询失败 status=%d body=%s", resp.StatusCode, truncate(string(body), 500))
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return truncate(string(body), 4000), nil
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return truncate(string(body), 4000), nil
	}
	return truncate(string(out), 16000), nil
}

func (s *server) listTools() []tool {
	var out []tool
	if s.promURL != "" {
		out = append(out,
			tool{Name: "prom_query", Description: "只读 PromQL 瞬时查询；返回 Prometheus 原始 JSON。", InputSchema: object(map[string]any{
				"query": map[string]any{"type": "string", "description": "PromQL 表达式"},
				"time":  map[string]any{"type": "string", "description": "可选 RFC3339 或 Unix 时间"},
			}, []string{"query"})},
			tool{Name: "prom_query_range", Description: "只读 PromQL 区间查询。", InputSchema: object(map[string]any{
				"query": map[string]any{"type": "string"},
				"start": map[string]any{"type": "string"},
				"end":   map[string]any{"type": "string"},
				"step":  map[string]any{"type": "string"},
			}, []string{"query", "start", "end", "step"})},
			tool{Name: "prom_alerts", Description: "只读读取 Prometheus 当前告警。", InputSchema: object(nil, nil)},
		)
	}
	if s.tempoURL != "" {
		out = append(out,
			tool{Name: "tempo_search", Description: "只读 Tempo trace 搜索，按 tags/时间窗返回 trace 列表。", InputSchema: object(map[string]any{
				"tags":  map[string]any{"type": "string", "description": "TraceQL/标签过滤，例如 service.name=api"},
				"start": map[string]any{"type": "string"},
				"end":   map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer"},
			}, []string{"tags"})},
			tool{Name: "tempo_trace", Description: "只读读取单个 Tempo trace。", InputSchema: object(map[string]any{
				"trace_id": map[string]any{"type": "string"},
			}, []string{"trace_id"})},
		)
	}
	if s.kubectl != "" {
		out = append(out,
			tool{Name: "k8s_get", Description: "只读 kubectl get（资源/命名空间 allowlist，JSON 输出）。", InputSchema: object(map[string]any{
				"resource":  map[string]any{"type": "string"},
				"namespace": map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
			}, []string{"resource"})},
			tool{Name: "k8s_logs", Description: "只读 kubectl logs（最多 1000 行）。", InputSchema: object(map[string]any{
				"pod":       map[string]any{"type": "string"},
				"namespace": map[string]any{"type": "string"},
				"container": map[string]any{"type": "string"},
				"tail":      map[string]any{"type": "integer"},
			}, []string{"pod"})},
			tool{Name: "k8s_events", Description: "只读读取命名空间事件。", InputSchema: object(map[string]any{
				"namespace": map[string]any{"type": "string"},
			}, nil)},
		)
	}
	for _, u := range s.upstreams {
		out = append(out, u.tools...)
	}
	if s.lokiURL != "" {
		out = append(out,
			tool{Name: "loki_query", Description: "只读 LogQL 区间查询；返回 Loki 原始 JSON，limit 最大 500。", InputSchema: object(map[string]any{
				"query":     map[string]any{"type": "string"},
				"start":     map[string]any{"type": "string"},
				"end":       map[string]any{"type": "string"},
				"limit":     map[string]any{"type": "integer"},
				"direction": map[string]any{"type": "string"},
			}, []string{"query"})},
			tool{Name: "loki_labels", Description: "只读列出 Loki 标签。", InputSchema: object(nil, nil)},
			tool{Name: "loki_label_values", Description: "只读列出某个 Loki 标签的值。", InputSchema: object(map[string]any{
				"label": map[string]any{"type": "string"},
			}, []string{"label"})},
		)
	}
	return out
}

var k8sResourceRE = regexp.MustCompile(`^(pods|deployments|statefulsets|daemonsets|services|endpoints|events|nodes|namespaces|replicasets|jobs|cronjobs|configmaps|ingresses|horizontalpodautoscalers)$`)
var k8sNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

func (s *server) kubectlGet(args map[string]any) (string, error) {
	resource := stringArg(args, "resource")
	if !k8sResourceRE.MatchString(resource) {
		return "", errors.New("resource 不在只读 allowlist")
	}
	cmd := []string{"get", resource}
	if name := stringArg(args, "name"); name != "" {
		if !k8sNameRE.MatchString(name) {
			return "", errors.New("name 非法")
		}
		cmd = append(cmd, name)
	}
	if ns := stringArg(args, "namespace"); ns != "" {
		if !k8sNameRE.MatchString(ns) {
			return "", errors.New("namespace 非法")
		}
		cmd = append(cmd, "-n", ns)
	}
	cmd = append(cmd, "-o", "json", "--request-timeout=15s")
	return runKubectl(s.kubectl, cmd)
}

func (s *server) kubectlLogs(args map[string]any) (string, error) {
	pod := stringArg(args, "pod")
	if !k8sNameRE.MatchString(pod) {
		return "", errors.New("pod 非法")
	}
	cmd := []string{"logs", pod}
	if ns := stringArg(args, "namespace"); ns != "" {
		if !k8sNameRE.MatchString(ns) {
			return "", errors.New("namespace 非法")
		}
		cmd = append(cmd, "-n", ns)
	}
	if container := stringArg(args, "container"); container != "" {
		if !k8sNameRE.MatchString(container) {
			return "", errors.New("container 非法")
		}
		cmd = append(cmd, "-c", container)
	}
	tail := intArg(args, "tail")
	if tail <= 0 || tail > 1000 {
		tail = 200
	}
	cmd = append(cmd, "--tail", strconv.Itoa(tail), "--timestamps=false")
	return runKubectl(s.kubectl, cmd)
}

func (s *server) kubectlEvents(args map[string]any) (string, error) {
	cmd := []string{"get", "events", "-o", "json", "--request-timeout=15s"}
	if ns := stringArg(args, "namespace"); ns != "" {
		if !k8sNameRE.MatchString(ns) {
			return "", errors.New("namespace 非法")
		}
		cmd = append(cmd, "-n", ns)
	}
	return runKubectl(s.kubectl, cmd)
}

func runKubectl(bin string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl 失败: %v: %s", err, truncate(string(out), 500))
	}
	return truncate(string(out), 16000), nil
}

func object(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func authHeader(tokenEnv, userEnv, passEnv string) string {
	if token := os.Getenv(tokenEnv); token != "" {
		return "Bearer " + token
	}
	user, pass := os.Getenv(userEnv), os.Getenv(passEnv)
	if user != "" || pass != "" {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	return ""
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func intArg(args map[string]any, key string) int {
	switch value := args[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	}
	return 0
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n…(已截断)"
}
