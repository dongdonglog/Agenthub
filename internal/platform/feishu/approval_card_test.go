package feishu

import (
	"encoding/json"
	"testing"

	"github.com/moying/agenthub/internal/store"
)

func TestApprovalCard(t *testing.T) {
	raw := ApprovalCard(&store.Approval{ID: "apr_1", Action: "k8s_scale", RequestedBy: "oncall", Params: json.RawMessage(`{"replicas":2}`)})
	var card map[string]any
	if err := json.Unmarshal([]byte(raw), &card); err != nil {
		t.Fatalf("invalid card: %v", err)
	}
	if card["header"] == nil || card["elements"] == nil {
		t.Fatalf("card = %s", raw)
	}
}
