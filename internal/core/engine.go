// Package core 实现受理、会话串行、批次快照与投递的最小闭环。
//
// 本包当前使用内存状态，便于在没有数据库的环境中验证语义；持久化实现
// 将在 M2 接入，接口保持稳定。
package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

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
	profiles    map[string]ProfileSelection
	profileSrc  func() ProfileSelection
	identities  map[string]Identity
	priority    map[string]int
	events      map[string][]RunEvent
	// runSem 限制跨会话并发运行数；nil 表示不限制。
	runSem chan struct{}
	// rateLimit 是每会话每窗口允许的消息数；0 表示不限制。
	rateLimit  int
	rateWindow time.Duration
	hits       map[string][]time.Time
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
		profiles:           make(map[string]ProfileSelection),
		identities:         make(map[string]Identity),
		priority:           make(map[string]int),
		hits:               make(map[string][]time.Time),
		rateWindow:         time.Minute,
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
// SetPriority 覆盖会话的调度优先级并持久化（数值越大越优先）。
func (e *Engine) SetPriority(ctx context.Context, conv string, p int) error {
	e.mu.Lock()
	e.priority[conv] = p
	e.mu.Unlock()
	if e.journal != nil {
		return e.journal.SavePriority(ctx, conv, p)
	}
	return nil
}

// PriorityOf 返回会话已显式设置的优先级。
func (e *Engine) PriorityOf(conv string) (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.priority[conv]
	return p, ok
}

// SetNow 替换时钟，供测试使用。
func (e *Engine) SetNow(fn func() time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if fn != nil {
		e.now = fn
	}
}

// SetConcurrency 限制跨会话并发运行数；n <= 0 表示不限制。
func (e *Engine) SetConcurrency(n int) {
	if n <= 0 {
		e.runSem = nil
		return
	}
	e.runSem = make(chan struct{}, n)
}

// SetRateLimit 设置每会话每窗口的消息上限；n <= 0 关闭限流。
func (e *Engine) SetRateLimit(n int, window time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rateLimit = n
	if window > 0 {
		e.rateWindow = window
	}
}

// SetProfileSource 设置新会话代绑定配置的来源（通常是当前生效 Profile）。
func (e *Engine) SetProfileSource(fn func() ProfileSelection) {
	e.profileSrc = fn
}

// profileFor 返回会话代绑定的配置；首次出现时按当前来源固化。
func (e *Engine) profileFor(ctx context.Context, conv string, generation int) ProfileSelection {
	key := conv + "#" + strconv.Itoa(generation)
	e.mu.Lock()
	if p, ok := e.profiles[key]; ok {
		e.mu.Unlock()
		return p
	}
	e.mu.Unlock()

	var src ProfileSelection
	if e.profileSrc != nil {
		src = e.profileSrc()
	}
	e.mu.Lock()
	if p, ok := e.profiles[key]; ok {
		e.mu.Unlock()
		return p
	}
	e.profiles[key] = src
	e.mu.Unlock()

	if e.journal != nil {
		if err := e.journal.SaveProfileBinding(ctx, conv, generation, src); err != nil {
			e.log.Debug("persist profile binding failed", "error", err)
		}
	}
	return src
}

// RebindProfile 把会话当前代重新绑定到指定 Profile。
//
// @remarks 只影响当前代；下一批运行即生效。数据库写入失败时内存已更新，返回错误供调用方提示。
func (e *Engine) RebindProfile(ctx context.Context, conv string, p ProfileSelection) error {
	gen := e.Generation(conv)
	key := conv + "#" + strconv.Itoa(gen)
	e.mu.Lock()
	e.profiles[key] = p
	e.mu.Unlock()
	if e.journal != nil {
		return e.journal.SaveProfileBinding(ctx, conv, gen, p)
	}
	return nil
}

// ProfileOf 返回会话当前代绑定的 Profile 名称。
func (e *Engine) ProfileOf(conv string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.profiles[conv+"#"+strconv.Itoa(e.generationLocked(conv))]
	return p.Name
}

// newTraceID 生成短 trace 标识，用于贯通消息 → 运行 → 投递日志。
func newTraceID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("tr_%d", time.Now().UnixNano())
	}
	return "tr_" + hex.EncodeToString(b[:])
}

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
	if _, ok := e.priority[conv]; !ok {
		switch in.Chat.Kind {
		case "p2p":
			e.priority[conv] = 10
		default:
			e.priority[conv] = 5
		}
	}
	if e.rateLimit > 0 && !e.allowLocked(conv, e.now()) {
		e.mu.Unlock()
		return false, ErrRateLimited
	}

	e.seq[conv]++
	msg := &Message{
		ID:             fmt.Sprintf("msg_%s_%d", conv, e.seq[conv]),
		ConversationID: conv,
		ChatID:         in.Chat.ID,
		Role:           "user",
		Generation:     e.generation(conv),
		TraceID:        newTraceID(),
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

// Prune 立即按内存上限裁剪历史数据。
//
// @remarks 只裁剪内存，数据库仍是完整事实源；排队与处理中消息不会被删除。
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

// Generation 返回会话当前代。

// RunEvents 返回一次运行的事件列表。
//
// @remarks 内存缺失时从数据库补齐，因此重启后仍可回放。

// RunOwner 返回运行所属会话的用户标识（会话键最后一段）。

// StopRun 停止指定运行所属会话的当前批次。

// RunByID 返回指定运行。

// DeliveriesFor 返回某次运行的投递记录。

// Status 返回会话的排队数与当前运行。

// Clear 取消会话全部排队消息，返回取消数量。

// Reset 停止当前运行、清空排队并开启新会话代。
//
// @remarks 新代不再携带旧历史；旧消息仍保留在库中供追溯。

// Messages 返回某个会话的消息快照（按创建时间排序）。

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

// Runs 返回全部运行的快照。

// Deliveries 返回全部投递记录。

func conversationKey(in channel.Inbound) string {
	return in.Tenant + ":" + in.Chat.ID + ":" + in.Sender.OpenID
}
