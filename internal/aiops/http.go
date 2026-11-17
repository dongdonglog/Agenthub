package aiops

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/moying/agenthub/internal/alerting"
)

// Handler 是告警 webhook 入口，按来源选择适配器。
type Handler struct {
	ingestor     *Ingestor
	token        string
	sourceTokens map[string]string
	registry     *alerting.Registry
}

// NewHandler 构造 webhook handler，默认支持 Alertmanager/Grafana/通用/PagerDuty。
func NewHandler(ingestor *Ingestor, token string) *Handler {
	return &Handler{
		ingestor: ingestor,
		token:    token,
		registry: alerting.NewRegistry(alerting.Alertmanager{}, alerting.Grafana{}, alerting.Generic{}, alerting.PagerDuty{}),
	}
}

// SetRegistry 覆盖适配器注册表，便于扩展。
func (h *Handler) SetRegistry(registry *alerting.Registry) { h.registry = registry }

// SetSourceTokens 设置按来源区分的 token；未配置的来源回退到全局 token。
func (h *Handler) SetSourceTokens(tokens map[string]string) { h.sourceTokens = tokens }

// ServeHTTP 处理一次告警 webhook。
func (h *Handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if h == nil || h.ingestor == nil {
		http.Error(rw, "aiops disabled", http.StatusServiceUnavailable)
		return
	}
	source := r.PathValue("source")
	if source == "" {
		parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
		if len(parts) > 0 {
			source = parts[len(parts)-1]
		}
	}
	if source == "" {
		source = "alertmanager"
	}
	token := h.token
	if h.sourceTokens != nil && h.sourceTokens[source] != "" {
		token = h.sourceTokens[source]
	}
	if token != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.Header.Get("X-Alertmanager-Token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if h.registry == nil {
		h.registry = alerting.NewRegistry(alerting.Alertmanager{}, alerting.Grafana{}, alerting.Generic{}, alerting.PagerDuty{})
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(rw, "read body failed", http.StatusBadRequest)
		return
	}
	event, err := h.registry.Decode(source, body)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	count, err := h.ingestor.HandleEvent(r.Context(), event)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(rw).Encode(map[string]any{"status": "accepted", "alerts": count, "source": source})
}
