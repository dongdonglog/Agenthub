package core_test

import (
	"archive/zip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
)

type artifactAgent struct {
	files []string
}

func (a artifactAgent) Run(_ context.Context, _ agent.Input) (agent.Output, error) {
	out := agent.Output{Text: "已完成"}
	for _, path := range a.files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		kind := "file"
		if strings.HasSuffix(path, ".png") {
			kind = "image"
		}
		out.Artifacts = append(out.Artifacts, channel.Artifact{
			Name: filepath.Base(path), Path: path, Kind: kind, Size: info.Size(),
		})
	}
	return out, nil
}

type artifactChannel struct {
	mu        sync.Mutex
	texts     []channel.Outbound
	artifacts []channel.OutboundArtifact
}

func (c *artifactChannel) SendText(_ context.Context, out channel.Outbound) (channel.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, out)
	return channel.Receipt{ExternalID: "om_text"}, nil
}

func (c *artifactChannel) SendArtifact(_ context.Context, out channel.OutboundArtifact) (channel.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.artifacts = append(c.artifacts, out)
	return channel.Receipt{ExternalID: "om_file"}, nil
}

func newArtifactEngine(t *testing.T, ch channel.Channel, a agent.Agent) *core.Engine {
	t.Helper()
	engine := core.New(a, ch, slog.New(slog.NewTextHandler(io.Discard, nil)))
	engine.SetArtifactDir(t.TempDir())
	return engine
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestArtifactsAreDeliveredAfterText(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "report.md", "hello artifact")
	ch := &artifactChannel{}
	engine := newArtifactEngine(t, ch, artifactAgent{files: []string{path}})

	if _, err := engine.Submit(context.Background(), inbound("om_art_1", "生成报告")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ch.texts) != 1 || len(ch.artifacts) != 1 {
		t.Fatalf("texts=%d artifacts=%d", len(ch.texts), len(ch.artifacts))
	}
	if ch.artifacts[0].Artifact.Name != "report.md" {
		t.Fatalf("artifact = %+v", ch.artifacts[0])
	}
	if _, err := os.Stat(ch.artifacts[0].Artifact.Path); err != nil {
		t.Fatalf("copied artifact missing: %v", err)
	}
	runs := engine.Runs()
	arts := engine.Artifacts(context.Background(), runs[0].ID)
	if len(arts) != 1 || arts[0].SHA256 == "" {
		t.Fatalf("staged artifacts = %+v", arts)
	}
}

func TestArtifactLimitProducesNotice(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "big.bin", strings.Repeat("x", 100))
	ch := &artifactChannel{}
	engine := newArtifactEngine(t, ch, artifactAgent{files: []string{path}})
	engine.SetArtifactLimits(10, 10, 10)

	if _, err := engine.Submit(context.Background(), inbound("om_art_2", "生成大文件")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ch.artifacts) != 0 {
		t.Fatalf("超限产物不应发送: %+v", ch.artifacts)
	}
	if len(ch.texts) != 1 || !strings.Contains(ch.texts[0].Text, "超出大小上限") {
		t.Fatalf("缺少超限提示: %+v", ch.texts)
	}
}

func TestUnsupportedChannelGetsNoticeButKeepsArtifact(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "report.md", "hello")
	ch := &captureChannel{}
	engine := newArtifactEngine(t, ch, artifactAgent{files: []string{path}})

	if _, err := engine.Submit(context.Background(), inbound("om_art_3", "生成报告")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ch.count() != 1 || !strings.Contains(ch.sent[0].Text, "当前通道不支持文件交付") {
		t.Fatalf("text = %+v", ch.sent)
	}
	runs := engine.Runs()
	if arts := engine.Artifacts(context.Background(), runs[0].ID); len(arts) != 1 {
		t.Fatalf("产物应保留: %+v", arts)
	}
}

func TestMultipleDocumentsAreZippedWithUTF8Names(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "报告.md", "report")
	b := writeFile(t, dir, "明细.csv", "a,b")
	ch := &artifactChannel{}
	engine := newArtifactEngine(t, ch, artifactAgent{files: []string{a, b}})

	if _, err := engine.Submit(context.Background(), inbound("om_art_4", "生成两个文件")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ch.artifacts) != 1 {
		t.Fatalf("应合并为一个 ZIP，实际 %d", len(ch.artifacts))
	}
	artifact := ch.artifacts[0].Artifact
	if artifact.Kind != "zip" || !strings.HasSuffix(artifact.Name, ".zip") {
		t.Fatalf("artifact = %+v", artifact)
	}
	reader, err := zip.OpenReader(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != 2 {
		t.Fatalf("zip entries = %d", len(reader.File))
	}
	for _, file := range reader.File {
		if file.Flags&0x800 == 0 {
			t.Fatalf("zip entry %q 缺少 UTF-8 标志", file.Name)
		}
	}
}

func TestImageAndDocumentsSplit(t *testing.T) {
	dir := t.TempDir()
	image := writeFile(t, dir, "chart.png", "png")
	a := writeFile(t, dir, "a.md", "a")
	b := writeFile(t, dir, "b.md", "b")
	ch := &artifactChannel{}
	engine := newArtifactEngine(t, ch, artifactAgent{files: []string{image, a, b}})

	if _, err := engine.Submit(context.Background(), inbound("om_art_5", "生成图表和文档")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ch.artifacts) != 2 {
		t.Fatalf("应发送图片和 ZIP 两个产物，实际 %d", len(ch.artifacts))
	}
	kinds := map[string]bool{}
	for _, item := range ch.artifacts {
		kinds[item.Artifact.Kind] = true
	}
	if !kinds["image"] || !kinds["zip"] {
		t.Fatalf("kinds = %+v", kinds)
	}
}
