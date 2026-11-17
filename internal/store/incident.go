package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Incident 是一次告警聚合形成的事故。
type Incident struct {
	ID             string     `json:"id"`
	Fingerprint    string     `json:"fingerprint"`
	Status         string     `json:"status"`
	Severity       string     `json:"severity"`
	Title          string     `json:"title"`
	Summary        string     `json:"summary"`
	Service        string     `json:"service"`
	Environment    string     `json:"environment"`
	ChatID         string     `json:"chat_id"`
	AlertCount     int        `json:"alert_count"`
	AcknowledgedBy string     `json:"acknowledged_by"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// IncidentSeed 是新建或更新事故时来自告警组的摘要。
type IncidentSeed struct {
	Title       string
	Summary     string
	Severity    string
	Service     string
	Environment string
	ChatID      string
}

// AlertRecord 是单个告警事件。
type AlertRecord struct {
	Fingerprint string
	Status      string
	AlertName   string
	Severity    string
	Summary     string
	Description string
	StartsAt    time.Time
	EndsAt      time.Time
	Labels      map[string]string
	Annotations map[string]string
}

// IncidentEvent 是事故时间线的一条记录。
type IncidentEvent struct {
	ID         int64     `json:"id"`
	IncidentID string    `json:"incident_id"`
	Kind       string    `json:"kind"`
	Actor      string    `json:"actor"`
	Text       string    `json:"text"`
	CreatedAt  time.Time `json:"created_at"`
}

// IncidentStore 读写事故、告警事件与时间线。
type IncidentStore struct{ pool *pgxpool.Pool }

// NewIncidentStore 构造事故存储。
func NewIncidentStore(db *DB) *IncidentStore {
	if db == nil {
		return nil
	}
	return &IncidentStore{pool: db.Pool()}
}

// UpsertIncident 把告警并入活动事故；created 为 true 表示新建。
func (s *IncidentStore) UpsertIncident(ctx context.Context, fingerprint string, seed IncidentSeed, alert AlertRecord, now time.Time) (*Incident, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inc, err := scanIncident(tx.QueryRow(ctx, `
		SELECT id, fingerprint, status, severity, title, summary, service, environment, chat_id,
			alert_count, acknowledged_by, acknowledged_at, resolved_at, created_at, updated_at
		FROM incident WHERE fingerprint = $1 AND status <> 'resolved' FOR UPDATE`, fingerprint))
	created := false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		created = true
		id := "inc_" + shortHash(fingerprint+now.Format(time.RFC3339Nano))
		inc = &Incident{
			ID: id, Fingerprint: fingerprint, Status: "firing", Severity: seed.Severity,
			Title: seed.Title, Summary: seed.Summary, Service: seed.Service,
			Environment: seed.Environment, ChatID: seed.ChatID, AlertCount: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO incident (id, fingerprint, status, severity, title, summary, service, environment, chat_id, alert_count, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,$10,$10)`,
			inc.ID, inc.Fingerprint, inc.Status, inc.Severity, inc.Title, inc.Summary,
			inc.Service, inc.Environment, inc.ChatID, now); err != nil {
			return nil, false, err
		}
	default:
		if err != nil {
			return nil, false, err
		}
		inc.AlertCount++
		inc.UpdatedAt = now
		inc.Severity = seed.Severity
		inc.Summary = seed.Summary
		if _, err := tx.Exec(ctx, `
			UPDATE incident SET severity = $2, summary = $3, updated_at = $4, alert_count = alert_count + 1
			WHERE id = $1`, inc.ID, inc.Severity, inc.Summary, now); err != nil {
			return nil, false, err
		}
	}

	alertID := "alert_" + shortHash(fingerprint+alert.Status+alert.StartsAt.Format(time.RFC3339Nano))
	labels, _ := json.Marshal(alert.Labels)
	annotations, _ := json.Marshal(alert.Annotations)
	if _, err := tx.Exec(ctx, `
		INSERT INTO alert_event (id, incident_id, fingerprint, status, alertname, severity, summary, description, starts_at, ends_at, labels, annotations, received_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (fingerprint, status, starts_at) DO NOTHING`,
		alertID, inc.ID, alert.Fingerprint, alert.Status, alert.AlertName, alert.Severity, alert.Summary,
		alert.Description, nullIncidentTime(alert.StartsAt), nullIncidentTime(alert.EndsAt), labels, annotations, now); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO incident_event (incident_id, kind, actor, text, created_at)
		VALUES ($1, 'alert', 'alertmanager', $2, $3)`, inc.ID, alert.Summary, now); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return inc, created, nil
}

// ResolveIncident 把活动事故标记为已恢复。
func (s *IncidentStore) ResolveIncident(ctx context.Context, fingerprint string, now time.Time) (*Incident, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inc, err := scanIncident(tx.QueryRow(ctx, `
		SELECT id, fingerprint, status, severity, title, summary, service, environment, chat_id,
			alert_count, acknowledged_by, acknowledged_at, resolved_at, created_at, updated_at
		FROM incident WHERE fingerprint = $1 AND status <> 'resolved' FOR UPDATE`, fingerprint))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE incident SET status = 'resolved', resolved_at = $2, updated_at = $2 WHERE id = $1`, inc.ID, now); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO incident_event (incident_id, kind, actor, text, created_at) VALUES ($1,'resolved','alertmanager','告警已恢复',$2)`, inc.ID, now); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	inc.Status = "resolved"
	inc.ResolvedAt = &now
	inc.UpdatedAt = now
	return inc, true, nil
}

// Acknowledge 认领事故。
func (s *IncidentStore) Acknowledge(ctx context.Context, id, actor string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE incident SET status = 'acknowledged', acknowledged_by = $2, acknowledged_at = $3, updated_at = $3
		WHERE id = $1 AND status <> 'resolved'`, id, actor, now)
	return err
}

// AddEvent 追加事故时间线。
func (s *IncidentStore) AddEvent(ctx context.Context, incidentID, kind, actor, text string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO incident_event (incident_id, kind, actor, text, created_at) VALUES ($1,$2,$3,$4,$5)`, incidentID, kind, actor, text, now)
	return err
}

// ListIncidents 返回最近事故。
func (s *IncidentStore) ListIncidents(ctx context.Context, limit int) ([]*Incident, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, fingerprint, status, severity, title, summary, service, environment, chat_id,
			alert_count, acknowledged_by, acknowledged_at, resolved_at, created_at, updated_at
		FROM incident ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// GetIncident 读取单个事故。
func (s *IncidentStore) GetIncident(ctx context.Context, id string) (*Incident, error) {
	inc, err := scanIncident(s.pool.QueryRow(ctx, `
		SELECT id, fingerprint, status, severity, title, summary, service, environment, chat_id,
			alert_count, acknowledged_by, acknowledged_at, resolved_at, created_at, updated_at
		FROM incident WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return inc, err
}

// ListEvents 返回事故时间线。
func (s *IncidentStore) ListEvents(ctx context.Context, incidentID string, limit int) ([]IncidentEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, incident_id, kind, actor, text, created_at FROM incident_event WHERE incident_id = $1 ORDER BY id DESC LIMIT $2`, incidentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IncidentEvent
	for rows.Next() {
		var ev IncidentEvent
		if err := rows.Scan(&ev.ID, &ev.IncidentID, &ev.Kind, &ev.Actor, &ev.Text, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

type incidentScanner interface{ Scan(dest ...any) error }

func scanIncident(row incidentScanner) (*Incident, error) {
	inc := &Incident{}
	err := row.Scan(&inc.ID, &inc.Fingerprint, &inc.Status, &inc.Severity, &inc.Title, &inc.Summary,
		&inc.Service, &inc.Environment, &inc.ChatID, &inc.AlertCount, &inc.AcknowledgedBy,
		&inc.AcknowledgedAt, &inc.ResolvedAt, &inc.CreatedAt, &inc.UpdatedAt)
	return inc, err
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func nullIncidentTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// IncidentFilter 是事故查询条件。
type IncidentFilter struct {
	Status, Service, Query string
	Limit, Offset          int
}

// ListIncidentsFiltered 返回过滤后的事故和总数。
func (s *IncidentStore) ListIncidentsFiltered(ctx context.Context, f IncidentFilter) ([]*Incident, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	const where = `($1='' OR status=$1) AND ($2='' OR service=$2) AND ($3='' OR title ILIKE '%'||$3||'%' OR summary ILIKE '%'||$3||'%' OR service ILIKE '%'||$3||'%')`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM incident WHERE `+where, f.Status, f.Service, f.Query).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, fingerprint, status, severity, title, summary, service, environment, chat_id,
			alert_count, acknowledged_by, acknowledged_at, resolved_at, created_at, updated_at
		FROM incident WHERE `+where+` ORDER BY updated_at DESC LIMIT $4 OFFSET $5`, f.Status, f.Service, f.Query, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, inc)
	}
	return out, total, rows.Err()
}

// ResolveIncidentByID 手动解决指定事故。
func (s *IncidentStore) ResolveIncidentByID(ctx context.Context, id, actor string, now time.Time) (*Incident, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inc, err := scanIncident(tx.QueryRow(ctx, `
		SELECT id, fingerprint, status, severity, title, summary, service, environment, chat_id,
			alert_count, acknowledged_by, acknowledged_at, resolved_at, created_at, updated_at
		FROM incident WHERE id=$1 AND status <> 'resolved' FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE incident SET status='resolved', resolved_at=$2, updated_at=$2 WHERE id=$1`, id, now); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO incident_event (incident_id, kind, actor, text, created_at) VALUES ($1,'resolved',$2,'管理台手动恢复',$3)`, id, actor, now); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	inc.Status = "resolved"
	inc.ResolvedAt = &now
	inc.UpdatedAt = now
	return inc, true, nil
}
