// Package action 执行经过审批的受控运维动作。
package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/store"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,252}$`)

// Executor 执行 K8s 受控动作。
type Executor struct {
	Kubectl    string
	Namespaces map[string]bool
}

// Execute 执行审批中的动作；只支持 allowlist 内的 K8s scale/restart。
func (e *Executor) Execute(ctx context.Context, approval *store.Approval) (string, error) {
	if approval == nil {
		return "", errors.New("审批为空")
	}
	var params struct {
		Namespace  string `json:"namespace"`
		Deployment string `json:"deployment"`
		Replicas   int    `json:"replicas"`
	}
	if err := json.Unmarshal(approval.Params, &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if e.Kubectl == "" {
		return "", errors.New("kubectl 未配置")
	}
	if !nameRE.MatchString(params.Namespace) || !nameRE.MatchString(params.Deployment) {
		return "", errors.New("namespace/deployment 非法")
	}
	if len(e.Namespaces) == 0 {
		return "", errors.New("未配置 namespace allowlist，受控动作拒绝执行")
	}
	if !e.Namespaces[params.Namespace] {
		return "", fmt.Errorf("namespace %s 不在 allowlist", params.Namespace)
	}
	var args []string
	switch approval.Action {
	case "k8s_scale":
		if params.Replicas < 1 || params.Replicas > 20 {
			return "", errors.New("replicas 必须在 1-20")
		}
		args = []string{"scale", "deployment/" + params.Deployment, "--replicas", strconv.Itoa(params.Replicas), "-n", params.Namespace, "--request-timeout=30s"}
	case "k8s_restart":
		args = []string{"rollout", "restart", "deployment/" + params.Deployment, "-n", params.Namespace, "--request-timeout=30s"}
	default:
		return "", fmt.Errorf("不支持的动作: %s", approval.Action)
	}
	runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := exec.CommandContext(runCtx, e.Kubectl, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("执行失败: %w: %s", err, string(out))
	}
	result := strings.TrimSpace(string(out))
	if verified, verifyErr := e.verify(ctx, approval.Action, params.Namespace, params.Deployment); verifyErr != nil {
		result += "\n验证失败: " + verifyErr.Error()
	} else if verified != "" {
		result += "\n验证: " + verified
	}
	return result, nil
}

func (e *Executor) verify(ctx context.Context, action, namespace, deployment string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var args []string
	switch action {
	case "k8s_scale":
		args = []string{"get", "deployment/" + deployment, "-n", namespace, "-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}", "--request-timeout=15s"}
	case "k8s_restart":
		args = []string{"rollout", "status", "deployment/" + deployment, "-n", namespace, "--timeout=50s"}
	default:
		return "", nil
	}
	out, err := exec.CommandContext(ctx, e.Kubectl, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
