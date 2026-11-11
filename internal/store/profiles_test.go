package store_test

import (
	"testing"

	"github.com/moying/agenthub/internal/store"
)

func TestProfileApplyAndActive(t *testing.T) {
	dir := t.TempDir()
	ps := store.NewProfileStore(dir)
	if err := ps.PutProfile(store.Profile{Name: "a", Skills: []string{"x"}, MCPs: []string{"demo"}}); err != nil {
		t.Fatal(err)
	}
	if err := ps.PutProfile(store.Profile{Name: "b", Skills: []string{"y"}}); err != nil {
		t.Fatal(err)
	}
	if active := ps.Active(); active.Name != "" {
		t.Fatalf("默认不应有生效档案: %+v", active)
	}
	if err := ps.Apply("a"); err != nil {
		t.Fatal(err)
	}
	active := ps.Active()
	if active.Name != "a" || len(active.Skills) != 1 || active.Skills[0] != "x" || len(active.MCPs) != 1 {
		t.Fatalf("active = %+v", active)
	}
	if err := ps.Apply("missing"); err == nil {
		t.Fatal("不存在的 Profile 应报错")
	}
}
