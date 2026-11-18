package admin

import (
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// specPath 是仓库根的 OpenAPI 规范；测试从包目录出发。
const specPath = "../../docs/openapi.yaml"

type specDoc struct {
	OpenAPI string                    `yaml:"openapi"`
	Paths   map[string]map[string]any `yaml:"paths"`
}

// specOperations 返回规范里所有 /admin/api/ 下的 "METHOD /path"。
func specOperations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("读取规范失败: %v", err)
	}
	var doc specDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("规范不是合法 YAML: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Fatalf("期望 OpenAPI 3.1，实际 %q", doc.OpenAPI)
	}
	var out []string
	for path, ops := range doc.Paths {
		if !strings.HasPrefix(path, "/admin/api/") {
			continue
		}
		for method := range ops {
			switch strings.ToUpper(method) {
			case "GET", "PUT", "POST", "DELETE", "PATCH":
				out = append(out, strings.ToUpper(method)+" "+path)
			}
		}
	}
	return out
}

// normalizePattern 把 ServeMux 的通配写法收敛到规范写法。
//
// @remarks 两边只有一处差异：ServeMux 用 {path...} 表示剩余路径，
// OpenAPI 只能写成 {path}。其余（{id}、{name}）完全一致。
func normalizePattern(pattern string) string {
	return strings.ReplaceAll(pattern, "...}", "}")
}

// TestSpecMatchesRoutes 校验规范与路由表一一对应。
//
// @remarks 这是防止文档腐烂的唯一手段：加了路由却忘写规范，
// 或者规范里留着已删除的接口，都会在这里失败。
func TestSpecMatchesRoutes(t *testing.T) {
	spec := specOperations(t)
	code := make([]string, 0, len(apiRoutes))
	for _, r := range apiRoutes {
		if r.handler == nil {
			t.Fatalf("路由 %s 没有绑定 handler", r.pattern)
		}
		code = append(code, normalizePattern(r.pattern))
	}
	sort.Strings(spec)
	sort.Strings(code)

	inSpec := make(map[string]bool, len(spec))
	for _, op := range spec {
		inSpec[op] = true
	}
	inCode := make(map[string]bool, len(code))
	for _, op := range code {
		inCode[op] = true
	}

	for _, op := range code {
		if !inSpec[op] {
			t.Errorf("代码有路由但规范缺失: %s", op)
		}
	}
	for _, op := range spec {
		if !inCode[op] {
			t.Errorf("规范有路由但代码缺失: %s", op)
		}
	}
}

// TestRoutesHaveNoDuplicates 确保同一 method+path 不会被注册两次。
//
// @remarks ServeMux 对重复模式会 panic，但真跑起来才发现成本太高。
func TestRoutesHaveNoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(apiRoutes))
	for _, r := range apiRoutes {
		if seen[r.pattern] {
			t.Errorf("重复注册: %s", r.pattern)
		}
		seen[r.pattern] = true
	}
}

// TestRoutesAreUnderAdminPrefix 确认没有路由跑出管理面前缀。
func TestRoutesAreUnderAdminPrefix(t *testing.T) {
	for _, r := range apiRoutes {
		if !strings.HasPrefix(r.pattern, "GET /admin/api/") &&
			!strings.HasPrefix(r.pattern, "PUT /admin/api/") &&
			!strings.HasPrefix(r.pattern, "POST /admin/api/") &&
			!strings.HasPrefix(r.pattern, "DELETE /admin/api/") {
			t.Errorf("路由不在 /admin/api/ 下: %s", r.pattern)
		}
	}
}
