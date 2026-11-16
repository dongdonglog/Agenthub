package feishu_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/platform/feishu"
)

func TestReactionLifecycle(t *testing.T) {
	var added, removed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reactions"):
			var body struct {
				Type struct {
					Emoji string `json:"emoji_type"`
				} `json:"reaction_type"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			added = body.Type.Emoji
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"reaction_id": "r1"}})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/reactions/"):
			removed = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := feishu.NewClient("id", "secret", feishu.WithBaseURL(srv.URL))
	ch := feishu.NewChannel(client)
	id, err := ch.React(context.Background(), "om_1", "completed")
	if err != nil || id != "r1" || added != "DONE" {
		t.Fatalf("id=%s added=%s err=%v", id, added, err)
	}
	if err := ch.Unreact(context.Background(), "om_1", id); err != nil || removed != "r1" {
		t.Fatalf("removed=%s err=%v", removed, err)
	}
}
