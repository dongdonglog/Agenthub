package feishu_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/platform/feishu"
)

func TestSendArtifactUploadsFileAndReplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte("artifact-body"), 0o644); err != nil {
		t.Fatal(err)
	}
	var uploadName, uploadType, uploadBody string
	var message map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case r.URL.Path == "/open-apis/im/v1/files":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				switch part.FormName() {
				case "file_name":
					raw, _ := io.ReadAll(part)
					uploadName = string(raw)
				case "file_type":
					raw, _ := io.ReadAll(part)
					uploadType = string(raw)
				case "file":
					raw, _ := io.ReadAll(part)
					uploadBody = string(raw)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"file_key": "file_1"}})
		case strings.Contains(r.URL.Path, "/im/v1/messages/") && strings.HasSuffix(r.URL.Path, "/reply"):
			_ = json.NewDecoder(r.Body).Decode(&message)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_file"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := feishu.NewClient("cli", "secret", feishu.WithBaseURL(srv.URL))
	receipt, err := client.SendArtifact(context.Background(), channel.OutboundArtifact{
		ChatID: "oc_1", ReplyTo: "om_in",
		Artifact: channel.Artifact{Name: "report.md", Path: path, Kind: "file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ExternalID != "om_file" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if uploadName != "report.md" || uploadType != "stream" || uploadBody != "artifact-body" {
		t.Fatalf("upload name=%q type=%q body=%q", uploadName, uploadType, uploadBody)
	}
	if message["msg_type"] != "file" {
		t.Fatalf("message = %+v", message)
	}
	var content map[string]string
	_ = json.Unmarshal([]byte(message["content"].(string)), &content)
	if content["file_key"] != "file_1" {
		t.Fatalf("content = %+v", content)
	}
}

func TestSendArtifactUploadsImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chart.png")
	if err := os.WriteFile(path, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case r.URL.Path == "/open-apis/im/v1/images":
			var imageType string
			reader, _ := r.MultipartReader()
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				if part.FormName() == "image_type" {
					raw, _ := io.ReadAll(part)
					imageType = string(raw)
				}
			}
			if imageType != "message" {
				t.Fatalf("image_type = %q", imageType)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"image_key": "img_1"}})
		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			_ = json.NewDecoder(r.Body).Decode(&message)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_img"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := feishu.NewClient("cli", "secret", feishu.WithBaseURL(srv.URL))
	if _, err := client.SendArtifact(context.Background(), channel.OutboundArtifact{
		ChatID:   "oc_1",
		Artifact: channel.Artifact{Name: "chart.png", Path: path, Kind: "image"},
	}); err != nil {
		t.Fatal(err)
	}
	if message["msg_type"] != "image" {
		t.Fatalf("message = %+v", message)
	}
	var content map[string]string
	_ = json.Unmarshal([]byte(message["content"].(string)), &content)
	if content["image_key"] != "img_1" {
		t.Fatalf("content = %+v", content)
	}
}
