package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

const defaultDingtalkAPIBase = "https://api.dingtalk.com"

var attachmentHTTP = &http.Client{Timeout: 30 * time.Second}

type attachmentRef struct {
	Kind string
	Name string
	Code string
}

// parseAttachmentRefs 从回调 content 中提取文件/图片下载凭证。
func parseAttachmentRefs(content any) []attachmentRef {
	payload := content
	if raw, ok := content.(string); ok {
		var parsed any
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			payload = parsed
		}
	}
	obj, ok := payload.(map[string]any)
	if !ok {
		return nil
	}
	var out []attachmentRef
	name, _ := obj["fileName"].(string)
	if name == "" {
		name, _ = obj["name"].(string)
	}
	if code, _ := obj["downloadCode"].(string); code != "" {
		kind := "file"
		if _, ok := obj["pictureDownloadCode"]; ok {
			kind = "image"
		}
		if strings.HasPrefix(strings.ToLower(name), "image") || kind == "image" {
			kind = "image"
		}
		out = append(out, attachmentRef{Kind: kind, Name: name, Code: code})
	}
	if code, _ := obj["pictureDownloadCode"].(string); code != "" {
		out = append(out, attachmentRef{Kind: "image", Name: name, Code: code})
	}
	if rich, ok := obj["richText"].([]any); ok {
		for _, item := range rich {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if code, _ := part["downloadCode"].(string); code != "" {
				partName, _ := part["fileName"].(string)
				out = append(out, attachmentRef{Kind: "image", Name: partName, Code: code})
			}
		}
	}
	return out
}

func (c *Channel) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	base := c.cfg.APIBase
	if base == "" {
		base = defaultDingtalkAPIBase
	}
	body := map[string]string{"appKey": c.cfg.ClientID, "appSecret": c.cfg.ClientSecret}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1.0/oauth2/accessToken", strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := attachmentHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return "", fmt.Errorf("钉钉访问令牌换取失败: status=%d", resp.StatusCode)
	}
	ttl := time.Duration(out.ExpireIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	c.mu.Lock()
	c.token = out.AccessToken
	c.tokenExpiry = time.Now().Add(ttl - time.Minute)
	c.mu.Unlock()
	return out.AccessToken, nil
}

func (c *Channel) downloadAttachments(ctx context.Context, messageID string, refs []attachmentRef) []channel.Attachment {
	if len(refs) == 0 || c.cfg.AttachmentsDir == "" {
		return nil
	}
	dir := filepath.Join(c.cfg.AttachmentsDir, sanitizeAttachmentName(messageID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.log.Warn("dingtalk attachment dir failed", "error", err)
		return nil
	}
	var out []channel.Attachment
	for _, ref := range refs {
		path, size, err := c.downloadOne(ctx, dir, ref)
		if err != nil {
			c.log.Warn("dingtalk attachment download failed", "name", ref.Name, "error", err)
			continue
		}
		out = append(out, channel.Attachment{
			Kind: ref.Kind, Name: ref.Name, Path: path, Size: size, FileKey: ref.Code,
		})
	}
	return out
}

func (c *Channel) downloadOne(ctx context.Context, dir string, ref attachmentRef) (string, int64, error) {
	base := c.cfg.APIBase
	if base == "" {
		base = defaultDingtalkAPIBase
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return "", 0, err
	}
	body, _ := json.Marshal(map[string]string{"downloadCode": ref.Code, "robotCode": c.cfg.ClientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1.0/robot/messageFiles/download", strings.NewReader(string(body)))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)
	resp, err := attachmentHTTP.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	var meta struct {
		DownloadURL string `json:"downloadUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&meta); err != nil {
		return "", 0, err
	}
	if meta.DownloadURL == "" {
		return "", 0, fmt.Errorf("钉钉未返回下载地址")
	}
	downloadURL, err := url.Parse(meta.DownloadURL)
	if err != nil {
		return "", 0, err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, downloadURL.String(), nil)
	if err != nil {
		return "", 0, err
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("钉钉附件下载状态 %d", resp.StatusCode)
	}
	name := sanitizeAttachmentName(ref.Name)
	if name == "" {
		name = "attachment"
	}
	target := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", 0, err
	}
	n, copyErr := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp.Name())
		if copyErr != nil {
			return "", 0, copyErr
		}
		return "", 0, closeErr
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		_ = os.Remove(tmp.Name())
		return "", 0, err
	}
	return target, n, nil
}

func sanitizeAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}
