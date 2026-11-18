package action

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/store"
)

func fakeKubectl(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestK8sScaleAllowed(t *testing.T) {
	e := &Executor{Kubectl: fakeKubectl(t), Namespaces: map[string]bool{"prod": true}}
	params, _ := json.Marshal(map[string]any{"namespace": "prod", "deployment": "checkout", "replicas": 3})
	out, err := e.Execute(context.Background(), &store.Approval{Action: "k8s_scale", Params: params})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "scale deployment/checkout --replicas 3 -n prod") {
		t.Fatalf("out = %s", out)
	}
}

func TestK8sRejectsOutsideAllowlist(t *testing.T) {
	e := &Executor{Kubectl: fakeKubectl(t), Namespaces: map[string]bool{"prod": true}}
	params, _ := json.Marshal(map[string]any{"namespace": "default", "deployment": "checkout", "replicas": 3})
	if _, err := e.Execute(context.Background(), &store.Approval{Action: "k8s_scale", Params: params}); err == nil {
		t.Fatal("expected namespace rejection")
	}
}

func TestParseRequest(t *testing.T) {
	t.Run("scale 归一化", func(t *testing.T) {
		req, params, err := ParseRequest([]byte(`{"action":" k8s_scale ","namespace":"prod","deployment":"checkout","replicas":3,"reason":" 错误率上升 ","extra":"drop"}`))
		if err != nil {
			t.Fatal(err)
		}
		if req.Action != "k8s_scale" || req.Reason != "错误率上升" {
			t.Fatalf("req = %+v", req)
		}
		var parsed map[string]any
		if err := json.Unmarshal(params, &parsed); err != nil {
			t.Fatal(err)
		}
		// 未知字段不得进入审批参数。
		if _, ok := parsed["extra"]; ok {
			t.Fatalf("多余字段泄漏进参数: %s", params)
		}
		if _, ok := parsed["reason"]; ok {
			t.Fatalf("reason 不应进入执行参数: %s", params)
		}
		if parsed["replicas"] != float64(3) {
			t.Fatalf("params = %s", params)
		}
	})

	t.Run("restart 忽略 replicas", func(t *testing.T) {
		_, params, err := ParseRequest([]byte(`{"action":"k8s_restart","namespace":"prod","deployment":"checkout","replicas":9}`))
		if err != nil {
			t.Fatal(err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(params, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed["replicas"] != float64(0) {
			t.Fatalf("restart 不应带 replicas: %s", params)
		}
	})

	for name, body := range map[string]string{
		"不支持的动作":        `{"action":"kubectl_delete","namespace":"prod","deployment":"checkout"}`,
		"非法 namespace":  `{"action":"k8s_scale","namespace":"prod/../x","deployment":"checkout","replicas":1}`,
		"副本数越界":         `{"action":"k8s_scale","namespace":"prod","deployment":"checkout","replicas":99}`,
		"缺少 deployment": `{"action":"k8s_restart","namespace":"prod"}`,
		"非 JSON":        `not json`,
		"空动作":           `{"namespace":"prod","deployment":"checkout"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseRequest([]byte(body)); err == nil {
				t.Fatalf("应拒绝: %s", body)
			}
		})
	}
}
