package feishu

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/moying/agenthub/internal/core"
)

// CardAction 处理飞书卡片按钮回调。
//
// @remarks 回调需要公网可达地址；未配置时卡片不显示按钮。
type CardAction struct {
	engine *core.Engine
	token  string
	log    *slog.Logger
}

// NewCardAction 构造卡片回调处理器。
func NewCardAction(engine *core.Engine, verificationToken string, log *slog.Logger) *CardAction {
	if log == nil {
		log = slog.Default()
	}
	return &CardAction{engine: engine, token: verificationToken, log: log}
}

type cardCallback struct {
	Token  string `json:"token"`
	Header struct {
		Token string `json:"token"`
	} `json:"header"`
	Event struct {
		Operator struct {
			OpenID string `json:"open_id"`
		} `json:"operator"`
		Action struct {
			Value struct {
				Action string `json:"action"`
				RunID  string `json:"run_id"`
			} `json:"value"`
		} `json:"action"`
	} `json:"event"`
	// 兼容扁平结构（部分模板回调不带 event 包裹）。
	Action struct {
		Value struct {
			Action string `json:"action"`
			RunID  string `json:"run_id"`
		} `json:"value"`
	} `json:"action"`
}

// ServeHTTP 处理一次卡片回调。
func (c *CardAction) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}
	var cb cardCallback
	if err := json.Unmarshal(body, &cb); err != nil {
		http.Error(rw, "bad json", http.StatusBadRequest)
		return
	}
	if c.token != "" {
		token := cb.Header.Token
		if token == "" {
			token = cb.Token
		}
		if token != c.token {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
	}

	value := cb.Event.Action.Value
	if value.Action == "" {
		value = cb.Action.Value
	}
	switch value.Action {
	case "stop":
	default:
		writeJSON(rw, http.StatusOK, map[string]any{"toast": map[string]string{"type": "info", "content": "已忽略"}})
		return
	}

	owner := c.engine.RunOwner(value.RunID)
	if owner == "" {
		writeJSON(rw, http.StatusOK, map[string]any{"toast": map[string]string{"type": "error", "content": "运行已结束"}})
		return
	}
	if cb.Event.Operator.OpenID != owner {
		c.log.Warn("card stop denied", "run", value.RunID, "operator", cb.Event.Operator.OpenID)
		writeJSON(rw, http.StatusForbidden, map[string]any{"toast": map[string]string{"type": "error", "content": "只有发起人可以停止"}})
		return
	}
	if c.engine.StopRun(value.RunID) {
		c.log.Info("run stopped from card", "run", value.RunID)
		writeJSON(rw, http.StatusOK, map[string]any{"toast": map[string]string{"type": "success", "content": "已停止当前处理"}})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"toast": map[string]string{"type": "info", "content": "当前没有可停止的运行"}})
}
