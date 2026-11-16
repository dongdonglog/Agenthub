package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// applyProfileSelection 把 Profile 的技能/MCP 选择落到会话工作区副本。
//
// @remarks 空列表表示不限制；技能从部署 agent dir 重新复制，MCP 只保留白名单，
// 避免上一轮或配置同步遗留的资源继续暴露给本次运行。
func applyProfileSelection(agentDir, ws string, skills, mcps []string) error {
	agentCopy := filepath.Join(ws, ".agenthub-agent")
	if err := os.MkdirAll(agentCopy, 0o755); err != nil {
		return err
	}
	if err := applySkillSelection(filepath.Join(agentDir, "skills"), filepath.Join(agentCopy, "skills"), skills); err != nil {
		return err
	}
	if err := applyMCPSelection(filepath.Join(agentDir, "mcp.json"), filepath.Join(agentCopy, "mcp.json"), mcps); err != nil {
		return err
	}
	return nil
}

func applySkillSelection(src, dst string, allowed []string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	allow := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		name = strings.TrimSpace(name)
		allow[name] = true
		allow[strings.TrimSuffix(name, ".md")] = true
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".md")
		if len(allow) > 0 && !allow[entry.Name()] && !allow[name] {
			continue
		}
		source := filepath.Join(src, entry.Name())
		target := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyTree(source, target); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := copyFile(source, target); err != nil {
			return err
		}
	}
	return nil
}

func applyMCPSelection(src, dst string, allowed []string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	if len(allowed) > 0 {
		keep := make(map[string]bool, len(allowed))
		for _, name := range allowed {
			keep[strings.TrimSpace(name)] = true
		}
		for name := range payload.MCPServers {
			if !keep[name] {
				delete(payload.MCPServers, name)
			}
		}
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, out, 0o644)
}

// linkSkillExecutables 把 Skill 根目录的可执行文件链接到会话 PATH。
func linkSkillExecutables(ws string) error {
	binDir := filepath.Join(ws, ".agenthub-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return err
	}
	const prefix = "../.agenthub-agent/skills/"
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(filepath.Join(binDir, entry.Name()))
		if err == nil && strings.HasPrefix(target, prefix) {
			_ = os.Remove(filepath.Join(binDir, entry.Name()))
		}
	}

	skillsRoot := filepath.Join(ws, ".agenthub-agent", "skills")
	skills, err := os.ReadDir(skillsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, skill := range skills {
		if !skill.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(skillsRoot, skill.Name()))
		if err != nil {
			continue
		}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			info, err := file.Info()
			if err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			link := filepath.Join(binDir, file.Name())
			if _, err := os.Lstat(link); err == nil {
				continue
			}
			target := prefix + skill.Name() + "/" + file.Name()
			_ = os.Symlink(target, link)
		}
	}
	return nil
}
