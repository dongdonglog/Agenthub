// Package obs 提供结构化日志与指标。
package obs

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger 按配置构造结构化日志器。
//
// @param level debug / info / warn / error
// @param format json 或 text
// @param w 输出目标；为 nil 时使用标准输出
func NewLogger(level, format string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}
	return slog.New(handler).With("service", "agenthub")
}
