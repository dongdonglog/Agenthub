package agent

import (
	"context"
	"encoding/json"
)

// Tool 是暴露给模型的工具。
type Tool interface {
	// Name 是工具名，必须匹配 ^[a-zA-Z0-9_-]+$。
	Name() string
	// Description 告诉模型何时使用。
	Description() string
	// Schema 是 JSON Schema 形式的参数定义。
	Schema() map[string]any
	// Call 执行工具并返回文本结果。
	Call(ctx context.Context, args json.RawMessage) (string, error)
}
