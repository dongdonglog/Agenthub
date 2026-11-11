package obs

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Metrics 是一个最小可用的 Prometheus 文本指标注册表。
//
// @remarks 只覆盖 v1 需要的计数器与仪表盘；后续接入 OpenTelemetry 时替换实现，
// 调用方只依赖 Inc / Set 两个方法。
type Metrics struct {
	mu       sync.Mutex
	start    time.Time
	counters map[string]float64
	gauges   map[string]float64
	help     map[string]string
}

// NewMetrics 创建空的指标注册表。
func NewMetrics() *Metrics {
	return &Metrics{
		start:    time.Now(),
		counters: make(map[string]float64),
		gauges:   make(map[string]float64),
		help:     make(map[string]string),
	}
}

// Inc 给计数器加一。
//
// @param name 指标名，需符合 aa_<subsystem>_<name>_total 约定
// @param labels 标签，可为 nil
func (m *Metrics) Inc(name string, labels map[string]string) {
	m.Add(name, 1, labels)
}

// Add 给计数器加指定值。
func (m *Metrics) Add(name string, delta float64, labels map[string]string) {
	key := metricKey(name, labels)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[key] += delta
	if _, ok := m.help[name]; !ok {
		m.help[name] = name
	}
}

// Set 设置仪表值。
func (m *Metrics) Set(name string, value float64, labels map[string]string) {
	key := metricKey(name, labels)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[key] = value
	if _, ok := m.help[name]; !ok {
		m.help[name] = name
	}
}

// Handler 返回 Prometheus 文本格式的 HTTP 处理器。
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.write(w)
	})
}

func (m *Metrics) write(w http.ResponseWriter) {
	m.mu.Lock()
	defer m.mu.Unlock()

	up := time.Since(m.start).Seconds()
	fmt.Fprintf(w, "# HELP agenthub_uptime_seconds 进程运行时长（秒）\n# TYPE agenthub_uptime_seconds gauge\nagenthub_uptime_seconds %.0f\n", up)

	names := make([]string, 0, len(m.help))
	for name := range m.help {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		kind := "counter"
		if strings.HasSuffix(name, "_total") {
			kind = "counter"
		} else {
			kind = "gauge"
		}
		fmt.Fprintf(w, "# TYPE %s %s\n", name, kind)
		for _, line := range m.lines(name) {
			fmt.Fprintln(w, line)
		}
	}
}

func (m *Metrics) lines(name string) []string {
	var out []string
	for key, value := range m.counters {
		metric, labels := splitKey(key)
		if metric != name {
			continue
		}
		out = append(out, formatLine(metric, labels, value))
	}
	for key, value := range m.gauges {
		metric, labels := splitKey(key)
		if metric != name {
			continue
		}
		out = append(out, formatLine(metric, labels, value))
	}
	sort.Strings(out)
	return out
}

func formatLine(name, labels string, value float64) string {
	if labels == "" {
		return name + " " + strconv.FormatFloat(value, 'f', -1, 64)
	}
	return name + "{" + labels + "} " + strconv.FormatFloat(value, 'f', -1, 64)
}

func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name + "\x00"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	b.WriteString("\x00")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(strconv.Quote(labels[k]))
	}
	return b.String()
}

func splitKey(key string) (string, string) {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}
