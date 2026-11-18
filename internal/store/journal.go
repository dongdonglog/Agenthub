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
		INSERT INTO message_state (id, role, generation, trace_id, conversation_id, chat_id, external_id, sender, sender_name, text, msg_type, status, created_at, sent_by_admin)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (conversation_id, external_id) DO NOTHING`,
		m.ID, defaultRole(m.Role), defaultGeneration(m.Generation), m.TraceID, m.ConversationID, m.ChatID, m.ExternalID, m.SenderOpenID, m.SenderName, m.Text, m.MsgType, m.Status, m.CreatedAt, m.SentByAdmin)
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

// SaveRun 新建或更新运行。
func (j *Journal) SaveRun(ctx context.Context, r *core.Run) error {
	inputs, _ := json.Marshal(r.InputIDs)
	_, err := j.pool.Exec(ctx, `
		INSERT INTO run_state (id, conversation_id, chat_id, round_no, state, input_ids, output, error, started_at, ended_at, input_tokens, output_tokens, trace_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (id) DO UPDATE SET
			state = EXCLUDED.state,
			output = EXCLUDED.output,
			error = EXCLUDED.error,
			ended_at = EXCLUDED.ended_at,
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			trace_id = EXCLUDED.trace_id`,
		r.ID, r.ConversationID, r.ChatID, r.Round, r.State, inputs, r.Output, r.Error, r.StartedAt, r.EndedAt, r.InputTokens, r.OutputTokens, r.TraceID)
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

// LoadRecent 读取最近的消息与运行，用于重启后恢复管理台视图。
func (j *Journal) LoadRecent(ctx context.Context, limit int) ([]*core.Message, []*core.Run, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, role, generation, trace_id, conversation_id, chat_id, external_id, sender, sender_name, text, msg_type, status, created_at, sent_by_admin
		FROM message_state ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, nil, err
	}
	var messages []*core.Message
	for rows.Next() {
		m := &core.Message{}
		if err := rows.Scan(&m.ID, &m.Role, &m.Generation, &m.TraceID, &m.ConversationID, &m.ChatID, &m.ExternalID, &m.SenderOpenID,
			&m.SenderName, &m.Text, &m.MsgType, &m.Status, &m.CreatedAt, &m.SentByAdmin); err != nil {
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
		SELECT id, conversation_id, state, input_ids, output, error, started_at, ended_at, input_tokens, output_tokens, trace_id
		FROM run_state ORDER BY started_at DESC NULLS LAST LIMIT $1`, limit)
	if err != nil {
		return nil, nil, err
	}
	defer runRows.Close()
	var runs []*core.Run
	for runRows.Next() {
		r := &core.Run{}
		var inputs []byte
		var started, ended *time.Time
		if err := runRows.Scan(&r.ID, &r.ConversationID, &r.State, &inputs, &r.Output, &r.Error, &started, &ended, &r.InputTokens, &r.OutputTokens, &r.TraceID); err != nil {
			return nil, nil, err
		}
		_ = json.Unmarshal(inputs, &r.InputIDs)
		if started != nil {
			r.StartedAt = *started
		}
		if ended != nil {
			r.EndedAt = *ended
		}
		runs = append(runs, r)
	}
	return messages, runs, nil
}

// RecoverStaleRuns 把超时仍处于非终态的运行标记为 interrupted。
//
// @remarks 进程崩溃后内存中的运行状态随之消失，数据库里的 running 行没有
// 任何执行者；不清理会让卡片永远停在「处理中」。exclude 是本进程正在执行的
// 会话，避免误伤；cutoff 取足够长（远超单次运行上限）以免杀掉其它实例的长任务。
func (j *Journal) RecoverStaleRuns(ctx context.Context, cutoff, now time.Time, exclude []string) ([]*core.Run, error) {
	if exclude == nil {
		exclude = []string{}
	}
	rows, err := j.pool.Query(ctx, `
		UPDATE run_state
		SET state = 'interrupted', ended_at = $2, error = '进程中断，未产出结果'
		WHERE state IN ('running', 'stopping') AND started_at IS NOT NULL AND started_at < $1
		  AND conversation_id <> ALL($3)
		RETURNING id, conversation_id, chat_id, trace_id, input_ids`, cutoff, now, exclude)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Run
	for rows.Next() {
		r := &core.Run{State: "interrupted", EndedAt: now, Error: "进程中断，未产出结果"}
		var inputs []byte
		if err := rows.Scan(&r.ID, &r.ConversationID, &r.ChatID, &r.TraceID, &inputs); err != nil {
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
