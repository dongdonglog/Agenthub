// Package knowledge 把本地知识库文档导入存储。
package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/store"
)

// IngestDir 递归导入目录下的 Markdown 文档。
func IngestDir(ctx context.Context, st *store.KnowledgeStore, dir string) (int, error) {
	if st == nil || dir == "" {
		return 0, nil
	}
	count := 0
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		title := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if first := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0]); strings.HasPrefix(first, "#") {
			title = strings.TrimSpace(strings.TrimPrefix(first, "#"))
		}
		if err := st.Upsert(ctx, store.KnowledgeDoc{
			Title: title, Content: string(raw), Source: rel, UpdatedAt: time.Now(),
		}); err != nil {
			return err
		}
		count++
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return count, err
}
