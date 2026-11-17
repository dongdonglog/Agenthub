package feishu

import (
	"encoding/json"
	"fmt"

	"github.com/moying/agenthub/internal/store"
)

// ApprovalCard 构造动作审批卡片。
func ApprovalCard(approval *store.Approval) string {
	content := fmt.Sprintf("**动作审批**\n\n%s\n\n申请人：%s\n审批 ID：%s", approval.Action, approval.RequestedBy, approval.ID)
	if len(approval.Params) > 0 {
		content += "\n参数：`" + string(approval.Params) + "`"
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "orange",
			"title":    map[string]any{"tag": "plain_text", "content": "AgentHub 动作审批"},
		},
		"elements": []any{
			map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": content}},
			map[string]any{"tag": "action", "actions": []any{
				map[string]any{"tag": "button", "text": map[string]any{"tag": "plain_text", "content": "批准"},
					"type": "primary", "value": map[string]any{"action": "approval_approve", "approval_id": approval.ID}},
				map[string]any{"tag": "button", "text": map[string]any{"tag": "plain_text", "content": "拒绝"},
					"type": "danger", "value": map[string]any{"action": "approval_reject", "approval_id": approval.ID}},
			}},
		},
	}
	raw, _ := json.Marshal(card)
	return string(raw)
}
