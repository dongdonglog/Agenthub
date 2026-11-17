// Package support 接收技术支持工单并创建案件、拉起技术支持数字员工。
package support

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

// Config 是技术支持入口配置。
type Config struct {
	ChatID     string
	EmployeeID string
	Team       string
}

// Ticket 是外部工单。
type Ticket struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Requester   string `json:"requester"`
	Priority    string `json:"priority"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Source      string `json:"source"`
}

// Ingestor 把工单转成案件并提交技术支持运行。
type Ingestor struct {
	store     *store.CaseStore
	knowledge *store.KnowledgeStore
	engine    *core.Engine
	cfg       Config
	log       *slog.Logger
	now       func() time.Time
}

// New 构造技术支持入口。
func New(st *store.CaseStore, engine *core.Engine, cfg Config, log *slog.Logger) *Ingestor {
	if log == nil {
		log = slog.Default()
	}
	if cfg.EmployeeID == "" {
		cfg.EmployeeID = "tech-support"
	}
	return &Ingestor{store: st, engine: engine, cfg: cfg, log: log, now: time.Now}
}

// SetKnowledge 挂接知识库。
func (i *Ingestor) SetKnowledge(k *store.KnowledgeStore) { i.knowledge = k }

// Handle 处理一份工单；返回案件 ID。
func (i *Ingestor) Handle(ctx context.Context, raw []byte) (string, error) {
	var ticket Ticket
	if err := json.Unmarshal(raw, &ticket); err != nil {
		return "", fmt.Errorf("解析工单失败: %w", err)
	}
	ticket.ID = strings.TrimSpace(ticket.ID)
	ticket.Title = strings.TrimSpace(ticket.Title)
	if ticket.ID == "" || ticket.Title == "" {
		return "", fmt.Errorf("工单 id/title 不能为空")
	}
	source := ticket.Source
	if source == "" {
		source = "webhook"
	}
	now := i.now()
	severity := first(ticket.Priority, "P3")
	c, created, err := i.store.EnsureCase(ctx, i.cfg.EmployeeID, "ticket", source, ticket.ID,
		ticket.Title, ticket.Description, severity, ticket.Service, ticket.Environment, i.cfg.ChatID, now)
	if err != nil {
		return "", err
	}
	if !created {
		_ = i.store.AddCaseEvent(ctx, c.ID, "updated", "support", "工单更新", now)
		return c.ID, nil
	}
	_ = i.store.SetCaseStatus(ctx, c.ID, "in_progress", now)
	_ = i.store.AddCaseEvent(ctx, c.ID, "created", "support", "由工单创建案件", now)
	var docs []store.KnowledgeDoc
	if i.knowledge != nil {
		docs, _ = i.knowledge.Search(ctx, ticket.Title+" "+ticket.Description, i.cfg.Team, 5)
	}
	prompt := buildPrompt(ticket, docs)
	if _, err := i.engine.Submit(ctx, channel.Inbound{
		Channel: "support", Tenant: "support", ExternalID: "ticket:" + ticket.ID,
		SentAt: now, Sender: channel.Sender{OpenID: "ticket", Name: "工单系统"},
		Chat: channel.Chat{ID: i.cfg.ChatID, Kind: "group", Name: "技术支持"},
		Text: prompt, MsgType: "text", ReadOnly: true,
	}); err != nil {
		i.log.Warn("submit support run failed", "ticket", ticket.ID, "error", err)
		return c.ID, err
	}
	return c.ID, nil
}

func buildPrompt(t Ticket, docs []store.KnowledgeDoc) string {
	var b strings.Builder
	b.WriteString("【技术支持工单】\n\n")
	b.WriteString("请按 tech-support 技能处理：先确认产品/版本/环境/影响范围，再查知识库和历史工单；给出可执行步骤，无法解决时升级人工。\n\n")
	fmt.Fprintf(&b, "工单: %s\n标题: %s\n优先级: %s\n服务: %s\n环境: %s\n提交人: %s\n\n描述:\n%s\n",
		t.ID, t.Title, first(t.Priority, "P3"), t.Service, t.Environment, t.Requester, t.Description)
	if len(docs) > 0 {
		b.WriteString("\n知识库命中（引用来源，不要编造）：\n")
		for _, doc := range docs {
			fmt.Fprintf(&b, "\n### %s（来源：%s）\n%s\n", doc.Title, doc.Source, doc.Content)
		}
	}
	b.WriteString("\n输出：结论、依据/来源、用户可执行步骤、需要收集的信息、是否升级人工。")
	return b.String()
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
