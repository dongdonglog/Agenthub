package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moying/agenthub/internal/core"
)

// Journal 是核心状态的 PostgreSQL 实现。
type Journal struct {
	pool *pgxpool.Pool
}

// NewJournal 构造写入器；db 为空时返回 nil，调用方按无持久化处理。
func NewJournal(db *DB) *Journal {
	if db == nil || db.pool == nil {
		return nil
	}
	return &Journal{pool: db.pool}
}

// SaveMessage 写入消息；重复（同会话同外部 ID）返回 false。
func (j *Journal) SaveMessage(ctx context.Context, m *core.Message) (bool, error) {
	tag, err := j.pool.Exec(ctx, `
		INSERT INTO message_state (id, role, generation, trace_id, conversation_id, chat_id, external_id, sender, sender_name, text, msg_type, status, created_at, sent_by_admin, case_id, no_reply, read_only, tool_allow)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (conversation_id, external_id) DO NOTHING`,
		m.ID, defaultRole(m.Role), defaultGeneration(m.Generation), m.TraceID, m.ConversationID, m.ChatID, m.ExternalID, m.SenderOpenID,
		m.SenderName, m.Text, m.MsgType, m.Status, m.CreatedAt, m.SentByAdmin,
		m.CaseID, m.NoReply, m.ReadOnly, mustJSON(m.ToolAllow))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetMessageStatus 批量更新消息状态。
func (j *Journal) SetMessageStatus(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := j.pool.Exec(ctx, `UPDATE message_state SET status = $2 WHERE id = ANY($1)`, ids, status)
	return err
}

// ClaimMessages 原子领取一批消息：只有仍是 queued 的会被置为 processing。
//
// @remarks 跨实例场景下，本地队列可能持有已被别的实例处理过的消息。
// 用一条 UPDATE ... WHERE status='queued' RETURNING 完成「检查 + 占用」，
// 返回的才是真正归属本次运行的输入，避免同一条消息被执行两次。
func (j *Journal) ClaimMessages(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := j.pool.Query(ctx, `
		UPDATE message_state SET status = 'processing'
		WHERE id = ANY($1) AND status = 'queued'
		RETURNING id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// LoadMessageStatuses 读取一批消息的当前状态。
//
// @remarks 跨实例场景下，本地队列可能持有已被别的实例处理过的消息。
// 执行前用数据库状态校准，避免重复执行。
func (j *Journal) LoadMessageStatuses(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := j.pool.Query(ctx, `SELECT id, status FROM message_state WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string, len(ids))
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		out[id] = status
	}
	return out, rows.Err()
}

// TryAcquireConversationLease 尝试拿到会话执行租约。
//
// @remarks 租约把「谁有权执行这个会话」变成数据库事实，跨实例互斥。
// 已过期或自己持有都可以接管；同一行上的 INSERT .. ON CONFLICT 由 PostgreSQL
// 串行化，两个实例同时申请只有一个能拿到。
func (j *Journal) TryAcquireConversationLease(ctx context.Context, conv, owner string, ttl time.Duration) (bool, error) {
	var got bool
	err := j.pool.QueryRow(ctx, `
		INSERT INTO conversation_lease (conversation_id, owner, expires_at)
		VALUES ($1, $2, now() + make_interval(secs => $3))
		ON CONFLICT (conversation_id) DO UPDATE
			SET owner = EXCLUDED.owner, expires_at = EXCLUDED.expires_at
			WHERE conversation_lease.expires_at <= now() OR conversation_lease.owner = EXCLUDED.owner
		RETURNING true`, conv, owner, ttl.Seconds()).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return got, nil
}

// RenewConversationLease 续租；false 表示租约已丢失（可能被别的实例接管）。
func (j *Journal) RenewConversationLease(ctx context.Context, conv, owner string, ttl time.Duration) (bool, error) {
	tag, err := j.pool.Exec(ctx, `
		UPDATE conversation_lease SET expires_at = now() + make_interval(secs => $3)
		WHERE conversation_id = $1 AND owner = $2`, conv, owner, ttl.Seconds())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseConversationLease 释放自己持有的会话租约；过期由清理自然接管。
func (j *Journal) ReleaseConversationLease(ctx context.Context, conv, owner string) error {
	_, err := j.pool.Exec(ctx, `
		DELETE FROM conversation_lease WHERE conversation_id = $1 AND owner = $2`, conv, owner)
	return err
}

// SaveRun 新建或更新运行。
//
// @remarks 两个必须守住的性质：
//
//  1. 时间列写入 NULL 而不是 Go 零值。零值会被写成 0001-01-01，而保留期清理是
//     `ended_at IS NOT NULL AND ended_at < cutoff`——正在运行的行会被当成过期数据
//     删掉，卡片的句柄随之消失，那张卡就永远停在「处理中」且没人能关。
//  2. 卡片字段单调：card_handle 只做「从空到有」的补写，card_closed 只做
//     false→true。运行状态更新绝不能把它们冲回空/false。
func (j *Journal) SaveRun(ctx context.Context, r *core.Run) error {
	inputs, _ := json.Marshal(r.InputIDs)
	_, err := j.pool.Exec(ctx, `
		INSERT INTO run_state (id, conversation_id, chat_id, round_no, state, input_ids, output, error, started_at, ended_at, input_tokens, output_tokens, trace_id, heartbeat_at, card_handle, card_closed)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO UPDATE SET
			state = EXCLUDED.state,
			output = EXCLUDED.output,
			error = EXCLUDED.error,
			ended_at = COALESCE(EXCLUDED.ended_at, run_state.ended_at),
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			trace_id = EXCLUDED.trace_id,
			heartbeat_at = COALESCE(EXCLUDED.heartbeat_at, run_state.heartbeat_at),
			card_handle = COALESCE(NULLIF(EXCLUDED.card_handle, ''), run_state.card_handle),
			card_closed = run_state.card_closed OR EXCLUDED.card_closed`,
		r.ID, r.ConversationID, r.ChatID, r.Round, r.State, inputs, r.Output, r.Error,
		nullTime(r.StartedAt), nullTime(r.EndedAt),
		r.InputTokens, r.OutputTokens, r.TraceID, nullTime(r.HeartbeatAt), r.CardHandle, r.CardClosed)
	return err
}

// MarkCardClosed 记录卡片已写入终态。
func (j *Journal) MarkCardClosed(ctx context.Context, id string) error {
	_, err := j.pool.Exec(ctx, `
		UPDATE run_state SET card_closed = true WHERE id = $1`, id)
	return err
}

// BumpCardCloseAttempt 记录一次卡片结算失败，返回累计失败次数。
func (j *Journal) BumpCardCloseAttempt(ctx context.Context, id string) (int, error) {
	var attempts int
	err := j.pool.QueryRow(ctx, `
		UPDATE run_state SET card_close_attempts = card_close_attempts + 1
		WHERE id = $1
		RETURNING card_close_attempts`, id).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return attempts, err
}

// LoadUnclosedCards 返回已结束但卡片尚未收尾的运行。
//
// @remarks 覆盖「运行跑完了，但改卡片那一步失败」这条路径：卡片会一直停在
// 「处理中」，必须由巡检重试，不能只处理进程崩溃。
func (j *Journal) LoadUnclosedCards(ctx context.Context, limit int) ([]*core.Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := j.pool.Query(ctx, `
		SELECT id, conversation_id, chat_id, state, output, error, trace_id, card_handle
		FROM run_state
		WHERE card_handle <> '' AND card_closed = false AND state NOT IN ('running', 'stopping')
		ORDER BY ended_at DESC NULLS LAST
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Run
	for rows.Next() {
		r := &core.Run{}
		if err := rows.Scan(&r.ID, &r.ConversationID, &r.ChatID, &r.State, &r.Output, &r.Error, &r.TraceID, &r.CardHandle); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HeartbeatRun 刷新运行心跳；只有非终态运行会被更新。
//
// @remarks 进程被 kill 后心跳停止，其它实例据此判定执行者已消失。
func (j *Journal) HeartbeatRun(ctx context.Context, id string, at time.Time) error {
	_, err := j.pool.Exec(ctx, `
		UPDATE run_state SET heartbeat_at = $2
		WHERE id = $1 AND state IN ('running', 'stopping')`, id, at)
	return err
}

// deliveryColumns 是投递箱的完整列，保持读取与写入顺序一致。
const deliveryColumns = `idempotency_key, run_id, chat_id, reply_to, text, kind, name, artifact_id, artifact_path,
	state, error, attempts, next_attempt_at, trace_id, send_started_at,
	lease_owner, lease_token, lease_expires_at, send_phase, external_id, created_at`

type deliveryScanner interface {
	Scan(dest ...any) error
}

func scanDelivery(row deliveryScanner) (*core.Delivery, error) {
	d := &core.Delivery{}
	var next, started, leaseExpires, created *time.Time
	err := row.Scan(&d.IdempotencyKey, &d.RunID, &d.ChatID, &d.ReplyTo, &d.Text,
		&d.Kind, &d.Name, &d.ArtifactID, &d.ArtifactPath,
		&d.State, &d.Error, &d.Attempts, &next, &d.TraceID, &started,
		&d.LeaseOwner, &d.LeaseToken, &leaseExpires, &d.SendPhase, &d.ExternalID, &created)
	if err != nil {
		return nil, err
	}
	if next != nil {
		d.NextAttempt = *next
	}
	if started != nil {
		d.SendStartedAt = *started
	}
	if leaseExpires != nil {
		d.LeaseExpiresAt = *leaseExpires
	}
	if created != nil {
		d.CreatedAt = *created
	}
	return d, nil
}

// SaveDelivery 新建或更新投递记录。
func (j *Journal) SaveDelivery(ctx context.Context, d *core.Delivery) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO delivery_state (idempotency_key, run_id, chat_id, reply_to, text, kind, name, artifact_id, artifact_path, state, error, attempts, next_attempt_at, trace_id, send_started_at, lease_owner, lease_token, lease_expires_at, send_phase, external_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT (idempotency_key) DO UPDATE SET
			state = EXCLUDED.state,
			error = EXCLUDED.error,
			attempts = EXCLUDED.attempts,
			next_attempt_at = EXCLUDED.next_attempt_at,
			trace_id = EXCLUDED.trace_id,
			send_started_at = EXCLUDED.send_started_at,
			lease_owner = EXCLUDED.lease_owner,
			lease_token = EXCLUDED.lease_token,
			lease_expires_at = EXCLUDED.lease_expires_at,
			send_phase = EXCLUDED.send_phase,
			external_id = EXCLUDED.external_id`,
		d.IdempotencyKey, d.RunID, d.ChatID, d.ReplyTo, d.Text, d.Kind, d.Name, d.ArtifactID, d.ArtifactPath, d.State, d.Error, d.Attempts, nullTime(d.NextAttempt), d.TraceID, nullTime(d.SendStartedAt), d.LeaseOwner, d.LeaseToken, nullTime(d.LeaseExpiresAt), d.SendPhase, d.ExternalID)
	return err
}

// ClaimDelivery 原子领取一条到期投递；同一投递只能被一个发送者领取。
func (j *Journal) ClaimDelivery(ctx context.Context, key, owner, token string, lease time.Duration) (*core.Delivery, bool, error) {
	row := j.pool.QueryRow(ctx, `
		UPDATE delivery_state SET
			state = 'sending',
			attempts = attempts + 1,
			lease_owner = $2,
			lease_token = $3,
			lease_expires_at = now() + make_interval(secs => $4),
			send_started_at = now(),
			send_phase = 'claimed',
			error = ''
		WHERE idempotency_key = $1
		  AND state IN ('pending','retrying')
		  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		  AND (lease_expires_at IS NULL OR lease_expires_at <= now())
		RETURNING `+deliveryColumns,
		key, owner, token, lease.Seconds())
	d, err := scanDelivery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return d, true, nil
}

// MarkDeliveryCalling 把已领取的投递推进到调用通道阶段。
func (j *Journal) MarkDeliveryCalling(ctx context.Context, key, token string) (bool, error) {
	tag, err := j.pool.Exec(ctx, `
		UPDATE delivery_state SET send_phase = 'calling'
		WHERE idempotency_key = $1 AND lease_token = $2 AND state = 'sending' AND send_phase = 'claimed'`, key, token)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// FinishDelivery 在租约令牌匹配时写入发送终态或重试状态。
func (j *Journal) FinishDelivery(ctx context.Context, key, token, state, errText, externalID string, next time.Time) (bool, error) {
	tag, err := j.pool.Exec(ctx, `
		UPDATE delivery_state SET
			state = $3,
			error = $4,
			external_id = $5,
			next_attempt_at = $6,
			lease_owner = '',
			lease_token = '',
			lease_expires_at = NULL,
			send_phase = ''
		WHERE idempotency_key = $1 AND lease_token = $2 AND state = 'sending'`,
		key, token, state, errText, externalID, nullTime(next))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RecoverDeliveries 回收过期租约：claimed 可重试，calling/旧发送中进入歧义。
func (j *Journal) RecoverDeliveries(ctx context.Context, limit int) ([]*core.Delivery, error) {
	tx, err := j.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT `+deliveryColumns+`
		FROM delivery_state
		WHERE state = 'sending' AND (lease_expires_at IS NULL OR lease_expires_at <= now())
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	var items []*core.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for _, d := range items {
		if d.SendPhase == "claimed" {
			d.State = "retrying"
			d.Error = "发送认领租约过期，重新投递"
			d.NextAttempt = time.Time{}
		} else {
			d.State = "ambiguous"
			d.Error = "发送结果不确定；已停止自动重试，请人工核对"
		}
		d.LeaseOwner = ""
		d.LeaseToken = ""
		d.LeaseExpiresAt = time.Time{}
		d.SendPhase = ""
		if _, err := tx.Exec(ctx, `
			UPDATE delivery_state SET state = $2, error = $3, next_attempt_at = $4,
				lease_owner = '', lease_token = '', lease_expires_at = NULL, send_phase = ''
			WHERE idempotency_key = $1 AND state = 'sending'`,
			d.IdempotencyKey, d.State, d.Error, nullTime(d.NextAttempt)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// SaveArtifact 登记运行产物；同一 ID 重复写入保持幂等。
func (j *Journal) SaveArtifact(ctx context.Context, a *core.Artifact) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO run_artifact (id, run_id, conversation_id, name, kind, size, sha256, path, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO NOTHING`,
		a.ID, a.RunID, a.ConversationID, a.Name, a.Kind, a.Size, a.SHA256, a.Path, a.CreatedAt)
	return err
}

// LoadArtifacts 返回一次运行的全部产物。
func (j *Journal) LoadArtifacts(ctx context.Context, runID string) ([]*core.Artifact, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, run_id, conversation_id, name, kind, size, sha256, path, created_at
		FROM run_artifact WHERE run_id = $1 ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Artifact
	for rows.Next() {
		a := &core.Artifact{}
		if err := rows.Scan(&a.ID, &a.RunID, &a.ConversationID, &a.Name, &a.Kind, &a.Size, &a.SHA256, &a.Path, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LoadArtifact 按 ID 读取产物。
func (j *Journal) LoadArtifact(ctx context.Context, id string) (*core.Artifact, error) {
	a := &core.Artifact{}
	err := j.pool.QueryRow(ctx, `
		SELECT id, run_id, conversation_id, name, kind, size, sha256, path, created_at
		FROM run_artifact WHERE id = $1`, id).
		Scan(&a.ID, &a.RunID, &a.ConversationID, &a.Name, &a.Kind, &a.Size, &a.SHA256, &a.Path, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func defaultGeneration(gen int) int {
	if gen <= 0 {
		return 1
	}
	return gen
}

// SaveGeneration 记录会话当前代。
func (j *Journal) SaveGeneration(ctx context.Context, conversationID string, generation int) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO conversation_state (id, chat_id, sender, generation)
		VALUES ($1, '', '', $2)
		ON CONFLICT (id) DO UPDATE SET generation = EXCLUDED.generation, last_active = now()`,
		conversationID, generation)
	return err
}

// LoadGenerations 读取全部会话的当前代。
func (j *Journal) LoadGenerations(ctx context.Context) (map[string]int, error) {
	rows, err := j.pool.Query(ctx, `SELECT id, generation FROM conversation_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var id string
		var gen int
		if err := rows.Scan(&id, &gen); err != nil {
			return nil, err
		}
		out[id] = gen
	}
	return out, rows.Err()
}

func defaultRole(role string) string {
	if role == "" {
		return "user"
	}
	return role
}

// SavePriority 记录会话调度优先级；priority 为 nil 表示清除。
func (j *Journal) SavePriority(ctx context.Context, conv string, priority int) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO conversation_state (id, chat_id, sender, generation, priority)
		VALUES ($1, split_part($1, ':', 2), split_part($1, ':', 3), 1, $2)
		ON CONFLICT (id) DO UPDATE SET priority = EXCLUDED.priority`,
		conv, priority)
	return err
}

// LoadPriorities 读取显式设置过的会话优先级。
func (j *Journal) LoadPriorities(ctx context.Context, limit int) (map[string]int, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, priority FROM conversation_state
		WHERE priority IS NOT NULL LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var conv string
		var priority int
		if err := rows.Scan(&conv, &priority); err != nil {
			return nil, err
		}
		out[conv] = priority
	}
	return out, rows.Err()
}

// SaveIdentity 记录会话的群名与用户名。
func (j *Journal) SaveIdentity(ctx context.Context, conv, chatName, userName string) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO conversation_state (id, chat_id, sender, generation, chat_name, user_name)
		VALUES ($1, split_part($1, ':', 2), split_part($1, ':', 3), 1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET
			chat_name = EXCLUDED.chat_name,
			user_name = EXCLUDED.user_name`,
		conv, chatName, userName)
	return err
}

// LoadIdentities 读取会话展示名。
func (j *Journal) LoadIdentities(ctx context.Context, limit int) (map[string][2]string, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, chat_name, user_name FROM conversation_state
		WHERE chat_name <> '' OR user_name <> '' LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][2]string)
	for rows.Next() {
		var conv, chat, user string
		if err := rows.Scan(&conv, &chat, &user); err != nil {
			return nil, err
		}
		out[conv] = [2]string{chat, user}
	}
	return out, rows.Err()
}

// SaveProfileBinding 记录会话代绑定的配置档案。
func (j *Journal) SaveProfileBinding(ctx context.Context, conv string, generation int, p core.ProfileSelection) error {
	skills, _ := json.Marshal(p.Skills)
	mcps, _ := json.Marshal(p.MCPs)
	_, err := j.pool.Exec(ctx, `
		INSERT INTO conversation_profile (conversation_id, generation, profile_name, skills, mcps)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (conversation_id, generation) DO UPDATE SET
			profile_name = EXCLUDED.profile_name,
			skills = EXCLUDED.skills,
			mcps = EXCLUDED.mcps`,
		conv, generation, p.Name, skills, mcps)
	return err
}

// LoadProfileBindings 读取全部会话代绑定。
func (j *Journal) LoadProfileBindings(ctx context.Context, limit int) (map[string]core.ProfileSelection, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT conversation_id, generation, profile_name, skills, mcps
		FROM conversation_profile ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]core.ProfileSelection)
	for rows.Next() {
		var conv, name string
		var gen int
		var skillsRaw, mcpsRaw []byte
		if err := rows.Scan(&conv, &gen, &name, &skillsRaw, &mcpsRaw); err != nil {
			return nil, err
		}
		p := core.ProfileSelection{Name: name}
		_ = json.Unmarshal(skillsRaw, &p.Skills)
		_ = json.Unmarshal(mcpsRaw, &p.MCPs)
		out[profileKey(conv, gen)] = p
	}
	return out, rows.Err()
}

func profileKey(conv string, generation int) string {
	return conv + "#" + strconv.Itoa(generation)
}

// SaveRunEvent 追加一条运行事件。
func (j *Journal) SaveRunEvent(ctx context.Context, ev core.RunEvent) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO run_trace (run_id, kind, name, detail, duration_ms, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		ev.RunID, ev.Kind, ev.Name, ev.Detail, ev.DurationMS, ev.CreatedAt)
	return err
}

// LoadRunEvents 读取一次运行的事件。
func (j *Journal) LoadRunEvents(ctx context.Context, runID string) ([]core.RunEvent, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT run_id, kind, name, detail, duration_ms, created_at
		FROM run_trace WHERE run_id = $1 ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.RunEvent
	for rows.Next() {
		var ev core.RunEvent
		if err := rows.Scan(&ev.RunID, &ev.Kind, &ev.Name, &ev.Detail, &ev.DurationMS, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// PurgeBefore 删除早于 cutoff 的历史数据，返回删除行数。
//
// @remarks 排队中的消息与未完成的投递不会被删除，避免误删待处理工作。
func (j *Journal) PurgeBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	statements := []string{
		`DELETE FROM delivery_state WHERE created_at < $1 AND state NOT IN ('pending','retrying')`,
		`DELETE FROM run_trace WHERE created_at < $1`,
		`DELETE FROM run_artifact WHERE created_at < $1`,
		`DELETE FROM incident_event WHERE incident_id IN (SELECT id FROM incident WHERE status = 'resolved' AND updated_at < $1)`,
		`DELETE FROM alert_event WHERE incident_id IN (SELECT id FROM incident WHERE status = 'resolved' AND updated_at < $1)`,
		`DELETE FROM incident WHERE status = 'resolved' AND updated_at < $1`,
		`DELETE FROM case_event WHERE case_id IN (SELECT id FROM case_item WHERE status IN ('resolved','closed') AND updated_at < $1)`,
		`DELETE FROM case_feedback WHERE case_id IN (SELECT id FROM case_item WHERE status IN ('resolved','closed') AND updated_at < $1)`,
		`DELETE FROM approval_request WHERE case_id IN (SELECT id FROM case_item WHERE status IN ('resolved','closed') AND updated_at < $1)`,
		`DELETE FROM case_item WHERE status IN ('resolved','closed') AND updated_at < $1`,
		`DELETE FROM run_state WHERE ended_at IS NOT NULL AND ended_at < $1`,
		`DELETE FROM message_state WHERE created_at < $1 AND status NOT IN ('queued','processing')`,
	}
	for _, sql := range statements {
		tag, err := j.pool.Exec(ctx, sql, cutoff)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

// ResolveDeliveryConversation 通过已发送消息 ID 反查目标会话。
func (j *Journal) ResolveDeliveryConversation(ctx context.Context, externalID string) (string, string, bool, error) {
	var conv, chat string
	err := j.pool.QueryRow(ctx, `
		SELECT COALESCE(r.conversation_id, ''), d.chat_id
		FROM delivery_state d
		LEFT JOIN run_state r ON r.id = d.run_id
		WHERE d.external_id = $1 AND d.external_id <> ''
		ORDER BY d.created_at DESC LIMIT 1`, externalID).Scan(&conv, &chat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return conv, chat, conv != "", nil
}

// LoadPendingDeliveries 返回未完成投递，用于启动补偿。
func (j *Journal) LoadPendingDeliveries(ctx context.Context, limit int) ([]*core.Delivery, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT `+deliveryColumns+`
		FROM delivery_state WHERE state IN ('pending','retrying') LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LoadDeliveriesByRun 返回一次运行的全部投递。
func (j *Journal) LoadDeliveriesByRun(ctx context.Context, runID string) ([]*core.Delivery, error) {
	rows, err := j.pool.Query(ctx, `SELECT `+deliveryColumns+` FROM delivery_state WHERE run_id = $1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LoadDelivery 按幂等键读取一条投递。
func (j *Journal) LoadDelivery(ctx context.Context, key string) (*core.Delivery, error) {
	row := j.pool.QueryRow(ctx, `SELECT `+deliveryColumns+` FROM delivery_state WHERE idempotency_key = $1`, key)
	d, err := scanDelivery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// SaveReaction 记录消息状态表情投影。
func (j *Journal) SaveReaction(ctx context.Context, messageID, state, reactionID string) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO reaction_projection (message_id, state, reaction_id, updated_at)
		VALUES ($1,$2,$3,now())
		ON CONFLICT (message_id) DO UPDATE SET
			state = EXCLUDED.state, reaction_id = EXCLUDED.reaction_id, updated_at = now()`,
		messageID, state, reactionID)
	return err
}

// LoadReactions 读取最近的状态表情投影。
func (j *Journal) LoadReactions(ctx context.Context, limit int) (map[string]string, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT message_id, reaction_id FROM reaction_projection
		WHERE reaction_id <> '' ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var messageID, reactionID string
		if err := rows.Scan(&messageID, &reactionID); err != nil {
			return nil, err
		}
		out[messageID] = reactionID
	}
	return out, rows.Err()
}

// messageColumns 是 message_state 的完整列，读取与写入顺序保持一致。
const messageColumns = `id, role, generation, trace_id, conversation_id, chat_id, external_id, sender, sender_name, text, msg_type, status, created_at, sent_by_admin, case_id, no_reply, read_only, tool_allow`

// messageScanner 是消息行的扫描目标。
type messageScanner interface {
	Scan(dest ...any) error
}

// scanMessage 读取一行消息，含重启恢复必须保留的语义字段。
//
// @remarks no_reply/read_only/tool_allow 丢失会导致恢复后的消息去回复内部合成 ID，
// 以及只读诊断变成可写运行。
func scanMessage(row messageScanner) (*core.Message, error) {
	m := &core.Message{}
	var tools []byte
	if err := row.Scan(&m.ID, &m.Role, &m.Generation, &m.TraceID, &m.ConversationID, &m.ChatID,
		&m.ExternalID, &m.SenderOpenID, &m.SenderName, &m.Text, &m.MsgType, &m.Status, &m.CreatedAt,
		&m.SentByAdmin, &m.CaseID, &m.NoReply, &m.ReadOnly, &tools); err != nil {
		return nil, err
	}
	if len(tools) > 0 {
		_ = json.Unmarshal(tools, &m.ToolAllow)
	}
	return m, nil
}

// LoadRecent 读取最近的消息与运行，用于重启后恢复管理台视图。
func (j *Journal) LoadRecent(ctx context.Context, limit int) ([]*core.Message, []*core.Run, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT `+messageColumns+`
		FROM message_state ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, nil, err
	}
	var messages []*core.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		messages = append(messages, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// 反转为时间正序，便于按顺序恢复队列。
	for i, k := 0, len(messages)-1; i < k; i, k = i+1, k-1 {
		messages[i], messages[k] = messages[k], messages[i]
	}

	runRows, err := j.pool.Query(ctx, `
		SELECT id, conversation_id, state, input_ids, output, error, started_at, ended_at, input_tokens, output_tokens, trace_id, heartbeat_at, card_handle, card_closed
		FROM run_state ORDER BY started_at DESC NULLS LAST LIMIT $1`, limit)
	if err != nil {
		return nil, nil, err
	}
	defer runRows.Close()
	var runs []*core.Run
	for runRows.Next() {
		r := &core.Run{}
		var inputs []byte
		var started, ended, heartbeat *time.Time
		if err := runRows.Scan(&r.ID, &r.ConversationID, &r.State, &inputs, &r.Output, &r.Error, &started, &ended,
			&r.InputTokens, &r.OutputTokens, &r.TraceID, &heartbeat, &r.CardHandle, &r.CardClosed); err != nil {
			return nil, nil, err
		}
		_ = json.Unmarshal(inputs, &r.InputIDs)
		if started != nil {
			r.StartedAt = *started
		}
		if ended != nil {
			r.EndedAt = *ended
		}
		if heartbeat != nil {
			r.HeartbeatAt = *heartbeat
		}
		runs = append(runs, r)
	}
	return messages, runs, nil
}

// RecoverStaleRuns 回收心跳已停止的非终态运行，并返回卡片句柄。
//
// @remarks 进程被 kill 或崩溃后心跳不再刷新，数据库里的 running 行没有任何执行者；
// 不清理会让卡片永远停在「处理中」。判定依据是 heartbeat_at（历史数据回落到
// started_at），与运行时长无关，所以长任务不会被误杀。
// exclude 是本进程正在执行的运行 ID——必须按 ID 而不是会话排除：重启时同一会话
// 里可能并存「已死的旧运行」和「刚启动的新运行」，按会话排除会让旧运行永远回收不掉。
func (j *Journal) RecoverStaleRuns(ctx context.Context, cutoff, now time.Time, exclude []string) ([]*core.Run, error) {
	if exclude == nil {
		exclude = []string{}
	}
	rows, err := j.pool.Query(ctx, `
		UPDATE run_state
		SET state = 'interrupted', ended_at = $2, error = '执行进程已中断'
		WHERE state IN ('running', 'stopping')
		  AND COALESCE(heartbeat_at, started_at) IS NOT NULL
		  AND COALESCE(heartbeat_at, started_at) < $1
		  AND id <> ALL($3)
		RETURNING id, conversation_id, chat_id, trace_id, input_ids, card_handle`, cutoff, now, exclude)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Run
	for rows.Next() {
		r := &core.Run{State: "interrupted", EndedAt: now, Error: "执行进程已中断"}
		var inputs []byte
		if err := rows.Scan(&r.ID, &r.ConversationID, &r.ChatID, &r.TraceID, &inputs, &r.CardHandle); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(inputs, &r.InputIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestRunForConversation 返回一个会话最近一次运行。
func (j *Journal) LatestRunForConversation(ctx context.Context, conversationID string) (*core.Run, error) {
	r := &core.Run{}
	var inputs []byte
	var started, ended *time.Time
	err := j.pool.QueryRow(ctx, `
		SELECT id, conversation_id, state, input_ids, output, error, started_at, ended_at, input_tokens, output_tokens, trace_id
		FROM run_state WHERE conversation_id=$1 ORDER BY started_at DESC NULLS LAST LIMIT 1`, conversationID).
		Scan(&r.ID, &r.ConversationID, &r.State, &inputs, &r.Output, &r.Error, &started, &ended, &r.InputTokens, &r.OutputTokens, &r.TraceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(inputs, &r.InputIDs)
	if started != nil {
		r.StartedAt = *started
	}
	if ended != nil {
		r.EndedAt = *ended
	}
	return r, nil
}
