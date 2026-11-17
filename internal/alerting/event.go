// Package alerting 把不同告警平台的 webhook 归一化为统一事件。
package alerting

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Status 是告警状态。
type Status string

const (
	// Firing 表示告警触发。
	Firing Status = "firing"
	// Resolved 表示告警恢复。
	Resolved Status = "resolved"
)

// Alert 是归一化后的单条告警。
type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// Event 是一次 webhook 归一化后的事件组。
type Event struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	Title             string            `json:"title"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []Alert           `json:"alerts"`
	Source            string            `json:"source"`
}

// Adapter 解析某个平台的原始 webhook。
type Adapter interface {
	Name() string
	Decode(raw []byte) (*Event, error)
}

// Registry 按来源选择适配器。
type Registry struct{ adapters map[string]Adapter }

// NewRegistry 构造适配器注册表。
func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{adapters: map[string]Adapter{}}
	for _, adapter := range adapters {
		r.adapters[adapter.Name()] = adapter
	}
	return r
}

// Decode 解析指定来源；未知来源使用通用适配器。
func (r *Registry) Decode(source string, raw []byte) (*Event, error) {
	adapter, ok := r.adapters[source]
	if !ok {
		return nil, fmt.Errorf("未知告警来源: %s", source)
	}
	event, err := adapter.Decode(raw)
	if err != nil {
		return nil, err
	}
	Normalize(event, source)
	return event, nil
}

// Sources 返回已注册来源。
func (r *Registry) Sources() []string {
	out := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Normalize 补全来源、状态、分组键和标题。
func Normalize(event *Event, source string) {
	if event == nil {
		return
	}
	event.Source = source
	if strings.TrimSpace(event.GroupKey) == "" {
		if len(event.Alerts) > 0 {
			event.GroupKey = GroupKey(event.Alerts[0])
		}
	}
	if strings.TrimSpace(event.Status) == "" {
		event.Status = string(StatusOf(event.Alerts))
	}
	if strings.TrimSpace(event.Title) == "" {
		event.Title = firstNonEmpty(event.CommonAnnotations["summary"], event.CommonLabels["alertname"], "告警")
	}
}

// StatusOf 根据告警列表推导组状态。
func StatusOf(alerts []Alert) Status {
	if len(alerts) == 0 {
		return Firing
	}
	for _, alert := range alerts {
		if !strings.EqualFold(alert.Status, string(Resolved)) {
			return Firing
		}
	}
	return Resolved
}

// GroupKey 从常见标签生成稳定分组键。
func GroupKey(alert Alert) string {
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

// Alertmanager 解析 Prometheus Alertmanager/Grafana 兼容 webhook。
type Alertmanager struct{}

// Name 实现 Adapter。
func (Alertmanager) Name() string { return "alertmanager" }

// Decode 实现 Adapter。
func (Alertmanager) Decode(raw []byte) (*Event, error) {
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, fmt.Errorf("解析 Alertmanager 告警失败: %w", err)
	}
	return &event, nil
}

// Grafana 解析 Grafana Alerting webhook；字段与 Alertmanager 兼容，额外保留 title。
type Grafana struct{}

// Name 实现 Adapter。
func (Grafana) Name() string { return "grafana" }

// Decode 实现 Adapter。
func (Grafana) Decode(raw []byte) (*Event, error) {
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, fmt.Errorf("解析 Grafana 告警失败: %w", err)
	}
	return &event, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
