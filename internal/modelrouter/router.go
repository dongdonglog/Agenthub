// Package modelrouter 提供 OpenAI 兼容的主备模型路由。
package modelrouter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moying/agenthub/internal/modelcfg"
)

// Metrics 是路由上报的最小指标接口。
type Metrics interface {
	Inc(name string, labels map[string]string)
	Add(name string, delta float64, labels map[string]string)
	Set(name string, value float64, labels map[string]string)
}

// CandidateHealth 是单个候选的健康快照。
type CandidateHealth struct {
	Route         string    `json:"route"`
	Candidate     string    `json:"candidate"`
	Open          bool      `json:"open"`
	Failures      int       `json:"failures"`
	OpenUntil     time.Time `json:"open_until"`
	LastError     string    `json:"last_error"`
	LastLatencyMS int64     `json:"last_latency_ms"`
}

// Router 是回环 OpenAI 兼容路由。
type Router struct {
	cfg     atomic.Value // modelcfg.Config
	token   string
	metrics Metrics
	log     *slog.Logger
	client  *http.Client

	mu       sync.Mutex
	circuits map[string]*circuit

	server   *http.Server
	listener net.Listener
	url      string
}

const (
	firstByteTimeout = 90 * time.Second
	totalTimeout     = 10 * time.Minute
	maxBodyBytes     = 32 << 20
)

// New 构造路由。
func New(cfg modelcfg.Config, token string, metrics Metrics, log *slog.Logger) *Router {
	if log == nil {
		log = slog.Default()
	}
	router := &Router{
		token:    token,
		metrics:  metrics,
		log:      log,
		client:   &http.Client{},
		circuits: map[string]*circuit{},
	}
	router.cfg.Store(cfg)
	return router
}

// Update 热更新线路配置。
func (r *Router) Update(cfg modelcfg.Config) {
	r.cfg.Store(cfg)
}

// Config 返回当前配置。
func (r *Router) Config() modelcfg.Config { return r.cfg.Load().(modelcfg.Config) }

// URL 返回 pi 使用的 base URL。
func (r *Router) URL() string { return r.url }

// Token 返回内部鉴权 token。
func (r *Router) Token() string { return r.token }

// Start 在随机回环端口启动。
func (r *Router) Start() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	r.listener = listener
	r.url = "http://" + listener.Addr().String() + "/v1"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", r.handleChat)
	mux.HandleFunc("GET /v1/models", r.handleModels)
	r.server = &http.Server{Handler: r.auth(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := r.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.log.Error("model router stopped", "error", err)
		}
	}()
	r.log.Info("model router listening", "url", r.url)
	return nil
}

// Close 停止路由。
func (r *Router) Close(ctx context.Context) error {
	if r.server == nil {
		return nil
	}
	return r.server.Shutdown(ctx)
}

// Health 返回候选健康快照。
func (r *Router) Health() []CandidateHealth {
	cfg := r.Config()
	out := make([]CandidateHealth, 0, len(cfg.Routes)*2)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, route := range cfg.Routes {
		for _, id := range route.Candidates {
			c := r.circuits[route.Name+"|"+id]
			item := CandidateHealth{Route: route.Name, Candidate: id}
			if c != nil {
				c.mu.Lock()
				item.Open = time.Now().Before(c.openUntil)
				item.Failures = c.failures
				item.OpenUntil = c.openUntil
				item.LastError = c.lastError
				item.LastLatencyMS = c.lastMS
				c.mu.Unlock()
			}
			out = append(out, item)
		}
	}
	return out
}

func (r *Router) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.token != "" && req.Header.Get("Authorization") != "Bearer "+r.token {
			writeOpenAIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, req)
	})
}

func (r *Router) handleModels(w http.ResponseWriter, _ *http.Request) {
	cfg := r.Config()
	data := make([]map[string]any, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		data = append(data, map[string]any{"id": route.Name, "object": "model", "owned_by": "agenthub"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (r *Router) handleChat(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), totalTimeout)
	defer cancel()

	body, err := io.ReadAll(io.LimitReader(req.Body, maxBodyBytes))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "读取请求失败")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	routeName, _ := payload["model"].(string)
	cfg := r.Config()
	route, ok := cfg.Route(routeName)
	if !ok {
		writeOpenAIError(w, http.StatusBadRequest, "未知的模型线路："+routeName)
		return
	}
	vision := hasImagePart(payload)
	candidates := make([]modelcfg.Model, 0, len(route.Candidates))
	for _, id := range route.Candidates {
		model, ok := cfg.Model(id)
		if !ok {
			continue
		}
		if vision && !model.Supports("vision") {
			continue
		}
		candidates = append(candidates, model)
	}
	if len(candidates) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "没有满足请求能力的候选模型")
		return
	}

	stream, _ := payload["stream"].(bool)
	var lastErr string
	attempts := 0
	for _, model := range candidates {
		provider, ok := cfg.Provider(model.Provider)
		if !ok {
			continue
		}
		circuit := r.circuitFor(route.Name, model.ID)
		if !circuit.allow(time.Now()) {
			lastErr = "候选处于熔断冷却中"
			r.record(route.Name, model.ID, "circuit_open", 0)
			continue
		}
		attempts++
		started := time.Now()
		payload["model"] = model.Model
		upstreamBody, _ := json.Marshal(payload)

		if stream {
			failed, errMsg := r.tryStream(ctx, w, req, provider, model, upstreamBody, route.Name, attempts, started)
			if !failed {
				circuit.success(time.Since(started).Milliseconds())
				r.metricsAdd(route.Name, model.ID, started)
				return
			}
			circuit.failure(route.Circuit.Failures, time.Duration(route.Circuit.CooldownSeconds)*time.Second, errMsg, time.Since(started).Milliseconds())
			lastErr = errMsg
		} else {
			respBody, status, err := r.tryJSON(ctx, provider, model, upstreamBody)
			if err != nil {
				circuit.failure(route.Circuit.Failures, time.Duration(route.Circuit.CooldownSeconds)*time.Second, err.Error(), time.Since(started).Milliseconds())
				lastErr = err.Error()
				r.record(route.Name, model.ID, "error", time.Since(started).Milliseconds())
				if !retryable(err) {
					break
				}
				continue
			}
			circuit.success(time.Since(started).Milliseconds())
			r.metricsAdd(route.Name, model.ID, started)
			if attempts > 1 {
				r.metrics.Inc("model_router_fallbacks_total", map[string]string{"route": route.Name})
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-AgentHub-Route", route.Name)
			w.Header().Set("X-AgentHub-Model", model.Model)
			w.Header().Set("X-AgentHub-Attempts", fmt.Sprintf("%d", attempts))
			w.WriteHeader(status)
			_, _ = w.Write(respBody)
			return
		}
		if attempts > 1 {
			r.metrics.Inc("model_router_fallbacks_total", map[string]string{"route": route.Name})
		}
	}
	r.log.Warn("model route exhausted", "route", routeName, "attempts", attempts, "error", lastErr)
	writeOpenAIError(w, http.StatusBadGateway, "所有候选模型均失败："+lastErr)
}

func (r *Router) tryJSON(ctx context.Context, provider modelcfg.Provider, model modelcfg.Model, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, 0, &attemptError{message: err.Error(), retry: true}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, &attemptError{message: fmt.Sprintf("上游 %d：%s", resp.StatusCode, truncate(string(raw), 200)), retry: retryableStatus(resp.StatusCode)}
	}
	return raw, resp.StatusCode, nil
}

func (r *Router) tryStream(ctx context.Context, w http.ResponseWriter, req *http.Request, provider modelcfg.Provider, model modelcfg.Model, body []byte, route string, attempts int, started time.Time) (bool, string) {
	reqCtx, cancel := context.WithTimeout(ctx, firstByteTimeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(provider.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return true, err.Error()
	}
	upstream.Header.Set("Content-Type", "application/json")
	if provider.APIKey != "" {
		upstream.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}
	resp, err := r.client.Do(upstream)
	if err != nil {
		r.record(route, model.ID, "error", time.Since(started).Milliseconds())
		return true, err.Error()
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		retry := retryableStatus(resp.StatusCode)
		r.record(route, model.ID, "http_error", time.Since(started).Milliseconds())
		if retry {
			return true, fmt.Sprintf("上游 %d：%s", resp.StatusCode, truncate(string(raw), 200))
		}
		return true, fmt.Sprintf("上游 %d（不可重试）：%s", resp.StatusCode, truncate(string(raw), 200))
	}

	reader := bufio.NewReader(resp.Body)
	if _, err := reader.Peek(1); err != nil {
		resp.Body.Close()
		r.record(route, model.ID, "first_byte", time.Since(started).Milliseconds())
		return true, "首字节前失败：" + err.Error()
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-AgentHub-Route", route)
	w.Header().Set("X-AgentHub-Model", model.Model)
	w.Header().Set("X-AgentHub-Attempts", fmt.Sprintf("%d", attempts))
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
	resp.Body.Close()
	r.record(route, model.ID, "ok", time.Since(started).Milliseconds())
	if attempts > 1 {
		r.metrics.Inc("model_router_fallbacks_total", map[string]string{"route": route})
	}
	return false, ""
}

func (r *Router) circuitFor(route, candidate string) *circuit {
	key := route + "|" + candidate
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.circuits[key]
	if !ok {
		c = &circuit{}
		r.circuits[key] = c
	}
	return c
}

func (r *Router) record(route, candidate, result string, latencyMS int64) {
	r.metrics.Inc("model_router_requests_total", map[string]string{"route": route, "candidate": candidate, "result": result})
	if latencyMS > 0 {
		r.metrics.Add("model_router_latency_ms_total", float64(latencyMS), map[string]string{"route": route, "candidate": candidate})
	}
}

func (r *Router) metricsAdd(route, candidate string, started time.Time) {
	r.metrics.Inc("model_router_requests_total", map[string]string{"route": route, "candidate": candidate, "result": "ok"})
	r.metrics.Add("model_router_latency_ms_total", float64(time.Since(started).Milliseconds()), map[string]string{"route": route, "candidate": candidate})
}

type attemptError struct {
	message string
	retry   bool
}

func (e *attemptError) Error() string { return e.message }

func retryable(err error) bool {
	var ae *attemptError
	if errors.As(err, &ae) {
		return ae.retry
	}
	return true
}

func retryableStatus(status int) bool {
	switch {
	case status == 408, status == 409, status == 429:
		return true
	case status == 401, status == 403:
		return true
	case status >= 500:
		return true
	default:
		return false
	}
}

func hasImagePart(payload map[string]any) bool {
	messages, _ := payload["messages"].([]any)
	for _, item := range messages {
		message, _ := item.(map[string]any)
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range content {
			block, _ := part.(map[string]any)
			if block["type"] == "image_url" {
				return true
			}
		}
	}
	return false
}

func writeOpenAIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": "model_router_error"}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}
