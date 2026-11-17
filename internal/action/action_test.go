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
