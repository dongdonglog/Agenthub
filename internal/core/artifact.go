package core

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

// SetArtifactDir 设置产物持久化目录；为空时直接使用工作区路径。
func (e *Engine) SetArtifactDir(dir string) {
	e.mu.Lock()
	e.artifactDir = dir
	e.mu.Unlock()
}

// SetArtifactLimits 设置单文件、数量与总量上限；非正值保持默认。
func (e *Engine) SetArtifactLimits(maxBytes int64, maxCount int, maxTotal int64) {
	e.mu.Lock()
	if maxBytes > 0 {
		e.artifactMaxBytes = maxBytes
	}
	if maxCount > 0 {
		e.artifactMaxCount = maxCount
	}
	if maxTotal > 0 {
		e.artifactMaxTotal = maxTotal
	}
	e.mu.Unlock()
}

// stageArtifacts 把执行器产物复制到持久目录并登记元数据。
//
// @returns 已受理的产物与给用户的提示；提示为空表示没有超限或失败。
func (e *Engine) stageArtifacts(run *Run, srcs []channel.Artifact) ([]*Artifact, string) {
	e.mu.Lock()
	dir := e.artifactDir
	maxBytes := e.artifactMaxBytes
	maxCount := e.artifactMaxCount
	maxTotal := e.artifactMaxTotal
	e.mu.Unlock()

	sorted := append([]channel.Artifact(nil), srcs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var staged []*Artifact
	cleanup := make(map[string]string)
	var oversized, overCount, failed int
	var total int64
	for _, src := range sorted {
		if len(staged) >= maxCount {
			overCount++
			continue
		}
		info, err := os.Stat(src.Path)
		if err != nil || !info.Mode().IsRegular() {
			failed++
			continue
		}
		if info.Size() > maxBytes || total+info.Size() > maxTotal {
			oversized++
			continue
		}
		name := safeArtifactName(src.Name)
		if name == "" {
			name = "artifact"
		}
		id := run.ID + ":" + strconv.Itoa(len(staged)+1)
		path := src.Path
		copied := false
		if dir != "" {
			path, err = copyArtifact(dir, run.ID, id, src.Path)
			if err != nil {
				failed++
				continue
			}
			copied = true
		}
		sum, _ := fileHash(path)
		artifact := &Artifact{
			ID:             id,
			RunID:          run.ID,
			ConversationID: run.ConversationID,
			Name:           name,
			Kind:           artifactKind(name, src.Kind),
			Size:           info.Size(),
			SHA256:         sum,
			Path:           path,
			CreatedAt:      e.now(),
		}
		staged = append(staged, artifact)
		total += info.Size()
		if copied {
			cleanup[artifact.ID] = src.Path
		}
	}

	e.mu.Lock()
	for _, artifact := range staged {
		e.artifacts[artifact.ID] = artifact
	}
	journal := e.journal
	e.mu.Unlock()

	for _, artifact := range staged {
		persisted := true
		if journal != nil {
			if err := journal.SaveArtifact(context.Background(), artifact); err != nil {
				persisted = false
				e.log.Warn("persist artifact failed", "error", err, "artifact", artifact.ID)
			}
		}
		if persisted {
			// 持久化成功后，本次工作区 outbox 不再需要保留原件。
			if src, ok := cleanup[artifact.ID]; ok {
				_ = os.Remove(src)
			}
		}
	}

	var notices []string
	if overCount > 0 {
		notices = append(notices, fmt.Sprintf("有 %d 个产物超出单次数量上限（%d 个），未交付。", overCount, maxCount))
	}
	if oversized > 0 {
		notices = append(notices, fmt.Sprintf("有 %d 个产物超出大小上限（单文件 %s / 单次 %s），未交付。",
			oversized, formatBytes(maxBytes), formatBytes(maxTotal)))
	}
	if failed > 0 {
		notices = append(notices, fmt.Sprintf("有 %d 个产物读取失败，未交付。", failed))
	}
	return staged, strings.Join(notices, "\n")
}

func (e *Engine) canSendArtifacts() bool {
	if e.ch == nil {
		return false
	}
	_, ok := e.ch.(channel.ArtifactSender)
	return ok
}

// buildArtifactDeliveries 为本次运行创建产物投递；图片单独发，多个文档打 ZIP。
func (e *Engine) buildArtifactDeliveries(run *Run, msg *Message, artifacts []*Artifact) []*Delivery {
	if len(artifacts) == 0 {
		return nil
	}
	var images, docs []*Artifact
	for _, artifact := range artifacts {
		if artifact.Kind == "image" {
			images = append(images, artifact)
		} else {
			docs = append(docs, artifact)
		}
	}
	if len(docs) > 1 {
		if zipped, err := e.zipArtifacts(run, docs); err == nil {
			docs = []*Artifact{zipped}
		} else {
			e.log.Warn("zip artifacts failed; send individually", "error", err, "run", run.ID)
		}
	}
	items := append(images, docs...)
	deliveries := make([]*Delivery, 0, len(items))
	for i, artifact := range items {
		deliveries = append(deliveries, &Delivery{
			TraceID:        run.TraceID,
			CreatedAt:      e.now(),
			RunID:          run.ID,
			IdempotencyKey: run.ID + ":artifact:" + strconv.Itoa(i+1),
			ChatID:         msg.ChatID,
			ReplyTo:        replyTarget(msg),
			Kind:           artifact.Kind,
			Name:           artifact.Name,
			ArtifactID:     artifact.ID,
			ArtifactPath:   artifact.Path,
			State:          "pending",
		})
	}
	return deliveries
}

func (e *Engine) zipArtifacts(run *Run, docs []*Artifact) (*Artifact, error) {
	e.mu.Lock()
	dir := e.artifactDir
	e.mu.Unlock()
	if dir == "" {
		dir = os.TempDir()
	}
	targetDir := filepath.Join(dir, safeArtifactName(run.ID))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	name := "deliverables-" + safeArtifactName(run.ID) + ".zip"
	target := filepath.Join(targetDir, name)
	file, err := os.Create(target)
	if err != nil {
		return nil, err
	}
	writer := zip.NewWriter(file)
	for _, doc := range docs {
		src, err := os.Open(doc.Path)
		if err != nil {
			continue
		}
		header := &zip.FileHeader{Name: doc.Name, Method: zip.Deflate}
		header.SetMode(0o644)
		// Go 默认写 UTF-8 标志位；显式声明避免解压端按本地编码误判。
		header.NonUTF8 = false
		entry, err := writer.CreateHeader(header)
		if err == nil {
			_, _ = io.Copy(entry, src)
		}
		_ = src.Close()
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	sum, _ := fileHash(target)
	artifact := &Artifact{
		ID:             run.ID + ":zip",
		RunID:          run.ID,
		ConversationID: run.ConversationID,
		Name:           name,
		Kind:           "zip",
		Size:           info.Size(),
		SHA256:         sum,
		Path:           target,
		CreatedAt:      time.Now(),
	}
	e.mu.Lock()
	e.artifacts[artifact.ID] = artifact
	e.mu.Unlock()
	if e.journal != nil {
		if err := e.journal.SaveArtifact(context.Background(), artifact); err != nil {
			e.log.Warn("persist zip artifact failed", "error", err, "artifact", artifact.ID)
		}
	}
	return artifact, nil
}

func copyArtifact(root, runID, id, src string) (string, error) {
	dir := filepath.Join(root, safeArtifactName(runID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := safeArtifactName(filepath.Base(src))
	if base == "" {
		base = "artifact"
	}
	dst := filepath.Join(dir, safeArtifactName(id)+"-"+base)
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".artifact-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	return dst, nil
}

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func artifactKind(name, declared string) string {
	if declared == "image" {
		return "image"
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return "image"
	default:
		return "file"
	}
}

func safeArtifactName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == 0 {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

func formatBytes(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.0fMB", float64(n)/(1<<20))
	}
	if n >= 1<<10 {
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

// Artifacts 返回一次运行的产物；优先内存，重启后回退数据库。
func (e *Engine) Artifacts(ctx context.Context, runID string) []*Artifact {
	e.mu.Lock()
	var out []*Artifact
	for _, artifact := range e.artifacts {
		if artifact.RunID == runID {
			out = append(out, artifact)
		}
	}
	journal := e.journal
	e.mu.Unlock()
	if len(out) == 0 && journal != nil {
		if items, err := journal.LoadArtifacts(ctx, runID); err == nil {
			out = items
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Artifact 按 ID 查找产物，供管理台下载。
func (e *Engine) Artifact(ctx context.Context, id string) *Artifact {
	e.mu.Lock()
	artifact := e.artifacts[id]
	journal := e.journal
	e.mu.Unlock()
	if artifact != nil {
		return artifact
	}
	if journal != nil {
		if item, err := journal.LoadArtifact(ctx, id); err == nil {
			return item
		}
	}
	return nil
}
