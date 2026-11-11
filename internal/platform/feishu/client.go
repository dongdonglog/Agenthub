package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

// DefaultBaseURL 是飞书开放平台地址；Lark 海外部署可覆盖。
const DefaultBaseURL = "https://open.feishu.cn"

// Client 是最小可用的飞书 OpenAPI 客户端。
type Client struct {
	appID     string
	appSecret string
	baseURL   string
	http      *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// Option 用于覆盖客户端默认行为。
type Option func(*Client)

// WithBaseURL 覆盖开放平台地址（测试指向本地假服务）。
func WithBaseURL(base string) Option {
	return func(c *Client) { c.baseURL = base }
}

// WithHTTPClient 注入自定义 HTTP 客户端。
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// NewClient 构造客户端。
func NewClient(appID, appSecret string, opts ...Option) *Client {
	c := &Client{
		appID:     appID,
		appSecret: appSecret,
		baseURL:   DefaultBaseURL,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError 是飞书接口返回的业务错误。
type APIError struct {
	StatusCode int
	Code       int
	Msg        string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("feishu api error: status=%d code=%d msg=%s", e.StatusCode, e.Code, e.Msg)
}

// SendText 发送文本消息；replyTo 非空时以回复形式发送。
//
// @returns 飞书返回的消息 ID
func (c *Client) SendText(ctx context.Context, chatID, replyTo, text string) (string, error) {
	if chatID == "" {
		return "", fmt.Errorf("chat_id 为空")
	}
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"msg_type": "text",
		"content":  string(content),
	}
	path := "/open-apis/im/v1/messages"
	if replyTo != "" {
		path = "/open-apis/im/v1/messages/" + url.PathEscape(replyTo) + "/reply"
	} else {
		body["receive_id"] = chatID
	}

	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, path+"?receive_id_type=chat_id", body, &out); err != nil {
		return "", err
	}
	return out.Data.MessageID, nil
}

func (c *Client) tenantToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.expiry) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	body := map[string]string{"app_id": c.appID, "app_secret": c.appSecret}
	if err := c.doRaw(ctx, http.MethodPost, "/open-apis/auth/v3/tenant_access_token/internal", body, "", &out); err != nil {
		return "", err
	}
	if out.TenantAccessToken == "" {
		return "", fmt.Errorf("飞书未返回 tenant_access_token")
	}

	c.mu.Lock()
	c.token = out.TenantAccessToken
	ttl := time.Duration(out.Expire) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	c.expiry = time.Now().Add(ttl - 60*time.Second)
	c.mu.Unlock()
	return out.TenantAccessToken, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	token, err := c.tenantToken(ctx)
	if err != nil {
		return err
	}
	return c.doRaw(ctx, method, path, body, token, out)
}

func (c *Client) doRaw(ctx context.Context, method, path string, body any, token string, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{StatusCode: resp.StatusCode, Msg: string(data)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("解析飞书响应失败: %w", err)
		}
	}
	var head struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &head); err == nil && head.Code != 0 {
		return &APIError{StatusCode: resp.StatusCode, Code: head.Code, Msg: head.Msg}
	}
	return nil
}

// CreateCard 发送一张交互卡片，返回消息 ID。
//
// @param content 卡片 JSON
// @param replyTo 非空时以回复形式发送
func (c *Client) CreateCard(ctx context.Context, chatID, replyTo, content string) (string, error) {
	body := map[string]any{"msg_type": "interactive", "content": content}
	path := "/open-apis/im/v1/messages?receive_id_type=chat_id"
	if replyTo != "" {
		path = "/open-apis/im/v1/messages/" + url.PathEscape(replyTo) + "/reply"
	} else {
		body["receive_id"] = chatID
	}
	var out struct {
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return "", err
	}
	return out.Data.MessageID, nil
}

// UpdateCard 更新已发送的卡片内容。
func (c *Client) UpdateCard(ctx context.Context, messageID, content string) error {
	return c.do(ctx, http.MethodPatch, "/open-apis/im/v1/messages/"+url.PathEscape(messageID),
		map[string]any{"content": content}, nil)
}

// Quoted 是被引用消息的摘要。
type Quoted struct {
	// Text 是消息的文本表达。
	Text string
	// FromBot 表示这条消息由应用发出。
	FromBot bool
}

// GetMessage 读取一条消息，用于补齐引用上下文。
//
// @throws 消息不存在、无权限或解析失败时返回错误
func (c *Client) GetMessage(ctx context.Context, messageID string) (Quoted, error) {
	var out struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				MsgType string `json:"msg_type"`
				Body    struct {
					Content string `json:"content"`
				} `json:"body"`
				Sender struct {
					ID         string `json:"id"`
					SenderType string `json:"sender_type"`
				} `json:"sender"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/open-apis/im/v1/messages/"+url.PathEscape(messageID), nil, &out); err != nil {
		return Quoted{}, err
	}
	if len(out.Data.Items) == 0 {
		return Quoted{}, fmt.Errorf("消息不存在")
	}
	item := out.Data.Items[0]
	text, _ := extractContent(Message{MessageType: item.MsgType, Content: item.Body.Content})
	return Quoted{
		Text:    strings.TrimSpace(text),
		FromBot: item.Sender.SenderType == "app" || item.Sender.ID == c.appID,
	}, nil
}

// DownloadResource 流式下载消息资源到 w。
//
// @param kind image 走图片资源接口，其余走文件接口
// @returns 写入的字节数
// @throws 非 200 或传输失败时返回错误
func (c *Client) DownloadResource(ctx context.Context, messageID, fileKey, kind string, w io.Writer) (int64, error) {
	token, err := c.tenantToken(ctx)
	if err != nil {
		return 0, err
	}
	resourceType := "file"
	if kind == "image" {
		resourceType = "image"
	}
	endpoint := fmt.Sprintf("%s/open-apis/im/v1/messages/%s/resources/%s?type=%s",
		strings.TrimRight(c.baseURL, "/"), url.PathEscape(messageID), url.PathEscape(fileKey), resourceType)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("下载资源失败 %d: %s", resp.StatusCode, string(body))
	}
	return io.Copy(w, resp.Body)
}

// Channel 把客户端适配为 {@link channel.Channel}。
type Channel struct {
	client *Client
}

// NewChannel 包装客户端。
func NewChannel(c *Client) *Channel {
	return &Channel{client: c}
}

// Client 返回底层客户端，供附件下载等场景复用。
func (c *Channel) Client() *Client { return c.client }

// maxTextChunk 是单条文本消息的安全长度。
const maxTextChunk = 3500

// SendText 实现 {@link channel.Channel}。
//
// @remarks 超长文本按段落边界切分，首块建立回复关系，其余顺序追加，
// 保证长答案不会被平台截断。
func (c *Channel) SendText(ctx context.Context, out channel.Outbound) (channel.Receipt, error) {
	chunks := splitText(out.Text, maxTextChunk)
	firstID := ""
	for i, chunk := range chunks {
		replyTo := ""
		if i == 0 {
			replyTo = out.ReplyTo
		}
		id, err := c.client.SendText(ctx, out.ChatID, replyTo, chunk)
		if err != nil {
			return channel.Receipt{ExternalID: firstID}, err
		}
		if i == 0 {
			firstID = id
		}
	}
	return channel.Receipt{ExternalID: firstID}, nil
}

// splitText 按字符数与段落边界切分文本。
func splitText(text string, limit int) []string {
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	var chunks []string
	for len(runes) > limit {
		cut := limit
		// 尽量在换行处断开。
		for i := limit - 1; i > limit/2; i-- {
			if runes[i] == 10 {
				cut = i + 1
				break
			}
		}
		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}
