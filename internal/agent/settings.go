package agent

import "sync"

// ModelSettings 是运行期可热更新的模型配置。
type ModelSettings struct {
	BaseURL       string
	APIKey        string
	Model         string
	FallbackModel string
	VisionModel   string
}

// SettingsHolder 以原子方式保存当前模型配置。
type SettingsHolder struct {
	mu sync.RWMutex
	v  ModelSettings
}

// NewSettingsHolder 用初始值构造。
func NewSettingsHolder(v ModelSettings) *SettingsHolder {
	return &SettingsHolder{v: v}
}

// Get 返回当前配置。
func (h *SettingsHolder) Get() ModelSettings {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.v
}

// Set 更新配置。
func (h *SettingsHolder) Set(v ModelSettings) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.v = v
}
