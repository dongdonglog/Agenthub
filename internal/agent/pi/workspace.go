package pi

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
		if err := os.Link(path, target); err == nil {
			return nil
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
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
