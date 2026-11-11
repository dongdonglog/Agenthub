package store

import (
	"context"
	"time"

	"github.com/moying/agenthub/internal/core"
)

// Generation 是一个会话代的历史摘要。
type Generation struct {
	ConversationID string    `json:"conversation_id"`
	Generation     int       `json:"generation"`
	Messages       int       `json:"messages"`
	LastActive     time.Time `json:"last_active"`
}

// ListGenerations 按会话与代聚合历史。
//
// @param limit 最多返回多少组
func (j *Journal) ListGenerations(ctx context.Context, limit int) ([]Generation, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT conversation_id, generation, count(*), max(created_at)
		FROM message_state
		GROUP BY conversation_id, generation
		ORDER BY max(created_at) DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Generation
	for rows.Next() {
		var g Generation
		if err := rows.Scan(&g.ConversationID, &g.Generation, &g.Messages, &g.LastActive); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListGenerationMessages 读取某个会话代的消息。
func (j *Journal) ListGenerationMessages(ctx context.Context, conv string, generation, limit int) ([]core.Message, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, role, generation, trace_id, conversation_id, chat_id, external_id, sender, text, msg_type, status, created_at
		FROM message_state
		WHERE conversation_id = $1 AND generation = $2
		ORDER BY created_at ASC LIMIT $3`, conv, generation, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Message
	for rows.Next() {
		var m core.Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Generation, &m.TraceID, &m.ConversationID, &m.ChatID,
			&m.ExternalID, &m.SenderOpenID, &m.Text, &m.MsgType, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AuditEntry 是一条管理面审计记录。
type AuditEntry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// SaveAudit 追加一条审计记录。
func (j *Journal) SaveAudit(ctx context.Context, actor, action, target, detail string) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO audit_log (actor, action, target, detail) VALUES ($1,$2,$3,$4)`, actor, action, target, detail)
	return err
}

// ListAudit 读取最近的审计记录。
func (j *Journal) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, actor, action, target, detail, created_at
		FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
