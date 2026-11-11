// Package core 实现受理、会话串行、批次快照与投递的最小闭环。
//
// 本包当前使用内存状态，便于在没有数据库的环境中验证语义；持久化实现
// 将在 M2 接入，接口保持稳定。
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

// Message 是一条已受理的消息。
type Message struct {
	ID             string
	ConversationID string
	ChatID         string
	ExternalID     string
	SenderOpenID   string
	Text           string
	MsgType        string
	Status         string // queued | processing | done | failed | stopped
	Attachments    []channel.Attachment
	CreatedAt      time.Time
}

// Run 是一次执行。
type Run struct {
	ID             string
	ConversationID string
	State          string // running | completed | failed | interrupted
	InputIDs       []string
	Output         string
	Error          string
	StartedAt      time.Time
	EndedAt        time.Time
}

// Delivery 是一次结果投递。
type Delivery struct {
	RunID          string
	IdempotencyKey string
	ChatID         string
	ReplyTo        string
	Text           string
	State          string // sent | failed
	Error          string
}

// Engine 负责把入站消息收敛成批次并驱动执行与投递。
type Engine struct {
	mu    sync.Mutex
	agent agent.Agent
	ch    channel.Channel
	log   *slog.Logger
	now   func() time.Time

	messages   map[string]*Message
	order      map[string][]string
	dedupe     map[string]string
	runs       map[string]*Run
	runOrder   []string
	active     map[string]string
	cancels    map[string]context.CancelFunc
	deliveries map[string]*Delivery
	sent       map[string]bool
	seq        map[string]int64
	runSeq     int64
}

// New 构造执行引擎。
//
// @param a 执行器；为 nil 时使用回显实现
// @param ch 发送通道；为 nil 时投递只记录不发送
func New(a agent.Agent, ch channel.Channel, log *slog.Logger) *Engine {
	if a == nil {
		a = agent.Echo{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		agent:      a,
		ch:         ch,
		log:        log,
		now:        time.Now,
		messages:   make(map[string]*Message),
		order:      make(map[string][]string),
		dedupe:     make(map[string]string),
		runs:       make(map[string]*Run),
		active:     make(map[string]string),
		cancels:    make(map[string]context.CancelFunc),
		deliveries: make(map[string]*Delivery),
		sent:       make(map[string]bool),
		seq:        make(map[string]int64),
	}
}

// Submit 受理一条消息。
//
// @returns inserted 为 false 表示重复投递，已忽略
// @throws 缺少外部消息 ID 或 chat_id 时返回错误
func (e *Engine) Submit(_ context.Context, in channel.Inbound) (bool, error) {
	if in.ExternalID == "" {
		return false, errors.New("缺少外部消息 ID")
	}
	if in.Chat.ID == "" {
		return false, errors.New("缺少 chat_id")
	}
	conv := conversationKey(in)
	dedupeKey := in.Tenant + "|" + in.ExternalID

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.dedupe[dedupeKey]; ok {
		return false, nil
	}

	e.seq[conv]++
	msg := &Message{
		ID:             fmt.Sprintf("msg_%s_%d", conv, e.seq[conv]),
		ConversationID: conv,
		ChatID:         in.Chat.ID,
		ExternalID:     in.ExternalID,
		SenderOpenID:   in.Sender.OpenID,
		Text:           in.Text,
		MsgType:        in.MsgType,
		Status:         "queued",
		Attachments:    in.Attachments,
		CreatedAt:      e.now(),
	}
	e.messages[msg.ID] = msg
	e.dedupe[dedupeKey] = msg.ID
	e.order[conv] = append(e.order[conv], msg.ID)
	return true, nil
}

// Flush 处理所有"有排队消息且无活动运行"的会话。
//
// @remarks 生产环境由定时器驱动；测试可以显式调用以获得确定性。
func (e *Engine) Flush(ctx context.Context) error {
	e.mu.Lock()
	convs := make([]string, 0, len(e.order))
	for conv, ids := range e.order {
		if len(ids) > 0 && e.active[conv] == "" {
			convs = append(convs, conv)
		}
	}
	e.mu.Unlock()

	sort.Strings(convs)
	var firstErr error
	for _, conv := range convs {
		if err := e.runBatch(ctx, conv); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Stop 中止会话当前批次。
//
// @returns 没有活动运行时返回 false
func (e *Engine) Stop(conv string) bool {
	e.mu.Lock()
	runID := e.active[conv]
	cancel := e.cancels[conv]
	e.mu.Unlock()
	if runID == "" || cancel == nil {
		return false
	}
	cancel()
	return true
}

// Messages 返回某个会话的消息快照（按创建时间排序）。
func (e *Engine) Messages(conv string) []*Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Message, 0, len(e.messages))
	for _, m := range e.messages {
		if m.ConversationID == conv {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Conversation 是管理面使用的会话摘要。
type Conversation struct {
	ID          string    `json:"id"`
	ChatID      string    `json:"chat_id"`
	Sender      string    `json:"sender"`
	Messages    int       `json:"messages"`
	Queued      int       `json:"queued"`
	ActiveRunID string    `json:"active_run_id,omitempty"`
	LastActive  time.Time `json:"last_active"`
}

// Conversations 返回全部会话摘要，按最近活跃排序。
func (e *Engine) Conversations() []Conversation {
	e.mu.Lock()
	defer e.mu.Unlock()
	index := make(map[string]*Conversation)
	for _, m := range e.messages {
		c, ok := index[m.ConversationID]
		if !ok {
			c = &Conversation{ID: m.ConversationID, ChatID: m.ChatID, Sender: m.SenderOpenID}
			index[m.ConversationID] = c
		}
		c.Messages++
		if m.Status == "queued" {
			c.Queued++
		}
		if m.CreatedAt.After(c.LastActive) {
			c.LastActive = m.CreatedAt
		}
	}
	for conv, runID := range e.active {
		if c, ok := index[conv]; ok {
			c.ActiveRunID = runID
		}
	}
	out := make([]Conversation, 0, len(index))
	for _, c := range index {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out
}

// Runs 返回全部运行的快照。
func (e *Engine) Runs() []*Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Run, 0, len(e.runOrder))
	for _, id := range e.runOrder {
		if r := e.runs[id]; r != nil {
			out = append(out, r)
		}
	}
	return out
}

// Deliveries 返回全部投递记录。
func (e *Engine) Deliveries() []*Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Delivery, 0, len(e.deliveries))
	for _, d := range e.deliveries {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IdempotencyKey < out[j].IdempotencyKey })
	return out
}

func (e *Engine) runBatch(ctx context.Context, conv string) error {
	e.mu.Lock()
	if e.active[conv] != "" {
		e.mu.Unlock()
		return nil
	}
	batch := make([]*Message, 0, len(e.order[conv]))
	for _, id := range e.order[conv] {
		if m := e.messages[id]; m != nil && m.Status == "queued" {
			batch = append(batch, m)
		}
	}
	if len(batch) == 0 {
		e.order[conv] = nil
		e.mu.Unlock()
		return nil
	}
	// 批次快照：清空队列并冻结输入（不变量 I2）。
	e.order[conv] = nil
	e.runSeq++
	run := &Run{
		ID:             fmt.Sprintf("run_%d", e.runSeq),
		ConversationID: conv,
		State:          "running",
		StartedAt:      e.now(),
	}
	for _, m := range batch {
		m.Status = "processing"
		run.InputIDs = append(run.InputIDs, m.ID)
	}
	e.runs[run.ID] = run
	e.runOrder = append(e.runOrder, run.ID)
	e.active[conv] = run.ID

	runCtx, cancel := context.WithCancel(ctx)
	e.cancels[conv] = cancel
	e.mu.Unlock()

	input := agent.Input{ConversationID: conv}
	for _, m := range batch {
		input.Messages = append(input.Messages, agent.Message{ID: m.ID, Text: m.Text, Attachments: m.Attachments})
	}

	out, err := e.agent.Run(runCtx, input)
	cancel()

	e.mu.Lock()
	run.EndedAt = e.now()
	text := out.Text
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		run.State = "interrupted"
		run.Error = err.Error()
		if text == "" {
			text = "已停止当前处理。"
		}
		for _, m := range batch {
			m.Status = "stopped"
		}
	case err != nil:
		run.State = "failed"
		run.Error = err.Error()
		if text == "" {
			text = "处理失败：" + err.Error()
		}
		for _, m := range batch {
			m.Status = "failed"
		}
	default:
		run.State = "completed"
		run.Output = text
		for _, m := range batch {
			m.Status = "done"
		}
	}
	delete(e.active, conv)
	delete(e.cancels, conv)
	delivery := &Delivery{
		RunID:          run.ID,
		IdempotencyKey: run.ID + ":result",
		ChatID:         batch[0].ChatID,
		ReplyTo:        batch[0].ExternalID,
		Text:           text,
		State:          "pending",
	}
	e.deliveries[delivery.IdempotencyKey] = delivery
	alreadySent := e.sent[delivery.IdempotencyKey]
	e.mu.Unlock()

	if alreadySent {
		return nil
	}
	e.send(ctx, delivery)
	return nil
}

func (e *Engine) send(ctx context.Context, d *Delivery) {
	if e.ch == nil {
		e.mu.Lock()
		d.State = "sent"
		e.sent[d.IdempotencyKey] = true
		e.mu.Unlock()
		return
	}
	_, err := e.ch.SendText(ctx, channel.Outbound{ChatID: d.ChatID, ReplyTo: d.ReplyTo, Text: d.Text})
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		d.State = "failed"
		d.Error = err.Error()
		e.log.Warn("delivery failed", "run", d.RunID, "error", err)
		return
	}
	d.State = "sent"
	e.sent[d.IdempotencyKey] = true
}

func conversationKey(in channel.Inbound) string {
	return in.Tenant + ":" + in.Chat.ID + ":" + in.Sender.OpenID
}
