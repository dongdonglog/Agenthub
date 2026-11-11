package agent

import (
	"io"
	"os"
	"path/filepath"
)

// SeedSkills 在目标目录为空时，把随仓库发布的技能复制过去。
//
// @remarks 只在目标目录不存在或为空时执行，避免覆盖管理台里改过的内容。
// 返回复制的文件数；源目录缺失时返回 0。
func SeedSkills(srcDir, dstDir string) (int, error) {
	if srcDir == "" || dstDir == "" {
		return 0, nil
	}
	if entries, err := os.ReadDir(dstDir); err == nil && len(entries) > 0 {
		return 0, nil
	}
	if _, err := os.Stat(srcDir); err != nil {
		return 0, nil
	}

	copied := 0
	err := filepath.WalkDir(srcDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		dst, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			return err
		}
		src.Close()
		if err := dst.Close(); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}
