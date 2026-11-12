package store_test

import (
	"context"
	"testing"

	"github.com/moying/agenthub/internal/store"
)

func TestIdentityRoundTrip(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()
	db, err := store.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}

	j := store.NewJournal(db)
	const conv = "id:chat-id:user-id"
	if err := j.SaveIdentity(ctx, conv, "运维群", "张三"); err != nil {
		t.Fatal(err)
	}
	got, err := j.LoadIdentities(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	pair := got[conv]
	if pair[0] != "运维群" || pair[1] != "张三" {
		t.Fatalf("identity = %v", pair)
	}
}
