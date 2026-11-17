package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// KnowledgeDoc 是知识库文档。
type KnowledgeDoc struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	Team      string    `json:"team"`
	UpdatedAt time.Time `json:"updated_at"`
}

// KnowledgeStore 读写知识库。
type KnowledgeStore struct{ pool *pgxpool.Pool }

// NewKnowledgeStore 构造知识库存储。
func NewKnowledgeStore(db *DB) *KnowledgeStore {
	if db == nil {
		return nil
	}
	return &KnowledgeStore{pool: db.Pool()}
}

// Upsert 保存文档；按 source 幂等。
func (s *KnowledgeStore) Upsert(ctx context.Context, doc KnowledgeDoc) error {
	if doc.ID == "" {
		sum := sha1.Sum([]byte(doc.Source + doc.Title))
		doc.ID = "doc_" + hex.EncodeToString(sum[:])[:16]
	}
	if doc.UpdatedAt.IsZero() {
		doc.UpdatedAt = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO knowledge_doc (id, title, content, source, team, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, content=EXCLUDED.content,
			source=EXCLUDED.source, team=EXCLUDED.team, updated_at=EXCLUDED.updated_at`,
		doc.ID, doc.Title, doc.Content, doc.Source, doc.Team, doc.UpdatedAt)
	return err
}

// Search 返回匹配文档；中文按整句匹配，英文按空格分词。
func (s *KnowledgeStore) Search(ctx context.Context, query, team string, limit int) ([]KnowledgeDoc, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	terms := strings.Fields(query)
	if len(terms) == 0 {
		terms = []string{query}
	}
	where := make([]string, 0, len(terms))
	args := make([]any, 0, len(terms)+2)
	for _, term := range terms {
		args = append(args, "%"+term+"%")
		where = append(where, "(title ILIKE $"+itoa(len(args))+" OR content ILIKE $"+itoa(len(args))+")")
	}
	filter := "(" + strings.Join(where, " OR ") + ")"
	if team != "" {
		args = append(args, team)
		filter += " AND (team = '' OR team = $" + itoa(len(args)) + ")"
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, content, source, team, updated_at FROM knowledge_doc
		WHERE `+filter+`
		ORDER BY updated_at DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeDoc
	for rows.Next() {
		var doc KnowledgeDoc
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.Content, &doc.Source, &doc.Team, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		if len(doc.Content) > 4000 {
			doc.Content = doc.Content[:4000]
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
