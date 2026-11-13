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
