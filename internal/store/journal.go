package store

import (
	"context"
	"encoding/json"
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
		INSERT INTO message_state (id, conversation_id, chat_id, external_id, sender, text, msg_type, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (conversation_id, external_id) DO NOTHING`,
		m.ID, m.ConversationID, m.ChatID, m.ExternalID, m.SenderOpenID, m.Text, m.MsgType, m.Status, m.CreatedAt)
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
		INSERT INTO run_state (id, conversation_id, state, input_ids, output, error, started_at, ended_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
			state = EXCLUDED.state,
			output = EXCLUDED.output,
			error = EXCLUDED.error,
			ended_at = EXCLUDED.ended_at`,
		r.ID, r.ConversationID, r.State, inputs, r.Output, r.Error, r.StartedAt, r.EndedAt)
	return err
}

// SaveDelivery 新建或更新投递记录。
func (j *Journal) SaveDelivery(ctx context.Context, d *core.Delivery) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO delivery_state (idempotency_key, run_id, chat_id, reply_to, text, state, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (idempotency_key) DO UPDATE SET
			state = EXCLUDED.state,
			error = EXCLUDED.error`,
		d.IdempotencyKey, d.RunID, d.ChatID, d.ReplyTo, d.Text, d.State, d.Error)
	return err
}

// LoadRecent 读取最近的消息与运行，用于重启后恢复管理台视图。
func (j *Journal) LoadRecent(ctx context.Context, limit int) ([]*core.Message, []*core.Run, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, conversation_id, chat_id, external_id, sender, text, msg_type, status, created_at
		FROM message_state ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, nil, err
	}
	var messages []*core.Message
	for rows.Next() {
		m := &core.Message{}
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.ChatID, &m.ExternalID, &m.SenderOpenID,
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
		SELECT id, conversation_id, state, input_ids, output, error, started_at, ended_at
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
		if err := runRows.Scan(&r.ID, &r.ConversationID, &r.State, &inputs, &r.Output, &r.Error, &started, &ended); err != nil {
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
