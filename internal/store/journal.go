package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

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
		INSERT INTO message_state (id, role, generation, trace_id, conversation_id, chat_id, external_id, sender, text, msg_type, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (conversation_id, external_id) DO NOTHING`,
		m.ID, defaultRole(m.Role), defaultGeneration(m.Generation), m.TraceID, m.ConversationID, m.ChatID, m.ExternalID, m.SenderOpenID, m.Text, m.MsgType, m.Status, m.CreatedAt)
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
		INSERT INTO run_state (id, conversation_id, state, input_ids, output, error, started_at, ended_at, input_tokens, output_tokens, trace_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (id) DO UPDATE SET
			state = EXCLUDED.state,
			output = EXCLUDED.output,
			error = EXCLUDED.error,
			ended_at = EXCLUDED.ended_at,
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			trace_id = EXCLUDED.trace_id`,
		r.ID, r.ConversationID, r.State, inputs, r.Output, r.Error, r.StartedAt, r.EndedAt, r.InputTokens, r.OutputTokens, r.TraceID)
	return err
}

// SaveDelivery 新建或更新投递记录。
func (j *Journal) SaveDelivery(ctx context.Context, d *core.Delivery) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO delivery_state (idempotency_key, run_id, chat_id, reply_to, text, state, error, attempts, next_attempt_at, trace_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (idempotency_key) DO UPDATE SET
			state = EXCLUDED.state,
			error = EXCLUDED.error,
			attempts = EXCLUDED.attempts,
			next_attempt_at = EXCLUDED.next_attempt_at,
			trace_id = EXCLUDED.trace_id`,
		d.IdempotencyKey, d.RunID, d.ChatID, d.ReplyTo, d.Text, d.State, d.Error, d.Attempts, nullTime(d.NextAttempt), d.TraceID)
	return err
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

// LoadPendingDeliveries 返回未完成投递，用于启动补偿。
func (j *Journal) LoadPendingDeliveries(ctx context.Context, limit int) ([]*core.Delivery, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT idempotency_key, run_id, chat_id, reply_to, text, state, attempts, next_attempt_at, trace_id
		FROM delivery_state WHERE state IN ('pending','retrying') LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Delivery
	for rows.Next() {
		d := &core.Delivery{}
		var next *time.Time
		if err := rows.Scan(&d.IdempotencyKey, &d.RunID, &d.ChatID, &d.ReplyTo, &d.Text, &d.State, &d.Attempts, &next, &d.TraceID); err != nil {
			return nil, err
		}
		if next != nil {
			d.NextAttempt = *next
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LoadRecent 读取最近的消息与运行，用于重启后恢复管理台视图。
func (j *Journal) LoadRecent(ctx context.Context, limit int) ([]*core.Message, []*core.Run, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, role, generation, trace_id, conversation_id, chat_id, external_id, sender, text, msg_type, status, created_at
		FROM message_state ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, nil, err
	}
	var messages []*core.Message
	for rows.Next() {
		m := &core.Message{}
		if err := rows.Scan(&m.ID, &m.Role, &m.Generation, &m.TraceID, &m.ConversationID, &m.ChatID, &m.ExternalID, &m.SenderOpenID,
			&m.Text, &m.MsgType, &m.Status, &m.CreatedAt); err != nil {
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
