package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Employee 是一个数字员工。
type Employee struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Title        string    `json:"title"`
	Role         string    `json:"role"`
	Mission      string    `json:"mission"`
	Status       string    `json:"status"`
	ChatID       string    `json:"chat_id"`
	AllowedTools []string  `json:"allowed_tools"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Case 是一个统一工作项：事故、工单或任务。
type Case struct {
	ID          string     `json:"id"`
	EmployeeID  string     `json:"employee_id"`
	Type        string     `json:"type"`
	Source      string     `json:"source"`
	SourceID    string     `json:"source_id"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Severity    string     `json:"severity"`
	Status      string     `json:"status"`
	Requester   string     `json:"requester"`
	Service     string     `json:"service"`
	Environment string     `json:"environment"`
	ChatID      string     `json:"chat_id"`
	SLADueAt    *time.Time `json:"sla_due_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

// CaseEvent 是案件时间线。
type CaseEvent struct {
	ID        int64     `json:"id"`
	CaseID    string    `json:"case_id"`
	Kind      string    `json:"kind"`
	Actor     string    `json:"actor"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// CaseFeedback 是值班人对案件的反馈。
type CaseFeedback struct {
	ID        int64     `json:"id"`
	CaseID    string    `json:"case_id"`
	Actor     string    `json:"actor"`
	Verdict   string    `json:"verdict"`
	Note      string    `json:"note"`
	CSAT      int       `json:"csat"`
	CreatedAt time.Time `json:"created_at"`
}

// CaseReview 是人工五维评审。
type CaseReview struct {
	ID         int64     `json:"id"`
	CaseID     string    `json:"case_id"`
	Reviewer   string    `json:"reviewer"`
	Evidence   int       `json:"evidence"`
	RootCause  int       `json:"root_cause"`
	Safety     int       `json:"safety"`
	Usefulness int       `json:"usefulness"`
	Honesty    int       `json:"honesty"`
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
}

// EmployeeStats 是员工绩效快照。
type EmployeeStats struct {
	EmployeeID   string  `json:"employee_id"`
	Cases        int     `json:"cases"`
	Resolved     int     `json:"resolved"`
	Useful       int     `json:"useful"`
	NotUseful    int     `json:"not_useful"`
	AvgResolveMS float64 `json:"avg_resolve_ms"`
	CSAT         float64 `json:"csat"`
	Reviews      int     `json:"reviews"`
	AvgEvidence  float64 `json:"avg_evidence"`
	AvgRootCause float64 `json:"avg_root_cause"`
	AvgSafety    float64 `json:"avg_safety"`
	AvgUseful    float64 `json:"avg_usefulness"`
	AvgHonesty   float64 `json:"avg_honesty"`
}

// Approval 是一次受控动作审批。
type Approval struct {
	ID          string          `json:"id"`
	CaseID      string          `json:"case_id"`
	Action      string          `json:"action"`
	Params      json.RawMessage `json:"params"`
	Status      string          `json:"status"`
	RequestedBy string          `json:"requested_by"`
	ApprovedBy  string          `json:"approved_by"`
	Result      string          `json:"result"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CaseStore 读写数字员工与案件。
type CaseStore struct{ pool *pgxpool.Pool }

// NewCaseStore 构造案件存储。
func NewCaseStore(db *DB) *CaseStore {
	if db == nil {
		return nil
	}
	return &CaseStore{pool: db.Pool()}
}

// EnsureDefaultEmployee 创建默认 SRE 值班员工（幂等）。
func (s *CaseStore) EnsureDefaultEmployee(ctx context.Context, chatID string, now time.Time) (*Employee, error) {
	return s.EnsureEmployee(ctx, Employee{
		ID: "sre-oncall", Name: "SRE 值班员", Title: "SRE 数字同事", Role: "sre-oncall",
		Mission: "接住运维告警，查证据、做诊断、写时间线、完成交接，不执行未经审批的写操作。",
		Status:  "active", ChatID: chatID,
		AllowedTools: []string{"read", "bash", "grep", "find", "ls", "tool_search", "ops_investigate", "submit_report"},
	}, now)
}

// EnsureEmployee 创建或更新一个数字员工。
func (s *CaseStore) EnsureEmployee(ctx context.Context, e Employee, now time.Time) (*Employee, error) {
	if e.Status == "" {
		e.Status = "active"
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO employee (id, name, title, role, mission, status, chat_id, allowed_tools, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, title=EXCLUDED.title, role=EXCLUDED.role,
			mission=EXCLUDED.mission, status=EXCLUDED.status, chat_id=EXCLUDED.chat_id,
			allowed_tools=EXCLUDED.allowed_tools, updated_at=EXCLUDED.updated_at`,
		e.ID, e.Name, e.Title, e.Role, e.Mission, e.Status, e.ChatID, mustJSON(e.AllowedTools), now); err != nil {
		return nil, err
	}
	return s.GetEmployee(ctx, e.ID)
}

// ListEmployees 返回数字员工。
func (s *CaseStore) ListEmployees(ctx context.Context) ([]*Employee, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, title, role, mission, status, chat_id, allowed_tools, created_at, updated_at FROM employee ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Employee
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEmployee 读取员工。
func (s *CaseStore) GetEmployee(ctx context.Context, id string) (*Employee, error) {
	e, err := scanEmployee(s.pool.QueryRow(ctx, `SELECT id, name, title, role, mission, status, chat_id, allowed_tools, created_at, updated_at FROM employee WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

// EnsureCase 按 source + source_id 幂等创建/更新案件。
func (s *CaseStore) EnsureCase(ctx context.Context, employeeID, caseType, source, sourceID, title, summary, severity, service, environment, chatID string, now time.Time) (*Case, bool, error) {
	c, err := s.getCaseBySource(ctx, source, sourceID)
	if err != nil {
		return nil, false, err
	}
	due := now.Add(slaFor(severity))
	if c != nil {
		_, err := s.pool.Exec(ctx, `UPDATE case_item SET title=$2, summary=$3, severity=$4, service=$5, environment=$6, employee_id=$7, sla_due_at=$8, updated_at=$9 WHERE id=$1`,
			c.ID, title, summary, severity, service, environment, employeeID, due, now)
		if err != nil {
			return nil, false, err
		}
		c.Title, c.Summary, c.Severity, c.Service, c.Environment, c.EmployeeID, c.SLADueAt, c.UpdatedAt = title, summary, severity, service, environment, employeeID, &due, now
		return c, false, nil
	}
	id := "case_" + caseShortHash(source+sourceID+now.Format(time.RFC3339Nano))
	c = &Case{ID: id, EmployeeID: employeeID, Type: caseType, Source: source, SourceID: sourceID,
		Title: title, Summary: summary, Severity: severity, Status: "new", Service: service,
		Environment: environment, ChatID: chatID, SLADueAt: &due, CreatedAt: now, UpdatedAt: now}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO case_item (id, employee_id, type, source, source_id, title, summary, severity, status, service, environment, chat_id, sla_due_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)`,
		c.ID, c.EmployeeID, c.Type, c.Source, c.SourceID, c.Title, c.Summary, c.Severity,
		c.Status, c.Service, c.Environment, c.ChatID, due, now); err != nil {
		return nil, false, err
	}
	return c, true, nil
}

// SetCaseStatus 更新案件状态。
func (s *CaseStore) SetCaseStatus(ctx context.Context, id, status string, now time.Time) error {
	var resolved any
	if status == "resolved" || status == "closed" {
		resolved = now
	}
	_, err := s.pool.Exec(ctx, `UPDATE case_item SET status=$2, resolved_at=$3, updated_at=$4 WHERE id=$1`, id, status, resolved, now)
	return err
}

// AddCaseEvent 追加时间线。
func (s *CaseStore) AddCaseEvent(ctx context.Context, caseID, kind, actor, text string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO case_event (case_id, kind, actor, text, created_at) VALUES ($1,$2,$3,$4,$5)`, caseID, kind, actor, text, now)
	return err
}

// CreateApproval 创建待审批动作。
func (s *CaseStore) CreateApproval(ctx context.Context, caseID, action string, params json.RawMessage, requestedBy string, now time.Time) (*Approval, error) {
	id := "apr_" + caseShortHash(caseID+action+now.Format(time.RFC3339Nano))
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	a := &Approval{ID: id, CaseID: caseID, Action: action, Params: params, Status: "pending", RequestedBy: requestedBy, CreatedAt: now, UpdatedAt: now}
	if _, err := s.pool.Exec(ctx, `INSERT INTO approval_request (id, case_id, action, params, status, requested_by, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$7)`,
		a.ID, a.CaseID, a.Action, a.Params, a.Status, a.RequestedBy, now); err != nil {
		return nil, err
	}
	return a, nil
}

// DecideApproval 原子审批；状态不是 pending 时拒绝。
func (s *CaseStore) DecideApproval(ctx context.Context, id, status, actor, result string, now time.Time) (*Approval, bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE approval_request SET status=$2, approved_by=$3, result=$4, updated_at=$5 WHERE id=$1 AND status='pending'`, id, status, actor, result, now)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 0 {
		return nil, false, nil
	}
	a, err := s.GetApproval(ctx, id)
	return a, true, err
}

// SetApprovalResult 写入执行结果。
func (s *CaseStore) SetApprovalResult(ctx context.Context, id, result string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE approval_request SET result=$2, updated_at=$3 WHERE id=$1`, id, result, now)
	return err
}

// GetApproval 读取审批。
func (s *CaseStore) GetApproval(ctx context.Context, id string) (*Approval, error) {
	a := &Approval{}
	err := s.pool.QueryRow(ctx, `SELECT id, case_id, action, params, status, requested_by, approved_by, result, created_at, updated_at FROM approval_request WHERE id=$1`, id).
		Scan(&a.ID, &a.CaseID, &a.Action, &a.Params, &a.Status, &a.RequestedBy, &a.ApprovedBy, &a.Result, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// ListApprovals 返回案件审批。
func (s *CaseStore) ListApprovals(ctx context.Context, caseID string) ([]*Approval, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, case_id, action, params, status, requested_by, approved_by, result, created_at, updated_at FROM approval_request WHERE case_id=$1 ORDER BY created_at DESC`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Approval
	for rows.Next() {
		a := &Approval{}
		if err := rows.Scan(&a.ID, &a.CaseID, &a.Action, &a.Params, &a.Status, &a.RequestedBy, &a.ApprovedBy, &a.Result, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddFeedback 记录案件反馈。
func (s *CaseStore) AddFeedback(ctx context.Context, caseID, actor, verdict, note string, csat int, now time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO case_feedback (case_id, actor, verdict, note, csat, created_at) VALUES ($1,$2,$3,$4,$5,$6)`, caseID, actor, verdict, note, csat, now)
	return err
}

// ListFeedback 返回案件反馈。
func (s *CaseStore) ListFeedback(ctx context.Context, caseID string, limit int) ([]CaseFeedback, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, case_id, actor, verdict, note, csat, created_at FROM case_feedback WHERE case_id=$1 ORDER BY id DESC LIMIT $2`, caseID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CaseFeedback
	for rows.Next() {
		var item CaseFeedback
		if err := rows.Scan(&item.ID, &item.CaseID, &item.Actor, &item.Verdict, &item.Note, &item.CSAT, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// EmployeeStats 汇总员工案件与反馈。
func (s *CaseStore) EmployeeStats(ctx context.Context, employeeID string) (EmployeeStats, error) {
	stats := EmployeeStats{EmployeeID: employeeID}
	if err := s.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status IN ('resolved','closed')) FROM case_item WHERE employee_id=$1`, employeeID).Scan(&stats.Cases, &stats.Resolved); err != nil {
		return stats, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE f.verdict='useful'), count(*) FILTER (WHERE f.verdict='not_useful')
		FROM case_feedback f JOIN case_item c ON c.id=f.case_id WHERE c.employee_id=$1`, employeeID).Scan(&stats.Useful, &stats.NotUseful); err != nil {
		return stats, err
	}
	_ = s.pool.QueryRow(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (resolved_at-created_at))*1000),0)
		FROM case_item WHERE employee_id=$1 AND resolved_at IS NOT NULL`, employeeID).Scan(&stats.AvgResolveMS)
	_ = s.pool.QueryRow(ctx, `
		SELECT COALESCE(AVG(f.csat) FILTER (WHERE f.csat > 0),0)
		FROM case_feedback f JOIN case_item c ON c.id=f.case_id WHERE c.employee_id=$1`, employeeID).Scan(&stats.CSAT)
	_ = s.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(AVG(r.evidence),0), COALESCE(AVG(r.root_cause),0),
		       COALESCE(AVG(r.safety),0), COALESCE(AVG(r.usefulness),0), COALESCE(AVG(r.honesty),0)
		FROM case_review r JOIN case_item c ON c.id=r.case_id WHERE c.employee_id=$1`, employeeID).
		Scan(&stats.Reviews, &stats.AvgEvidence, &stats.AvgRootCause, &stats.AvgSafety, &stats.AvgUseful, &stats.AvgHonesty)
	return stats, nil
}

// ListCases 返回最近案件。
func (s *CaseStore) ListCases(ctx context.Context, limit int) ([]*Case, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, employee_id, type, source, source_id, title, summary, severity, status, requester, service, environment, chat_id, sla_due_at, created_at, updated_at, resolved_at
		FROM case_item ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Case
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCase 读取单个案件。
func (s *CaseStore) GetCase(ctx context.Context, id string) (*Case, error) {
	c, err := scanCase(s.pool.QueryRow(ctx, `
		SELECT id, employee_id, type, source, source_id, title, summary, severity, status, requester, service, environment, chat_id, sla_due_at, created_at, updated_at, resolved_at
		FROM case_item WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// AddReview 保存或更新一次人工评审。
func (s *CaseStore) AddReview(ctx context.Context, review CaseReview, now time.Time) error {
	if review.Reviewer == "" {
		review.Reviewer = "admin"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO case_review (case_id, reviewer, evidence, root_cause, safety, usefulness, honesty, note, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (case_id, reviewer) DO UPDATE SET evidence=EXCLUDED.evidence, root_cause=EXCLUDED.root_cause,
			safety=EXCLUDED.safety, usefulness=EXCLUDED.usefulness, honesty=EXCLUDED.honesty,
			note=EXCLUDED.note, created_at=EXCLUDED.created_at`,
		review.CaseID, review.Reviewer, review.Evidence, review.RootCause, review.Safety, review.Usefulness, review.Honesty, review.Note, now)
	return err
}

// ListReviews 返回案件人工评审。
func (s *CaseStore) ListReviews(ctx context.Context, caseID string) ([]CaseReview, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, case_id, reviewer, evidence, root_cause, safety, usefulness, honesty, note, created_at FROM case_review WHERE case_id=$1 ORDER BY created_at DESC`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CaseReview
	for rows.Next() {
		var item CaseReview
		if err := rows.Scan(&item.ID, &item.CaseID, &item.Reviewer, &item.Evidence, &item.RootCause, &item.Safety, &item.Usefulness, &item.Honesty, &item.Note, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// EscalateOverdue 把超过 SLA 的未结案件升级为等待人工，返回升级的案件 ID。
func (s *CaseStore) EscalateOverdue(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		WITH overdue AS (
			SELECT id FROM case_item
			WHERE sla_due_at IS NOT NULL AND sla_due_at < $1
			  AND status NOT IN ('waiting_human','resolved','closed')
			ORDER BY sla_due_at LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE case_item SET status='waiting_human', updated_at=$1
		WHERE id IN (SELECT id FROM overdue) RETURNING id`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		_ = s.AddCaseEvent(ctx, id, "sla", "system", "超过 SLA，升级等待人工", now)
	}
	return ids, nil
}

// ListCaseEvents 返回时间线。
func (s *CaseStore) ListCaseEvents(ctx context.Context, caseID string, limit int) ([]CaseEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, case_id, kind, actor, text, created_at FROM case_event WHERE case_id=$1 ORDER BY id DESC LIMIT $2`, caseID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CaseEvent
	for rows.Next() {
		var ev CaseEvent
		if err := rows.Scan(&ev.ID, &ev.CaseID, &ev.Kind, &ev.Actor, &ev.Text, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *CaseStore) getCaseBySource(ctx context.Context, source, sourceID string) (*Case, error) {
	c, err := scanCase(s.pool.QueryRow(ctx, `
		SELECT id, employee_id, type, source, source_id, title, summary, severity, status, requester, service, environment, chat_id, sla_due_at, created_at, updated_at, resolved_at
		FROM case_item WHERE source=$1 AND source_id=$2`, source, sourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

type employeeScanner interface{ Scan(dest ...any) error }

func scanEmployee(row employeeScanner) (*Employee, error) {
	e := &Employee{}
	var tools []byte
	if err := row.Scan(&e.ID, &e.Name, &e.Title, &e.Role, &e.Mission, &e.Status, &e.ChatID, &tools, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(tools, &e.AllowedTools)
	return e, nil
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

type caseScanner interface{ Scan(dest ...any) error }

func scanCase(row caseScanner) (*Case, error) {
	c := &Case{}
	err := row.Scan(&c.ID, &c.EmployeeID, &c.Type, &c.Source, &c.SourceID, &c.Title, &c.Summary,
		&c.Severity, &c.Status, &c.Requester, &c.Service, &c.Environment, &c.ChatID,
		&c.SLADueAt, &c.CreatedAt, &c.UpdatedAt, &c.ResolvedAt)
	return c, err
}

func slaFor(severity string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "p1", "sev1":
		return time.Hour
	case "high", "p2", "sev2":
		return 4 * time.Hour
	case "medium", "p3", "sev3":
		return 24 * time.Hour
	default:
		return 72 * time.Hour
	}
}

func caseShortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

// CaseFilter 是案件查询条件。
type CaseFilter struct {
	Status, Type, Source, Query string
	From, To                    *time.Time
	Limit, Offset               int
}

// ListCasesFiltered 返回过滤后的案件和总数。
func (s *CaseStore) ListCasesFiltered(ctx context.Context, f CaseFilter) ([]*Case, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	const where = `($1='' OR status=$1) AND ($2='' OR type=$2) AND ($3='' OR source=$3)
		AND ($4='' OR title ILIKE '%'||$4||'%' OR summary ILIKE '%'||$4||'%' OR service ILIKE '%'||$4||'%')
		AND ($5::timestamptz IS NULL OR created_at >= $5) AND ($6::timestamptz IS NULL OR created_at <= $6)`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM case_item WHERE `+where, f.Status, f.Type, f.Source, f.Query, f.From, f.To).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, employee_id, type, source, source_id, title, summary, severity, status, requester, service, environment, chat_id, sla_due_at, created_at, updated_at, resolved_at
		FROM case_item WHERE `+where+` ORDER BY updated_at DESC LIMIT $7 OFFSET $8`, f.Status, f.Type, f.Source, f.Query, f.From, f.To, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Case
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// FindCaseBySource 按来源和外部 ID 查找案件。
func (s *CaseStore) FindCaseBySource(ctx context.Context, source, sourceID string) (*Case, error) {
	c, err := scanCase(s.pool.QueryRow(ctx, `
		SELECT id, employee_id, type, source, source_id, title, summary, severity, status, requester, service, environment, chat_id, sla_due_at, created_at, updated_at, resolved_at
		FROM case_item WHERE source=$1 AND source_id=$2`, source, sourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// CaseReport 是案件的处置报告。
type CaseReport struct {
	CaseID       string    `json:"case_id"`
	Problem      string    `json:"problem"`
	Impact       string    `json:"impact"`
	Evidence     string    `json:"evidence"`
	RootCause    string    `json:"root_cause"`
	Actions      string    `json:"actions"`
	Verification string    `json:"verification"`
	Prevention   string    `json:"prevention"`
	Status       string    `json:"status"`
	Author       string    `json:"author"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// GetCaseReport 读取处置报告。
func (s *CaseStore) GetCaseReport(ctx context.Context, caseID string) (*CaseReport, error) {
	r := &CaseReport{}
	err := s.pool.QueryRow(ctx, `SELECT case_id, problem, impact, evidence, root_cause, actions, verification, prevention, status, author, created_at, updated_at FROM case_report WHERE case_id=$1`, caseID).
		Scan(&r.CaseID, &r.Problem, &r.Impact, &r.Evidence, &r.RootCause, &r.Actions, &r.Verification, &r.Prevention, &r.Status, &r.Author, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// SaveCaseReport 保存处置报告。
func (s *CaseStore) SaveCaseReport(ctx context.Context, r CaseReport, now time.Time) error {
	if r.Status == "" {
		r.Status = "draft"
	}
	if r.Author == "" {
		r.Author = "admin"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO case_report (case_id, problem, impact, evidence, root_cause, actions, verification, prevention, status, author, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
		ON CONFLICT (case_id) DO UPDATE SET problem=EXCLUDED.problem, impact=EXCLUDED.impact,
			evidence=EXCLUDED.evidence, root_cause=EXCLUDED.root_cause, actions=EXCLUDED.actions,
			verification=EXCLUDED.verification, prevention=EXCLUDED.prevention, status=EXCLUDED.status,
			author=EXCLUDED.author, updated_at=EXCLUDED.updated_at`,
		r.CaseID, r.Problem, r.Impact, r.Evidence, r.RootCause, r.Actions, r.Verification, r.Prevention, r.Status, r.Author, now)
	return err
}

// ListCaseReports 返回最近的处置报告。
func (s *CaseStore) ListCaseReports(ctx context.Context, limit int) ([]CaseReport, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT case_id, problem, impact, evidence, root_cause, actions, verification, prevention, status, author, created_at, updated_at FROM case_report ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CaseReport
	for rows.Next() {
		var r CaseReport
		if err := rows.Scan(&r.CaseID, &r.Problem, &r.Impact, &r.Evidence, &r.RootCause, &r.Actions, &r.Verification, &r.Prevention, &r.Status, &r.Author, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
