package action

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/store"
)

// TestK8sScaleIntegration 需要真实集群和 AGENTHUB_TEST_K8S=1，CI 默认跳过。
func TestK8sScaleIntegration(t *testing.T) {
	if os.Getenv("AGENTHUB_TEST_K8S") != "1" {
		t.Skip("未设置 AGENTHUB_TEST_K8S=1，跳过真实 K8s 用例")
	}
	e := &Executor{Kubectl: "kubectl", Namespaces: map[string]bool{"agenthub-test": true}}
	params, _ := json.Marshal(map[string]any{"namespace": "agenthub-test", "deployment": "nginx", "replicas": 2})
	if out, err := e.Execute(context.Background(), &store.Approval{Action: "k8s_scale", Params: params}); err != nil {
		t.Fatalf("scale: %v %s", err, out)
	}
	out, err := exec.Command("kubectl", "-n", "agenthub-test", "get", "deploy", "nginx", "-o", "jsonpath={.spec.replicas}").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "2" {
		t.Fatalf("replicas = %s", out)
	}
	if out, err := e.Execute(context.Background(), &store.Approval{Action: "k8s_restart", Params: params}); err != nil {
		t.Fatalf("restart: %v %s", err, out)
	}
}
