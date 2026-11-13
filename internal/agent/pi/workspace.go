package pi

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/moying/agenthub/internal/agent"
)

// ensureWorkspace 创建工作区并复制只读部署配置。
func ensureWorkspace(ws, agentDir, agentsMD string) error {
	agentCopy := filepath.Join(ws, ".agenthub-agent")
	if _, err := os.Stat(agentCopy); err == nil {
		return ensureAgentsFile(ws, agentsMD)
	}
	if _, err := os.Stat(agentDir); err != nil {
		return errors.New("部署 agent dir 不存在：" + agentDir)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return err
	}
	if err := copyTree(agentDir, agentCopy); err != nil {
		return err
	}
	for _, dir := range []string{"outbox", "attachments"} {
		if err := os.MkdirAll(filepath.Join(ws, dir), 0o755); err != nil {
			return err
		}
	}
	return ensureAgentsFile(ws, agentsMD)
}

func ensureAgentsFile(ws, content string) error {
	path := filepath.Join(ws, "AGENTS.md")
	if content == "" {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// copyTree 复制目录；优先硬链接，失败退化为普通复制。
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// buildAgentsMD 生成会话级 AGENTS.md。
func buildAgentsMD(conv string, generation int, attachments []string) string {
	var b strings.Builder
	b.WriteString("# 会话上下文\n\n")
	b.WriteString("本文件由 AgentHub 生成，描述本次会话的输入与约束。\n\n")
	b.WriteString("## 输出约定\n\n")
	b.WriteString("- 直接给出结论，不要输出内部运行编号或工具细节；\n")
	b.WriteString("- 需要多条回复时用单独一行 `---SPLIT---` 分隔；\n")
	b.WriteString("- 附件位于 `attachments/`，只读；产物写入 `outbox/`。\n")
	if len(attachments) > 0 {
		b.WriteString("\n## 附件\n\n")
		for _, name := range attachments {
			b.WriteString("- attachments/" + name + "\n")
		}
	}
	return b.String()
}

// linkAttachments 把入站附件链接到工作区只读目录，返回文件名列表。
func linkAttachments(ws string, messages []agent.Message) []string {
	dir := filepath.Join(ws, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, m := range messages {
		for _, a := range m.Attachments {
			if a.Path == "" {
				continue
			}
			name := filepath.Base(a.Name)
			if name == "" || name == "." || name == string(filepath.Separator) {
				name = filepath.Base(a.Path)
			}
			name = sanitizeName(name)
			if name == "" {
				continue
			}
			base := name
			for i := 2; seen[name]; i++ {
				ext := filepath.Ext(base)
				name = strings.TrimSuffix(base, ext) + fmt.Sprintf("-%d", i) + ext
			}
			seen[name] = true
			dest := filepath.Join(dir, name)
			if _, err := os.Stat(dest); err == nil {
				names = append(names, name)
				continue
			}
			if err := os.Link(a.Path, dest); err != nil {
				if err := copyFile(a.Path, dest); err != nil {
					continue
				}
			}
			names = append(names, name)
		}
	}
	return names
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}
