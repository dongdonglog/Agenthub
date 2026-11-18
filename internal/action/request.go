package action

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// approvalNameRE 与执行期校验保持一致，限制 namespace / deployment 的字符集。
var approvalNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

// Request 是 pi 通过 request_approval 工具提交的动作申请。
type Request struct {
	Action     string `json:"action"`
	Namespace  string `json:"namespace"`
	Deployment string `json:"deployment"`
	Replicas   int    `json:"replicas"`
	// Reason 是模型给出的申请理由，进入案件时间线与审批卡片。
	Reason string `json:"reason"`
}

// ParseRequest 校验并归一化一份申请。
//
// @remarks pi 的输出按不可信输入处理：只接受受控动作类型与合法资源名，
// 任何多余字段都不进入审批参数。真正的 allowlist 授权仍由 {@link Executor.Execute} 在
// 审批通过后执行，这里只做形状校验，避免把脏数据写进 approval_request。
func ParseRequest(raw []byte) (*Request, json.RawMessage, error) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, fmt.Errorf("申请格式非法: %w", err)
	}
	req.Action = strings.TrimSpace(req.Action)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Deployment = strings.TrimSpace(req.Deployment)
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.Reason) > 2000 {
		req.Reason = req.Reason[:2000]
	}
	switch req.Action {
	case "k8s_scale":
		if req.Replicas < 1 || req.Replicas > 20 {
			return nil, nil, errors.New("replicas 必须在 1-20")
		}
	case "k8s_restart":
		// 重启不使用 replicas，忽略传入值。
		req.Replicas = 0
	default:
		return nil, nil, fmt.Errorf("不支持的申请动作: %q", req.Action)
	}
	if !approvalNameRE.MatchString(req.Namespace) {
		return nil, nil, errors.New("namespace 非法")
	}
	if !approvalNameRE.MatchString(req.Deployment) {
		return nil, nil, errors.New("deployment 非法")
	}
	params, err := json.Marshal(map[string]any{
		"namespace":  req.Namespace,
		"deployment": req.Deployment,
		"replicas":   req.Replicas,
	})
	if err != nil {
		return nil, nil, err
	}
	return &req, params, nil
}
