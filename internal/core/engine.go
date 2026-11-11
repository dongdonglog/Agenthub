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
	"strings"
	"sync"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

// Message 是一条已受理的消息。
type Message struct {
	ID string
	// Role 是 user 或 assistant。
	Role           string
	Generation     int
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
	InputTokens    int
	OutputTokens   int
}

// Delivery 是一次结果投递。
type Delivery struct {
	RunID          string
	IdempotencyKey string
	ChatID         string
	ReplyTo        string
	Text           string
	State          string // pending | retrying | sent | failed
	Error          string
	ExternalID     string
	Attempts       int
	CreatedAt      time.Time
	// NextAttempt 是下一次重试时间，零值表示立即。
	NextAttempt time.Time
}

// Metrics 是核心向指标系统上报的最小接口。
type Metrics interface {
	Inc(name string, labels map[string]string)
}

// RunEvent 是一条运行事件，用于管理台回放。
type RunEvent struct {
	RunID      string    `json:"run_id"`
	Kind       string    `json:"kind"` // phase | tool
	Name       string    `json:"name,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Presentation 描述一次运行对应的可更新输出。
type Presentation struct {
	ConversationID string
	ChatID         string
	ReplyTo        string
	RunID          string
	Inputs         int
}

// Presenter 是可更新输出的展示层。
//
// @remarks Open 失败时调用方退回纯文本；Close 成功表示结果已由卡片承载。
type Presenter interface {
	Open(ctx context.Context, p Presentation) (string, error)
	Update(ctx context.Context, handle, phase, detail string) error
	Close(ctx context.Context, handle, body string, failed bool) error
}

// Journal 是核心状态的持久化接口。
//
// @remarks 实现方负责幂等：同一会话同一外部消息 ID 只应写入一次。
type Journal interface {
	SaveMessage(ctx context.Context, m *Message) (bool, error)
	SetMessageStatus(ctx context.Context, ids []string, status string) error
	SaveGeneration(ctx context.Context, conversationID string, generation int) error
	LoadGenerations(ctx context.Context) (map[string]int, error)
	SaveRun(ctx context.Context, r *Run) error
	SaveDelivery(ctx context.Context, d *Delivery) error
	LoadRecent(ctx context.Context, limit int) ([]*Message, []*Run, error)
	LoadPendingDeliveries(ctx context.Context, limit int) ([]*Delivery, error)
	SaveRunEvent(ctx context.Context, ev RunEvent) error
	LoadRunEvents(ctx context.Context, runID string) ([]RunEvent, error)
}

// Engine 负责把入站消息收敛成批次并驱动执行与投递。
type Engine struct {
	mu    sync.Mutex
	agent agent.Agent
	ch    channel.Channel
	log   *slog.Logger
	now   func() time.Time

	messages    map[string]*Message
	order       map[string][]string
	dedupe      map[string]string
	runs        map[string]*Run
	runOrder    []string
	active      map[string]string
	cancels     map[string]context.CancelFunc
	deliveries  map[string]*Delivery
	sent        map[string]bool
	seq         map[string]int64
	runSeq      int64
	journal     Journal
	presenter   Presenter
	metrics     Metrics
	generations map[string]int
	events      map[string][]RunEvent
	// retryDelay 计算投递重试间隔，默认指数退避。
	retryDelay func(attempt int) time.Duration

	// 内存上限，避免长跑无界增长。
	maxMessagesPerConv int
	maxConversations   int
	maxRuns            int
	maxDeliveries      int
	pruneAt            time.Time
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
		agent:              a,
		ch:                 ch,
		log:                log,
		now:                time.Now,
		messages:           make(map[string]*Message),
		order:              make(map[string][]string),
		dedupe:             make(map[string]string),
		runs:               make(map[string]*Run),
		active:             make(map[string]string),
		cancels:            make(map[string]context.CancelFunc),
		deliveries:         make(map[string]*Delivery),
		generations:        make(map[string]int),
		events:             make(map[string][]RunEvent),
		maxMessagesPerConv: 100,
		maxConversations:   200,
		maxRuns:            200,
		maxDeliveries:      500,
		retryDelay: func(attempt int) time.Duration {
			d := time.Duration(1<<uint(attempt)) * time.Second
			if d > 30*time.Second {
				d = 30 * time.Second
			}
			return d
		},
		sent: make(map[string]bool),
		seq:  make(map[string]int64),
	}
}

// SetRetryDelay 覆盖投递重试间隔，主要用于测试。
func (e *Engine) SetRetryDelay(fn func(attempt int) time.Duration) {
	if fn != nil {
		e.retryDelay = fn
	}
}

// SetLimits 覆盖内存上限，主要用于测试。
func (e *Engine) SetLimits(messagesPerConv, conversations, runs, deliveries int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if messagesPerConv > 0 {
		e.maxMessagesPerConv = messagesPerConv
	}
	if conversations > 0 {
		e.maxConversations = conversations
	}
	if runs > 0 {
		e.maxRuns = runs
	}
	if deliveries > 0 {
		e.maxDeliveries = deliveries
	}
}

// SetMetrics 注入指标上报；为空时不上报。
func (e *Engine) SetMetrics(m Metrics) {
	e.metrics = m
}

// SetPresenter 注入可更新输出；为空时只发文本。
func (e *Engine) SetPresenter(p Presenter) {
	e.presenter = p
}

// SetJournal 注入持久化实现；为空表示纯内存运行。
func (e *Engine) SetJournal(j Journal) {
	e.journal = j
}

// Restore 从持久化恢复最近消息与运行。
//
// @remarks 重启时处于 processing 的消息重新排队，避免中断后卡住。
func (e *Engine) Restore(ctx context.Context) error {
	if e.journal == nil {
		return nil
	}
	msgs, runs, err := e.journal.LoadRecent(ctx, 1000)
	if err != nil {
		return err
	}
	e.mu.Lock()
	for _, m := range msgs {
		if m.Status == "processing" {
			m.Status = "queued"
		}
		if m.Generation > e.generations[m.ConversationID] {
			e.generations[m.ConversationID] = m.Generation
		}
		e.messages[m.ID] = m
		e.dedupe[m.ConversationID+"|"+m.ExternalID] = m.ID
		if n := seqSuffix(m.ID); n > e.seq[m.ConversationID] {
			e.seq[m.ConversationID] = n
		}
		if m.Status == "queued" {
			e.order[m.ConversationID] = append(e.order[m.ConversationID], m.ID)
		}
	}
	if gens, err := e.journal.LoadGenerations(ctx); err == nil {
		for conv, gen := range gens {
			if gen > e.generations[conv] {
				e.generations[conv] = gen
			}
		}
	}
	for _, r := range runs {
		e.runs[r.ID] = r
		e.runOrder = append(e.runOrder, r.ID)
		if n := seqSuffix(r.ID); n > e.runSeq {
			e.runSeq = n
		}
	}
	e.mu.Unlock()

	// 启动补偿：上次未送达的结果继续投递。
	if pending, err := e.journal.LoadPendingDeliveries(ctx, 100); err == nil {
		for _, d := range pending {
			e.mu.Lock()
			e.deliveries[d.IdempotencyKey] = d
			e.mu.Unlock()
			delay := time.Until(d.NextAttempt)
			if delay < 0 {
				delay = 0
			}
			e.log.Info("resuming pending delivery", "delivery", d.IdempotencyKey, "delay", delay)
			go func(item *Delivery) {
				if delay > 0 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(delay):
					}
				}
				e.send(ctx, item)
			}(d)
		}
	}
	return nil
}

func seqSuffix(id string) int64 {
	i := strings.LastIndex(id, "_")
	if i < 0 || i == len(id)-1 {
		return 0
	}
	var n int64
	for _, c := range id[i+1:] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

// Submit 受理一条消息。
//
// @returns inserted 为 false 表示重复投递，已忽略
// @throws 缺少外部消息 ID 或 chat_id 时返回错误
func (e *Engine) Submit(ctx context.Context, in channel.Inbound) (bool, error) {
	if in.ExternalID == "" {
		return false, errors.New("缺少外部消息 ID")
	}
	if in.Chat.ID == "" {
		return false, errors.New("缺少 chat_id")
	}
	conv := conversationKey(in)
	dedupeKey := conv + "|" + in.ExternalID

	e.mu.Lock()
	if _, ok := e.dedupe[dedupeKey]; ok {
		e.mu.Unlock()
		return false, nil
	}

	e.seq[conv]++
	msg := &Message{
		ID:             fmt.Sprintf("msg_%s_%d", conv, e.seq[conv]),
		ConversationID: conv,
		ChatID:         in.Chat.ID,
		Role:           "user",
		Generation:     e.generation(conv),
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
	e.mu.Unlock()
	if e.metrics != nil {
		e.metrics.Inc("agenthub_messages_accepted_total", nil)
	}

	if e.journal != nil {
		inserted, err := e.journal.SaveMessage(ctx, msg)
		switch {
		case err != nil:
			e.log.Error("persist message failed", "error", err, "message", msg.ExternalID)
		case !inserted:
			e.dropMessage(conv, dedupeKey, msg.ID)
			return false, nil
		}
	}
	return true, nil
}

func (e *Engine) dropMessage(conv, dedupeKey, id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.messages, id)
	delete(e.dedupe, dedupeKey)
	ids := e.order[conv][:0]
	for _, existing := range e.order[conv] {
		if existing != id {
			ids = append(ids, existing)
		}
	}
	e.order[conv] = ids
}

// WaitIdle 等待所有活动运行结束。
//
// @remarks 用于优雅停机：先停止接收新事件，再等在途运行收敛。
// 返回 true 表示已空闲，false 表示超时仍有运行。
func (e *Engine) WaitIdle(ctx context.Context) bool {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		e.mu.Lock()
		active := len(e.active)
		e.mu.Unlock()
		if active == 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// Prune 立即按内存上限裁剪历史数据。
//
// @remarks 只裁剪内存，数据库仍是完整事实源；排队与处理中消息不会被删除。
func (e *Engine) Prune() {
	e.mu.Lock()
	e.pruneAt = time.Time{}
	e.mu.Unlock()
	e.prune()
}

func (e *Engine) prune() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if now.Sub(e.pruneAt) < 30*time.Second {
		return
	}
	e.pruneAt = now

	// 1) 每个会话保留最近 N 条非活动消息
	byConv := make(map[string][]*Message)
	for _, m := range e.messages {
		byConv[m.ConversationID] = append(byConv[m.ConversationID], m)
	}
	for conv, list := range byConv {
		if len(list) <= e.maxMessagesPerConv {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
		drop := len(list) - e.maxMessagesPerConv
		for _, m := range list {
			if drop == 0 {
				break
			}
			if m.Status == "queued" || m.Status == "processing" {
				continue
			}
			delete(e.messages, m.ID)
			delete(e.dedupe, conv+"|"+m.ExternalID)
			drop--
		}
	}

	// 2) 会话数量上限：淘汰最久未活跃且空闲的会话
	if len(byConv) > e.maxConversations {
		type convState struct {
			id     string
			active time.Time
			idle   bool
		}
		states := make([]convState, 0, len(byConv))
		for conv, list := range byConv {
			latest := time.Time{}
			idle := e.active[conv] == ""
			for _, m := range list {
				if m.CreatedAt.After(latest) {
					latest = m.CreatedAt
				}
				if m.Status == "queued" || m.Status == "processing" {
					idle = false
				}
			}
			states = append(states, convState{id: conv, active: latest, idle: idle})
		}
		sort.Slice(states, func(i, j int) bool { return states[i].active.Before(states[j].active) })
		remove := len(states) - e.maxConversations
		for _, st := range states {
			if remove == 0 {
				break
			}
			if !st.idle {
				continue
			}
			for id, m := range e.messages {
				if m.ConversationID == st.id {
					delete(e.messages, id)
					delete(e.dedupe, st.id+"|"+m.ExternalID)
				}
			}
			remove--
		}
	}

	// 3) 运行与投递上限（保留最近）
	if len(e.runOrder) > e.maxRuns {
		excess := len(e.runOrder) - e.maxRuns
		kept := e.runOrder[:0]
		for _, id := range e.runOrder {
			if excess > 0 {
				if _, active := e.active[runConv(e.runs[id])]; !active {
					delete(e.runs, id)
					delete(e.events, id)
					excess--
					continue
				}
			}
			kept = append(kept, id)
		}
		e.runOrder = kept
	}
	if len(e.deliveries) > e.maxDeliveries {
		list := make([]*Delivery, 0, len(e.deliveries))
		for _, d := range e.deliveries {
			list = append(list, d)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
		for _, d := range list[:len(list)-e.maxDeliveries] {
			if d.State == "pending" || d.State == "retrying" {
				continue
			}
			delete(e.deliveries, d.IdempotencyKey)
		}
	}
}

func runConv(r *Run) string {
	if r == nil {
		return ""
	}
	return r.ConversationID
}

// Flush 处理所有"有排队消息且无活动运行"的会话。
//
// @remarks 生产环境由定时器驱动；测试可以显式调用以获得确定性。
func (e *Engine) Flush(ctx context.Context) error {
	e.prune()
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

// Generation 返回会话当前代。
func (e *Engine) Generation(conv string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.generationLocked(conv)
}

func (e *Engine) generation(conv string) int { return e.generationLocked(conv) }

func (e *Engine) generationLocked(conv string) int {
	if g := e.generations[conv]; g > 0 {
		return g
	}
	return 1
}

func (e *Engine) recordEvent(runID string, ev RunEvent) {
	if runID == "" {
		return
	}
	ev.RunID = runID
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = e.now()
	}
	e.mu.Lock()
	list := append(e.events[runID], ev)
	if len(list) > 100 {
		list = list[len(list)-100:]
	}
	e.events[runID] = list
	e.mu.Unlock()

	if e.journal != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := e.journal.SaveRunEvent(ctx, ev); err != nil {
			e.log.Debug("persist run event failed", "error", err)
		}
	}
}

// RunEvents 返回一次运行的事件列表。
//
// @remarks 内存缺失时从数据库补齐，因此重启后仍可回放。
func (e *Engine) RunEvents(runID string) []RunEvent {
	e.mu.Lock()
	cached := e.events[runID]
	e.mu.Unlock()
	if len(cached) == 0 && e.journal != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if loaded, err := e.journal.LoadRunEvents(ctx, runID); err == nil && len(loaded) > 0 {
			e.mu.Lock()
			e.events[runID] = loaded
			e.mu.Unlock()
			cached = loaded
		}
	}
	out := make([]RunEvent, len(cached))
	copy(out, cached)
	return out
}

// RunOwner 返回运行所属会话的用户标识（会话键最后一段）。
func (e *Engine) RunOwner(runID string) string {
	run := e.RunByID(runID)
	if run == nil {
		return ""
	}
	parts := strings.Split(run.ConversationID, ":")
	return parts[len(parts)-1]
}

// StopRun 停止指定运行所属会话的当前批次。
func (e *Engine) StopRun(runID string) bool {
	run := e.RunByID(runID)
	if run == nil {
		return false
	}
	return e.Stop(run.ConversationID)
}

// RunByID 返回指定运行。
func (e *Engine) RunByID(runID string) *Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.runs[runID]
	if r == nil {
		return nil
	}
	c := *r
	return &c
}

// DeliveriesFor 返回某次运行的投递记录。
func (e *Engine) DeliveriesFor(runID string) []*Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*Delivery
	for _, d := range e.deliveries {
		if d.RunID == runID {
			c := *d
			out = append(out, &c)
		}
	}
	return out
}

// Status 返回会话的排队数与当前运行。
func (e *Engine) Status(conv string) (queued int, runID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range e.order[conv] {
		if m := e.messages[id]; m != nil && m.Status == "queued" {
			queued++
		}
	}
	return queued, e.active[conv]
}

// Clear 取消会话全部排队消息，返回取消数量。
func (e *Engine) Clear(conv string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	cancelled := 0
	for _, id := range e.order[conv] {
		if m := e.messages[id]; m != nil && m.Status == "queued" {
			m.Status = "cancelled"
			cancelled++
		}
	}
	e.order[conv] = nil
	return cancelled
}

// Reset 停止当前运行、清空排队并开启新会话代。
//
// @remarks 新代不再携带旧历史；旧消息仍保留在库中供追溯。
func (e *Engine) Reset(conv string) bool {
	stopped := e.Stop(conv)
	e.Clear(conv)
	e.mu.Lock()
	e.generations[conv] = e.generationLocked(conv) + 1
	gen := e.generations[conv]
	e.mu.Unlock()
	if e.journal != nil {
		if err := e.journal.SaveGeneration(context.Background(), conv, gen); err != nil {
			e.log.Error("persist generation failed", "error", err)
		}
	}
	return stopped
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
	copies := make([]*Message, len(out))
	for i, m := range out {
		c := *m
		copies[i] = &c
	}
	return copies
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
			c := *r
			out = append(out, &c)
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
		c := *d
		out = append(out, &c)
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

	if e.journal != nil {
		if err := e.journal.SaveRun(ctx, run); err != nil {
			e.log.Error("persist run failed", "error", err, "run", run.ID)
		}
		if err := e.journal.SetMessageStatus(ctx, run.InputIDs, "processing"); err != nil {
			e.log.Error("persist message status failed", "error", err)
		}
	}

	var cardHandle string
	if e.presenter != nil {
		handle, err := e.presenter.Open(ctx, Presentation{
			ConversationID: conv,
			ChatID:         batch[0].ChatID,
			ReplyTo:        batch[0].ExternalID,
			RunID:          run.ID,
			Inputs:         len(batch),
		})
		if err != nil {
			e.log.Warn("presenter open failed; fallback to text", "error", err, "run", run.ID)
		} else {
			cardHandle = handle
		}
	}

	lastProgress := time.Time{}
	progress := func(phase string) {
		e.recordEvent(run.ID, RunEvent{Kind: "phase", Name: phase})
		if cardHandle == "" || e.presenter == nil || phase == "" {
			return
		}
		if time.Since(lastProgress) < 800*time.Millisecond {
			return
		}
		lastProgress = time.Now()
		if err := e.presenter.Update(ctx, cardHandle, phase, ""); err != nil {
			e.log.Debug("presenter update failed", "error", err)
		}
	}

	input := agent.Input{
		ConversationID: conv,
		Progress:       progress,
		History:        e.history(conv, batch),
		OnTool: func(name string, dur time.Duration, err error) {
			detail := "ok"
			if err != nil {
				detail = err.Error()
			}
			e.recordEvent(run.ID, RunEvent{Kind: "tool", Name: name, Detail: detail, DurationMS: dur.Milliseconds()})
		},
	}
	for _, m := range batch {
		input.Messages = append(input.Messages, agent.Message{ID: m.ID, Text: m.Text, Attachments: m.Attachments})
	}

	out, err := e.agent.Run(runCtx, input)
	cancel()

	e.mu.Lock()
	run.EndedAt = e.now()
	run.InputTokens = out.Usage.InputTokens
	run.OutputTokens = out.Usage.OutputTokens
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
	cardDone := false
	if cardHandle != "" && e.presenter != nil {
		if err := e.presenter.Close(ctx, cardHandle, text, run.State != "completed"); err != nil {
			e.log.Warn("presenter close failed; fallback to text", "error", err, "run", run.ID)
		} else {
			cardDone = true
		}
	}

	delivery := &Delivery{
		CreatedAt:      e.now(),
		RunID:          run.ID,
		IdempotencyKey: run.ID + ":result",
		ChatID:         batch[0].ChatID,
		ReplyTo:        batch[0].ExternalID,
		Text:           text,
		State:          "pending",
	}
	if cardDone {
		delivery.State = "sent"
		delivery.ExternalID = cardHandle
	}
	e.deliveries[delivery.IdempotencyKey] = delivery
	alreadySent := e.sent[delivery.IdempotencyKey]
	e.mu.Unlock()

	e.appendAssistant(conv, run.ID, text)
	if e.metrics != nil {
		e.metrics.Inc("agenthub_runs_total", map[string]string{"state": run.State})
	}

	if e.journal != nil {
		if err := e.journal.SaveRun(ctx, run); err != nil {
			e.log.Error("persist run failed", "error", err, "run", run.ID)
		}
		if err := e.journal.SetMessageStatus(ctx, run.InputIDs, batch[0].Status); err != nil {
			e.log.Error("persist message status failed", "error", err)
		}
		if err := e.journal.SaveDelivery(ctx, delivery); err != nil {
			e.log.Error("persist delivery failed", "error", err, "delivery", delivery.IdempotencyKey)
		}
	}

	if alreadySent || cardDone {
		return nil
	}
	e.send(ctx, delivery)
	return nil
}

// history 返回本批之前的最近对话，用于多轮上下文。
func (e *Engine) history(conv string, batch []*Message) []agent.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	skip := make(map[string]struct{}, len(batch))
	for _, m := range batch {
		skip[m.ID] = struct{}{}
	}
	msgs := make([]*Message, 0, len(e.messages))
	for _, m := range e.messages {
		if m.ConversationID != conv {
			continue
		}
		if _, ok := skip[m.ID]; ok {
			continue
		}
		if m.Status != "done" && m.Status != "failed" && m.Status != "stopped" {
			continue
		}
		if m.Generation != e.generationLocked(conv) {
			continue
		}
		msgs = append(msgs, m)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].CreatedAt.Before(msgs[j].CreatedAt) })
	if len(msgs) > 20 {
		msgs = msgs[len(msgs)-20:]
	}
	out := make([]agent.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, agent.Message{ID: m.ID, Text: m.Text, Attachments: m.Attachments, Role: m.Role})
	}
	return out
}

// appendAssistant 记录助手回复，供下一轮作为上下文。
func (e *Engine) appendAssistant(conv, runID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	msg := &Message{
		ID:             "asst_" + runID,
		Role:           "assistant",
		Generation:     e.Generation(conv),
		ConversationID: conv,
		ChatID:         "",
		Status:         "done",
		Text:           text,
		CreatedAt:      e.now(),
	}
	e.mu.Lock()
	e.messages[msg.ID] = msg
	e.mu.Unlock()
	if e.journal != nil {
		if _, err := e.journal.SaveMessage(context.Background(), msg); err != nil {
			e.log.Error("persist assistant message failed", "error", err)
		}
	}
}

// maxDeliveryAttempts 是投递重试上限。
const maxDeliveryAttempts = 3

func (e *Engine) send(ctx context.Context, d *Delivery) {
	if e.ch == nil {
		e.mu.Lock()
		d.State = "sent"
		e.sent[d.IdempotencyKey] = true
		e.mu.Unlock()
		e.persistDelivery(ctx, d)
		return
	}
	_, err := e.ch.SendText(ctx, channel.Outbound{ChatID: d.ChatID, ReplyTo: d.ReplyTo, Text: d.Text})
	e.mu.Lock()
	d.Attempts++
	if err == nil {
		d.State = "sent"
		d.Error = ""
		e.sent[d.IdempotencyKey] = true
		e.mu.Unlock()
		e.persistDelivery(ctx, d)
		return
	}
	d.Error = err.Error()
	retry := d.Attempts < maxDeliveryAttempts
	if retry {
		d.State = "retrying"
	} else {
		d.State = "failed"
	}
	attempt := d.Attempts
	e.mu.Unlock()
	if e.metrics != nil {
		e.metrics.Inc("agenthub_deliveries_total", map[string]string{"result": d.State})
	}

	if !retry {
		e.log.Warn("delivery failed permanently", "run", d.RunID, "attempts", attempt, "error", err)
		return
	}
	delay := e.retryDelay(attempt)
	e.mu.Lock()
	d.NextAttempt = time.Now().Add(delay)
	e.mu.Unlock()
	e.persistDelivery(ctx, d)
	e.log.Warn("delivery failed; scheduling retry", "run", d.RunID, "attempt", attempt, "delay", delay, "error", err)
	time.AfterFunc(delay, func() {
		e.send(context.Background(), d)
	})
}

func (e *Engine) persistDelivery(ctx context.Context, d *Delivery) {
	if e.journal == nil {
		return
	}
	if err := e.journal.SaveDelivery(ctx, d); err != nil {
		e.log.Error("persist delivery failed", "error", err, "delivery", d.IdempotencyKey)
	}
}

func conversationKey(in channel.Inbound) string {
	return in.Tenant + ":" + in.Chat.ID + ":" + in.Sender.OpenID
}
