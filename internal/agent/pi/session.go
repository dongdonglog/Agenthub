// Package pi 实现 AA 同款的 pi 运行时接入。
package pi

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const sessionVersion = 3

type sessionEntry struct {
	Type      string  `json:"type"`
	Version   int     `json:"version,omitempty"`
	ID        string  `json:"id,omitempty"`
	ParentID  *string `json:"parentId,omitempty"`
	Timestamp string  `json:"timestamp,omitempty"`
	Cwd       string  `json:"cwd,omitempty"`
	Message   any     `json:"message,omitempty"`
}

type userMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

type customMessage struct {
	Role       string `json:"role"`
	CustomType string `json:"customType"`
	Content    string `json:"content"`
	Display    bool   `json:"display"`
	Timestamp  int64  `json:"timestamp"`
}

func entryID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}

// ensureSessionFile 创建或校验会话文件，返回最后一条条目 ID。
func ensureSessionFile(path, cwd string) (*string, error) {
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		header := sessionEntry{
			Type: "session", Version: sessionVersion, ID: entryID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Cwd: cwd,
		}
		raw, _ := json.Marshal(header)
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return lastEntryID(path)
}

// validateSession 校验会话文件版本、工作区与上下文。
func validateSession(path, workDir string, allowEmpty bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("pi 会话文件缺失: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	first := true
	messages := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e sessionEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("pi 会话文件损坏: %w", err)
		}
		if first {
			if e.Type != "session" || e.Version != sessionVersion {
				return errors.New("pi 会话文件头或版本不匹配")
			}
			if e.Cwd != workDir {
				return errors.New("pi 会话文件属于其他工作区，拒绝恢复")
			}
			first = false
			continue
		}
		if e.Type == "message" {
			messages++
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !allowEmpty && messages == 0 {
		return errors.New("pi 已就绪会话没有上下文，拒绝按空会话恢复")
	}
	return nil
}

func lastEntryID(path string) (*string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := splitLines(raw)
	for i := len(lines) - 1; i >= 0; i-- {
		var e sessionEntry
		if err := json.Unmarshal(lines[i], &e); err != nil {
			continue
		}
		if e.Type == "session" || e.ID == "" {
			continue
		}
		id := e.ID
		return &id, nil
	}
	return nil, nil
}

func splitLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			if i > start {
				out = append(out, raw[start:i])
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, raw[start:])
	}
	return out
}

// appendUserMessages 追加若干条 user 消息，返回最后一条 ID。
func appendUserMessages(path string, texts []string) (*string, error) {
	parent, err := lastEntryID(path)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var last *string
	for i, text := range texts {
		id := entryID()
		msg := userMessage{Role: "user", Content: text, Timestamp: now.UnixMilli() + int64(i)}
		if err := appendEntry(path, sessionEntry{
			Type: "message", ID: id, ParentID: parent,
			Timestamp: now.Add(time.Duration(i) * time.Millisecond).UTC().Format(time.RFC3339Nano),
			Message:   msg,
		}); err != nil {
			return nil, err
		}
		parent = &id
		last = &id
	}
	return last, nil
}

// appendBatchMarker 追加批次标记，说明前面 N 条属于同一批未作答输入。
func appendBatchMarker(path string, count int) error {
	parent, err := lastEntryID(path)
	if err != nil {
		return err
	}
	msg := customMessage{
		Role:       "custom",
		CustomType: "agenthub.batch",
		Content:    fmt.Sprintf("本批共 %d 条消息，均为同一会话中连续收到的输入；前面的消息尚未作答，请把它们作为整体理解后一次作答。", count),
		Display:    false,
		Timestamp:  time.Now().UnixMilli(),
	}
	return appendEntry(path, sessionEntry{
		Type: "message", ID: entryID(), ParentID: parent,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Message: msg,
	})
}

// appendAbortMarker 追加中止标记。
func appendAbortMarker(path, runID, reason string) error {
	parent, err := lastEntryID(path)
	if err != nil {
		return err
	}
	msg := customMessage{
		Role:       "custom",
		CustomType: "agenthub.abort",
		Content:    fmt.Sprintf("本批输入已中止，不构成后续指令。内部运行标识：%s；中止原因：%s。", runID, reason),
		Display:    false,
		Timestamp:  time.Now().UnixMilli(),
	}
	return appendEntry(path, sessionEntry{
		Type: "message", ID: entryID(), ParentID: parent,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Message: msg,
	})
}

func appendEntry(path string, e sessionEntry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func countMessages(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range splitLines(raw) {
		var e sessionEntry
		if json.Unmarshal(line, &e) == nil && e.Type == "message" {
			count++
		}
	}
	return count, nil
}
