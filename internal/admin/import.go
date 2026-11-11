package admin

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	// maxImportBytes 限制单个技能包解压后的总大小。
	maxImportBytes = 20 << 20
	// maxImportFiles 限制技能包文件数，避免异常压缩包。
	maxImportFiles = 500
)

// ImportZip 从 ZIP 导入一个或多个技能目录（每个目录需含 SKILL.md）。
//
// @remarks 只接受相对路径，拒绝 .. 与绝对路径；同名技能已存在时需 overwrite 才覆盖。
func (s *Store) ImportZip(data []byte, overwrite bool) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("不是有效的 ZIP 文件")
	}
	if len(zr.File) > maxImportFiles {
		return nil, fmt.Errorf("文件数超过上限 %d", maxImportFiles)
	}

	var entries []zipEntry
	roots := map[string]bool{}
	var total int64

	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "./")
		if f.FileInfo().IsDir() {
			continue
		}
		clean := path.Clean(name)
		if clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || path.IsAbs(clean) {
			return nil, errors.New("压缩包包含非法路径")
		}
		if f.UncompressedSize64 > maxImportBytes {
			return nil, errors.New("压缩包内文件过大")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(rc, maxImportBytes+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		total += int64(len(raw))
		if total > maxImportBytes {
			return nil, errors.New("技能包解压后超过 20MB")
		}
		entries = append(entries, zipEntry{rel: clean, data: raw})
		if path.Base(clean) == "SKILL.md" {
			roots[path.Dir(clean)] = true
		}
	}
	if len(roots) == 0 {
		return nil, errors.New("压缩包内未找到 SKILL.md")
	}

	imported := make([]string, 0, len(roots))
	for root := range roots {
		name, err := skillNameFromRoot(root, entries)
		if err != nil {
			return nil, err
		}
		if !validName.MatchString(name) {
			return nil, fmt.Errorf("技能名非法：%s", name)
		}
		if _, _, err := s.findSkill(name); err == nil && !overwrite {
			return nil, fmt.Errorf("技能 %s 已存在，需确认覆盖", name)
		}
		written := 0
		for _, e := range entries {
			if root != "." && e.rel != root && !strings.HasPrefix(e.rel, root+"/") {
				continue
			}
			rel := e.rel
			if root != "." {
				rel = strings.TrimPrefix(strings.TrimPrefix(e.rel, root), "/")
			}
			if rel == "" {
				continue
			}
			dst := filepath.Join(s.skillsDir, name, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(dst, e.data, 0o644); err != nil {
				return nil, err
			}
			written++
		}
		if written == 0 {
			return nil, fmt.Errorf("技能 %s 没有可导入的文件", name)
		}
		imported = append(imported, name)
	}
	return imported, nil
}

// skillNameFromRoot 解析技能名：优先目录名，根目录时读 frontmatter。
type zipEntry struct {
	rel  string
	data []byte
}

func skillNameFromRoot(root string, entries []zipEntry) (string, error) {
	if root != "." {
		return path.Base(root), nil
	}
	for _, e := range entries {
		if e.rel == "SKILL.md" {
			for _, line := range strings.Split(string(e.data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "name:") {
					return strings.TrimSpace(strings.TrimPrefix(line, "name:")), nil
				}
			}
		}
	}
	return "", errors.New("根目录技能缺少 name")
}
