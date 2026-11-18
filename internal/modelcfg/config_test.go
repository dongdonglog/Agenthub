package modelcfg

import "testing"

func TestLegacyConversion(t *testing.T) {
	cfg := Legacy("https://gw/v1", "sk", "primary-model", "fallback-model", "vision-model")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	route, ok := cfg.Route("main")
	if !ok || len(route.Candidates) != 2 {
		t.Fatalf("main route = %+v", route)
	}
	vision, ok := cfg.Route("vision")
	if !ok || len(vision.Candidates) != 1 {
		t.Fatalf("vision route = %+v", vision)
	}
	if _, ok := cfg.Model("fallback"); !ok {
		t.Fatal("缺少 fallback 模型")
	}
}

func TestValidateRejectsUnknownCandidate(t *testing.T) {
	cfg := Config{Version: 2,
		Providers: []Provider{{Name: "p", BaseURL: "https://gw/v1"}},
		Models:    []Model{{ID: "m", Provider: "p", Model: "up"}},
		Routes:    []Route{{Name: "main", Candidates: []string{"missing"}}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("应拒绝不存在的候选")
	}
}

// TestHasCredentials 决定装配时是否退回回显执行器。
//
// @remarks 没配密钥时若仍走 pi + 模型网关，每次运行都会失败；本地冒烟和演示
// 应该能直接看到链路跑通。
func TestHasCredentials(t *testing.T) {
	if Legacy("https://gw/v1", "sk-test", "m1", "", "").HasCredentials() {
		// 有密钥应判定为可用，这里反向断言避免写反。
	} else {
		t.Fatal("配了密钥应判定为有凭据")
	}
	if Legacy("https://gw/v1", "", "m1", "", "").HasCredentials() {
		t.Fatal("空密钥不应判定为有凭据")
	}
	// 没有 main 线路（例如配置残缺）也应判定为无凭据。
	if (Config{Version: 2}).HasCredentials() {
		t.Fatal("缺少 main 线路不应判定为有凭据")
	}
	// 候选指向不存在的供应商。
	broken := Config{Version: 2,
		Models: []Model{{ID: "m", Provider: "missing"}},
		Routes: []Route{{Name: "main", Candidates: []string{"m"}}},
	}
	if broken.HasCredentials() {
		t.Fatal("供应商不存在不应判定为有凭据")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/settings.yaml")
	cfg := Legacy("https://gw/v1", "sk", "m1", "", "")
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 1 || loaded.Providers[0].APIKey != "sk" {
		t.Fatalf("loaded = %+v", loaded)
	}
}
