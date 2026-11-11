package feishu_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/platform/feishu"
)

func TestLongTextIsChunked(t *testing.T) {
	api, srv := newFakeAPI(t)
	client := feishu.NewClient("cli_test", "secret", feishu.WithBaseURL(srv.URL))
	ch := feishu.NewChannel(client)

	long := strings.Repeat("这是一段用于验证分块的长文本。\n", 300)
	if _, err := ch.SendText(context.Background(), channel.Outbound{
		ChatID: "oc_1", ReplyTo: "om_in", Text: long,
	}); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	sent := api.messages()
	if len(sent) < 2 {
		t.Fatalf("应分成多条发送，实际 %d", len(sent))
	}
	if !sent[0].reply {
		t.Fatal("首块应建立回复关系")
	}
	if sent[1].reply {
		t.Fatal("后续块不应重复建立回复关系")
	}

	var rebuilt strings.Builder
	for _, item := range sent {
		content, _ := item.body["content"].(string)
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(content), &payload); err != nil {
			t.Fatal(err)
		}
		rebuilt.WriteString(payload.Text)
	}
	if rebuilt.String() != long {
		t.Fatalf("分块内容不一致：got %d want %d", rebuilt.Len(), len(long))
	}
}
