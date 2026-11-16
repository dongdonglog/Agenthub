package aiops

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Handler 是 Alertmanager webhook 入口。
type Handler struct {
	ingestor *Ingestor
	token    string
}

// NewHandler 构造 webhook handler。
func NewHandler(ingestor *Ingestor, token string) *Handler {
	return &Handler{ingestor: ingestor, token: token}
}

func (h *Handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if h == nil || h.ingestor == nil {
		http.Error(rw, "aiops disabled", http.StatusServiceUnavailable)
		return
	}
	if h.token != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.Header.Get("X-Alertmanager-Token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(rw, "read body failed", http.StatusBadRequest)
		return
	}
	count, err := h.ingestor.Handle(r.Context(), body)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(rw).Encode(map[string]any{"status": "accepted", "alerts": count})
}
