package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

const defaultDingtalkOAPIBase = "https://oapi.dingtalk.com"

// SendArtifact 通过会话 webhook 交付文件或图片。
func (c *Channel) SendArtifact(ctx context.Context, out channel.OutboundArtifact) (channel.Receipt, error) {
	c.mu.Lock()
	hook, ok := c.hooks[out.ChatID]
	c.mu.Unlock()
	if !ok {
		return channel.Receipt{}, channel.Definite(fmt.Errorf("没有可用的会话 webhook"))
	}
	if time.Now().After(hook.expireAt.Add(-30 * time.Second)) {
		return channel.Receipt{}, channel.Definite(fmt.Errorf("会话 webhook 已过期"))
	}
	mediaType := "file"
	if out.Artifact.Kind == "image" {
		mediaType = "image"
	}
	mediaID, err := c.uploadMedia(ctx, out.Artifact.Path, out.Artifact.Name, mediaType)
	if err != nil {
		return channel.Receipt{}, err
	}
	payload := map[string]any{"msgtype": "file", "file": map[string]string{
		"mediaId": mediaID, "fileName": out.Artifact.Name, "fileType": strings.TrimPrefix(strings.ToLower(filepath.Ext(out.Artifact.Name)), "."),
	}}
	if mediaType == "image" {
		payload = map[string]any{"msgtype": "image", "image": map[string]string{"media_id": mediaID}}
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.url, bytes.NewReader(raw))
	if err != nil {
		return channel.Receipt{}, channel.Definite(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := attachmentHTTP.Do(req)
	if err != nil {
		return channel.Receipt{}, channel.Ambiguous(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return channel.Receipt{}, channel.Ambiguous(fmt.Errorf("钉钉文件消息状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.ErrCode != 0 {
		return channel.Receipt{}, channel.Definite(fmt.Errorf("钉钉文件消息失败: %s", strings.TrimSpace(string(body))))
	}
	return channel.Receipt{}, nil
}

func (c *Channel) uploadMedia(ctx context.Context, path, name, mediaType string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", channel.Definite(err)
	}
	defer file.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("media", name)
	if err != nil {
		return "", channel.Definite(err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", channel.Definite(err)
	}
	if err := writer.Close(); err != nil {
		return "", channel.Definite(err)
	}
	token, err := c.oapiToken(ctx)
	if err != nil {
		return "", channel.Retryable(err)
	}
	base := c.cfg.OAPIBase
	if base == "" {
		base = defaultDingtalkOAPIBase
	}
	endpoint := base + "/media/upload?access_token=" + url.QueryEscape(token) + "&type=" + mediaType
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", channel.Definite(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := attachmentHTTP.Do(req)
	if err != nil {
		return "", channel.Ambiguous(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", channel.Ambiguous(err)
	}
	if resp.StatusCode != http.StatusOK || out.ErrCode != 0 || out.MediaID == "" {
		if resp.StatusCode >= 500 {
			return "", channel.Ambiguous(fmt.Errorf("钉钉媒体上传状态 %d", resp.StatusCode))
		}
		return "", channel.Definite(fmt.Errorf("钉钉媒体上传失败: %s", out.ErrMsg))
	}
	return out.MediaID, nil
}

func (c *Channel) oapiToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.oapiAccessToken != "" && time.Now().Before(c.oapiTokenExpiry) {
		token := c.oapiAccessToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()
	base := c.cfg.OAPIBase
	if base == "" {
		base = defaultDingtalkOAPIBase
	}
	endpoint := base + "/gettoken?appkey=" + url.QueryEscape(c.cfg.ClientID) + "&appsecret=" + url.QueryEscape(c.cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := attachmentHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	if out.ErrCode != 0 || out.AccessToken == "" {
		return "", fmt.Errorf("钉钉 oapi token 失败: %s", out.ErrMsg)
	}
	ttl := time.Duration(out.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	c.mu.Lock()
	c.oapiAccessToken = out.AccessToken
	c.oapiTokenExpiry = time.Now().Add(ttl - time.Minute)
	c.mu.Unlock()
	return out.AccessToken, nil
}

var _ channel.ArtifactSender = (*Channel)(nil)
