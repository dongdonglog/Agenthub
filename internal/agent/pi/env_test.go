package pi

import (
	"strings"
	"testing"
)

// mergeEnv 必须让注入值覆盖基底里的同名变量，且不产生重复项。
// 这里守的是真实踩过的坑：cmd.Env 被赋值两次时 AGENTHUB_READONLY 会丢失，
// 只读 guard 随之失效。
func TestMergeEnvOverride(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/node", "AGENTHUB_READONLY=0"}
	override := []string{"PATH=/ws/.agenthub-bin:/usr/bin", "AGENTHUB_READONLY=1", "AGENTHUB_CASE_ID=case_1"}

	got := mergeEnv(base, override)
	env := map[string]string{}
	for _, item := range got {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			t.Fatalf("非法条目: %q", item)
		}
		if _, dup := env[key]; dup {
			t.Fatalf("重复键 %s", key)
		}
		env[key] = value
	}
	if env["AGENTHUB_READONLY"] != "1" {
		t.Fatalf("AGENTHUB_READONLY = %q，注入值必须生效", env["AGENTHUB_READONLY"])
	}
	if env["PATH"] != "/ws/.agenthub-bin:/usr/bin" {
		t.Fatalf("PATH = %q", env["PATH"])
	}
	if env["HOME"] != "/home/node" {
		t.Fatalf("基底变量不应丢失: HOME=%q", env["HOME"])
	}
	if env["AGENTHUB_CASE_ID"] != "case_1" {
		t.Fatalf("AGENTHUB_CASE_ID = %q", env["AGENTHUB_CASE_ID"])
	}
	if len(got) != 4 {
		t.Fatalf("条目数 = %d，期望 4: %v", len(got), got)
	}
}
