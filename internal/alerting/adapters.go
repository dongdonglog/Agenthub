package alerting

import (
	"encoding/json"
	"fmt"
	"time"
)

// Generic 解析 AgentHub 通用告警 schema。
type Generic struct{}

// Name 实现 Adapter。
func (Generic) Name() string { return "generic" }

// Decode 实现 Adapter。
func (Generic) Decode(raw []byte) (*Event, error) {
	var in struct {
		ID          string            `json:"id"`
		Status      string            `json:"status"`
		Title       string            `json:"title"`
		Summary     string            `json:"summary"`
		Description string            `json:"description"`
		Severity    string            `json:"severity"`
		Service     string            `json:"service"`
		Environment string            `json:"environment"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		StartsAt    time.Time         `json:"startsAt"`
		EndsAt      time.Time         `json:"endsAt"`
		URL         string            `json:"url"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("解析通用告警失败: %w", err)
	}
	labels := clone(in.Labels)
	annotations := clone(in.Annotations)
	if in.Severity != "" {
		labels["severity"] = in.Severity
	}
	if in.Service != "" {
		labels["service"] = in.Service
	}
	if in.Environment != "" {
		labels["environment"] = in.Environment
	}
	if in.Title != "" {
		annotations["summary"] = in.Title
	}
	if in.Description != "" {
		annotations["description"] = in.Description
	}
	if in.Summary != "" {
		annotations["description"] = in.Summary
	}
	alert := Alert{Status: in.Status, Labels: labels, Annotations: annotations, StartsAt: in.StartsAt, EndsAt: in.EndsAt, Fingerprint: in.ID, GeneratorURL: in.URL}
	if alert.Status == "" {
		alert.Status = string(Firing)
	}
	return &Event{GroupKey: in.ID, Status: alert.Status, Title: in.Title, CommonLabels: labels, CommonAnnotations: annotations, ExternalURL: in.URL, Alerts: []Alert{alert}}, nil
}

// PagerDuty 解析 PagerDuty v3 webhook。
type PagerDuty struct{}

// Name 实现 Adapter。
func (PagerDuty) Name() string { return "pagerduty" }

// Decode 实现 Adapter。
func (PagerDuty) Decode(raw []byte) (*Event, error) {
	var in struct {
		Event struct {
			EventType string `json:"event_type"`
			Data      struct {
				ID        string `json:"id"`
				Title     string `json:"title"`
				Urgency   string `json:"urgency"`
				HTMLURL   string `json:"html_url"`
				CreatedAt string `json:"created_at"`
				Service   struct {
					Summary string `json:"summary"`
				} `json:"service"`
			} `json:"data"`
		} `json:"event"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("解析 PagerDuty 告警失败: %w", err)
	}
	if in.Event.Data.ID == "" {
		return nil, fmt.Errorf("PagerDuty 事件缺少 data.id")
	}
	status := Firing
	if in.Event.EventType == "incident.resolved" {
		status = Resolved
	}
	labels := map[string]string{"alertname": in.Event.Data.Title, "service": in.Event.Data.Service.Summary, "severity": in.Event.Data.Urgency}
	annotations := map[string]string{"summary": in.Event.Data.Title}
	alert := Alert{Status: string(status), Labels: labels, Annotations: annotations, Fingerprint: in.Event.Data.ID, GeneratorURL: in.Event.Data.HTMLURL}
	return &Event{GroupKey: in.Event.Data.ID, Status: string(status), Title: in.Event.Data.Title, CommonLabels: labels, CommonAnnotations: annotations, ExternalURL: in.Event.Data.HTMLURL, Alerts: []Alert{alert}}, nil
}

func clone(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+4)
	for key, value := range in {
		out[key] = value
	}
	return out
}
