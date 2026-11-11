package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ReadFileTool 让模型读取附件目录内的文件。
//
// @remarks 只允许访问 Root 之下的路径，避免越权读取宿主文件。
type ReadFileTool struct {
	// Root 是允许访问的根目录（单根兼容用法）。
	Root string
	// Roots 是允许访问的多个根目录，任一匹配即可。
	Roots []string
	// MaxBytes 是单次读取上限，默认 256KB。
	MaxBytes int
}

// Name 实现 {@link Tool}。
func (t *ReadFileTool) Name() string { return "read_file" }

// Description 实现 {@link Tool}。
func (t *ReadFileTool) Description() string {
	return "读取已上传附件的内容。参数 path 可以是用附件消息给出的本地路径，也可以是相对附件目录的路径。"
}

// Schema 实现 {@link Tool}。
func (t *ReadFileTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "文件路径"},
		},
		"required": []string{"path"},
	}
}

// Call 实现 {@link Tool}。
func (t *ReadFileTool) Call(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", errors.New("path 不能为空")
	}
	path, err := t.resolve(args.Path)
	if err != nil {
		return "", err
	}
	limit := t.MaxBytes
	if limit <= 0 {
		limit = 256 * 1024
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	buf := make([]byte, limit)
	n, err := file.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	out := string(buf[:n])
	if n == limit {
		out += "\n…（已截断，文件超过读取上限）"
	}
	return out, nil
}

func (t *ReadFileTool) resolve(input string) (string, error) {
	roots := t.Roots
	if len(roots) == 0 && t.Root != "" {
		roots = []string{t.Root}
	}
	if len(roots) == 0 {
		return "", errors.New("未配置可读目录")
	}
	for _, root := range roots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		path := input
		if !filepath.IsAbs(path) {
			path = filepath.Join(absRoot, path)
		}
		path, err = filepath.Abs(path)
		if err != nil {
			continue
		}
		if path == absRoot || strings.HasPrefix(path, absRoot+string(os.PathSeparator)) {
			return path, nil
		}
	}
	return "", errors.New("只能读取附件或技能目录内的文件")
}
