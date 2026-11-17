// Package aiops 接收 Prometheus Alertmanager 告警，聚合事故并交给 agent 诊断。
package aiops

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

// Metrics 是 AIOps 需要的最小指标接口。
type Metrics interface {
	Inc(name string, labels map[string]string)
}

// Config 是告警接入配置。
type Config struct {
	ChatID     string
	RunbookDir string
	MaxAlerts  int
}

// Ingestor 把 Alertmanager webhook 转成事故和一次诊断运行。
type Ingestor struct {
	store    *store.IncidentStore
	cases    *store.CaseStore
	catalog  *Catalog
	employee string
	engine   *core.Engine
	cfg      Config
	log      *slog.Logger
	metrics  Metrics
	now      func() time.Time
}

// NewIngestor 构造告警处理器。
func NewIngestor(st *store.IncidentStore, engine *core.Engine, cfg Config, log *slog.Logger, metrics Metrics) *Ingestor {
	if log == nil {
		log = slog.Default()
	}
	if cfg.MaxAlerts <= 0 {
		cfg.MaxAlerts = 20
	}
	return &Ingestor{store: st, engine: engine, cfg: cfg, log: log, metrics: metrics, now: time.Now}
}

// SetCatalog 挂接服务目录。
func (i *Ingestor) SetCatalog(catalog *Catalog) { i.catalog = catalog }

// SetCases 挂接统一案件存储；employeeID 为负责该告警的员工。
func (i *Ingestor) SetCases(cases *store.CaseStore, employeeID string) {
	i.cases = cases
	i.employee = employeeID
}

// AlertmanagerPayload 是 Alertmanager webhook v4 的最小字段。
type AlertmanagerPayload struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
}

// Alert 是 Alertmanager 单条告警。
type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// Handle 处理一份 webhook；返回处理的告警数。
func (i *Ingestor) Handle(ctx context.Context, raw []byte) (int, error) {
	var payload AlertmanagerPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, fmt.Errorf("解析告警失败: %w", err)
	}
	if len(payload.Alerts) == 0 {
		return 0, nil
	}
	groupKey := strings.TrimSpace(payload.GroupKey)
	if groupKey == "" {
		groupKey = payload.Alerts[0].Fingerprint
	}
	if groupKey == "" {
		groupKey = groupKeyOf(payload.Alerts[0])
	}
	now := i.now()

	if strings.EqualFold(payload.Status, "resolved") {
		inc, ok, err := i.store.ResolveIncident(ctx, groupKey, now)
		if err != nil {
			return 0, err
		}
		if ok && inc != nil {
			_ = i.store.AddEvent(ctx, inc.ID, "resolved", "alertmanager", "告警组已恢复", now)
			i.syncCase(ctx, inc, "resolved", now)
			if i.metrics != nil {
				i.metrics.Inc("agenthub_incidents_total", map[string]string{"status": "resolved"})
			}
		}
		return len(payload.Alerts), nil
	}

	seed := seedOf(payload)
	seed.ChatID = i.cfg.ChatID
	accepted := 0
	createdIncident := false
	var incident *store.Incident
	for idx, alert := range payload.Alerts {
		if !strings.EqualFold(alert.Status, "firing") {
			continue
		}
		if idx >= i.cfg.MaxAlerts {
			break
		}
		record := recordOf(alert, seed)
		inc, created, err := i.store.UpsertIncident(ctx, groupKey, seed, record, now)
		if err != nil {
			return accepted, err
		}
		incident = inc
		accepted++
		if created {
			createdIncident = true
		}
		if i.metrics != nil {
			i.metrics.Inc("agenthub_alerts_total", map[string]string{"severity": record.Severity, "status": "firing"})
		}
	}
	if incident == nil {
		return accepted, nil
	}
	if !createdIncident {
		_ = i.store.AddEvent(ctx, incident.ID, "alert", "alertmanager", "告警组新增事件", now)
		return accepted, nil
	}
	i.syncCase(ctx, incident, "in_progress", now)
	if err := i.submitDiagnosis(ctx, incident, payload); err != nil {
		i.log.Error("submit aiops diagnosis failed", "incident", incident.ID, "error", err)
		return accepted, err
	}
	if i.metrics != nil {
		i.metrics.Inc("agenthub_incidents_total", map[string]string{"status": "firing"})
	}
	return accepted, nil
}

func (i *Ingestor) syncCase(ctx context.Context, incident *store.Incident, status string, now time.Time) {
	if i.cases == nil || incident == nil {
		return
	}
	c, created, err := i.cases.EnsureCase(ctx, i.employee, "incident", "alertmanager", incident.ID,
		incident.Title, incident.Summary, incident.Severity, incident.Service, incident.Environment, incident.ChatID, now)
	if err != nil {
		i.log.Warn("sync case failed", "incident", incident.ID, "error", err)
		return
	}
	if status == "resolved" {
		_ = i.cases.SetCaseStatus(ctx, c.ID, "resolved", now)
		_ = i.cases.AddCaseEvent(ctx, c.ID, "resolved", "alertmanager", "告警组恢复，案件关闭", now)
		return
	}
	_ = i.cases.SetCaseStatus(ctx, c.ID, "in_progress", now)
	if created {
		_ = i.cases.AddCaseEvent(ctx, c.ID, "created", "alertmanager", "由告警创建案件", now)
	} else {
		_ = i.cases.AddCaseEvent(ctx, c.ID, "updated", "alertmanager", "案件收到新告警", now)
	}
}

func (i *Ingestor) submitDiagnosis(ctx context.Context, incident *store.Incident, payload AlertmanagerPayload) error {
	prompt := i.buildPrompt(incident, payload)
	toolAllow := []string{}
	if i.cases != nil {
		if emp, _ := i.cases.GetEmployee(ctx, i.employee); emp != nil {
			toolAllow = emp.AllowedTools
		}
	}
	_, err := i.engine.Submit(ctx, channel.Inbound{
		Channel: "aiops", Tenant: "aiops",
		ExternalID: "incident:" + incident.ID + ":firing",
		SentAt:     i.now(),
		Sender:     channel.Sender{OpenID: "alertmanager", Name: "Alertmanager"},
		Chat:       channel.Chat{ID: i.cfg.ChatID, Kind: "group", Name: "运维告警"},
		Text:       prompt,
		MsgType:    "text",
		ReadOnly:   true,
		NoReply:    true,
		ToolAllow:  toolAllow,
	})
	return err
}

func (i *Ingestor) buildPrompt(incident *store.Incident, payload AlertmanagerPayload) string {
	var b strings.Builder
	b.WriteString("【运维告警诊断】\n\n")
	b.WriteString("请以 SRE 只读诊断模式处理这次告警：先判断影响面，再列证据、根因假设和建议动作；不要执行写操作或变更。\n\n")
	fmt.Fprintf(&b, "事故: %s\n标题: %s\n状态: %s\n严重级别: %s\n服务: %s\n环境: %s\n",
		incident.ID, incident.Title, incident.Status, incident.Severity, incident.Service, incident.Environment)
	fmt.Fprintf(&b, "告警组: %s\n外部地址: %s\n\n告警列表:\n", payload.GroupKey, payload.ExternalURL)
	limit := len(payload.Alerts)
	if limit > i.cfg.MaxAlerts {
		limit = i.cfg.MaxAlerts
	}
	for idx := 0; idx < limit; idx++ {
		alert := payload.Alerts[idx]
		name := alert.Labels["alertname"]
		summary := alert.Annotations["summary"]
		desc := alert.Annotations["description"]
		fmt.Fprintf(&b, "- [%s] %s: %s\n  %s\n", alert.Status, name, summary, desc)
	}
	if len(payload.CommonLabels) > 0 {
		b.WriteString("\n公共标签:\n")
		for key, value := range payload.CommonLabels {
			fmt.Fprintf(&b, "- %s=%s\n", key, value)
		}
	}
	if i.catalog != nil {
		if svc, ok := i.catalog.Lookup(incident.Service); ok {
			b.WriteString("\n服务目录:\n")
			fmt.Fprintf(&b, "- owner: %s\n- team: %s\n- namespace: %s\n- repo: %s\n- dashboard: %s\n- runbook: %s\n",
				svc.Owner, svc.Team, svc.Namespace, svc.Repo, svc.Dashboard, svc.Runbook)
		}
	}
	if runbook := i.runbookFor(incident, payload); runbook != "" {
		b.WriteString("\n相关运行手册:\n")
		b.WriteString(runbook)
		b.WriteString("\n")
	}
	b.WriteString("\n输出要求：结论、证据（引用指标/日志/变更）、根因候选（按概率）、建议动作（标注是否需要人工审批）。")
	return b.String()
}

func (i *Ingestor) runbookFor(incident *store.Incident, payload AlertmanagerPayload) string {
	if i.cfg.RunbookDir == "" {
		return ""
	}
	names := []string{}
	if alertname := payload.CommonLabels["alertname"]; alertname != "" {
		names = append(names, alertname)
	}
	if incident.Service != "" {
		names = append(names, incident.Service)
	}
	names = append(names, "default")
	for _, name := range names {
		name = filepath.Base(strings.TrimSpace(name))
		if name == "" || name == "." {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(i.cfg.RunbookDir, name+".md"))
		if err != nil {
			continue
		}
		if len(raw) > 8<<10 {
			raw = raw[:8<<10]
		}
		return string(raw)
	}
	return ""
}

func seedOf(payload AlertmanagerPayload) store.IncidentSeed {
	labels := payload.CommonLabels
	annotations := payload.CommonAnnotations
	if len(labels) == 0 && len(payload.Alerts) > 0 {
		labels = payload.Alerts[0].Labels
	}
	if len(annotations) == 0 && len(payload.Alerts) > 0 {
		annotations = payload.Alerts[0].Annotations
	}
	service := firstNonEmpty(labels["service"], labels["job"], labels["app"], labels["component"])
	environment := firstNonEmpty(labels["environment"], labels["env"], labels["cluster"])
	severity := firstNonEmpty(labels["severity"], labels["priority"])
	title := firstNonEmpty(annotations["summary"], labels["alertname"], "运维告警")
	summary := firstNonEmpty(annotations["description"], annotations["summary"], labels["alertname"])
	return store.IncidentSeed{
		Title: title, Summary: summary, Severity: severity,
		Service: service, Environment: environment,
	}
}

func recordOf(alert Alert, seed store.IncidentSeed) store.AlertRecord {
	severity := firstNonEmpty(alert.Labels["severity"], seed.Severity)
	return store.AlertRecord{
		Fingerprint: firstNonEmpty(alert.Fingerprint, groupKeyOf(alert)),
		Status:      alert.Status, AlertName: alert.Labels["alertname"], Severity: severity,
		Summary: alert.Annotations["summary"], Description: alert.Annotations["description"],
		StartsAt: alert.StartsAt, EndsAt: alert.EndsAt, Labels: alert.Labels, Annotations: alert.Annotations,
	}
}

func groupKeyOf(alert Alert) string {
	keys := []string{"alertname", "service", "job", "instance", "cluster", "namespace"}
	var parts []string
	for _, key := range keys {
		if value := alert.Labels[key]; value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	if len(parts) == 0 {
		return alert.Fingerprint
	}
	return strings.Join(parts, "|")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
