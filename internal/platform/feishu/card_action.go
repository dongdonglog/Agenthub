package feishu

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"context"
	"time"

	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

// CardAction 处理飞书卡片按钮回调。
//
// @remarks 回调需要公网可达地址；未配置时卡片不显示按钮。
type CardAction struct {
	engine     *core.Engine
	token      string
	encryptKey string
	log        *slog.Logger
	approvals  *store.CaseStore
	execute    func(context.Context, *store.Approval) (string, error)
	admins     map[string]bool
}

// NewCardAction 构造卡片回调处理器。
func NewCardAction(engine *core.Engine, verificationToken, encryptKey string, log *slog.Logger) *CardAction {
	if log == nil {
		log = slog.Default()
	}
	return &CardAction{engine: engine, token: verificationToken, encryptKey: encryptKey, log: log}
}

// SetApprovalHandler 挂接审批存储、执行器和允许审批的飞书用户。
func (c *CardAction) SetApprovalHandler(cases *store.CaseStore, execute func(context.Context, *store.Approval) (string, error), admins []string) {
	c.approvals = cases
	c.execute = execute
	c.admins = make(map[string]bool, len(admins))
	for _, id := range admins {
		c.admins[id] = true
	}
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
				Action     string `json:"action"`
				RunID      string `json:"run_id"`
				ApprovalID string `json:"approval_id"`
			} `json:"value"`
		} `json:"action"`
	} `json:"event"`
	// 兼容扁平结构（部分模板回调不带 event 包裹）。
	Action struct {
		Value struct {
			Action     string `json:"action"`
			RunID      string `json:"run_id"`
			ApprovalID string `json:"approval_id"`
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
	if !VerifySignature(c.encryptKey, r.Header, body) {
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}
	if decrypted, err := DecryptPayload(c.encryptKey, body); err != nil {
		http.Error(rw, "bad payload", http.StatusBadRequest)
		return
	} else {
		body = decrypted
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
	case "approval_approve", "approval_reject":
		c.handleApproval(rw, r, value.ApprovalID, value.Action == "approval_approve", cb.Event.Operator.OpenID)
		return
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

func (c *CardAction) handleApproval(rw http.ResponseWriter, r *http.Request, approvalID string, approve bool, operator string) {
	if c.approvals == nil || approvalID == "" {
		writeJSON(rw, http.StatusOK, map[string]any{"toast": map[string]string{"type": "error", "content": "审批不可用"}})
		return
	}
	if !c.admins[operator] {
		c.log.Warn("approval denied", "approval", approvalID, "operator", operator)
		writeJSON(rw, http.StatusForbidden, map[string]any{"toast": map[string]string{"type": "error", "content": "只有管理员可以审批"}})
		return
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	a, ok, err := c.approvals.DecideApproval(r.Context(), approvalID, status, operator, "", time.Now())
	if err != nil {
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"toast": map[string]string{"type": "error", "content": err.Error()}})
		return
	}
	if !ok {
		writeJSON(rw, http.StatusOK, map[string]any{"toast": map[string]string{"type": "info", "content": "审批已处理"}})
		return
	}
	if approve && c.execute != nil {
		result, execErr := c.execute(r.Context(), a)
		if execErr != nil {
			result = "执行失败: " + execErr.Error()
		}
		_ = c.approvals.SetApprovalResult(r.Context(), a.ID, result, time.Now())
		_ = c.approvals.AddCaseEvent(r.Context(), a.CaseID, "action", operator, result, time.Now())
	}
	content := "已拒绝"
	if approve {
		content = "已批准并执行"
	}
	writeJSON(rw, http.StatusOK, map[string]any{"toast": map[string]string{"type": "success", "content": content}})
}
