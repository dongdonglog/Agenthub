package store_test

import (
	"context"
	"testing"

	"github.com/moying/agenthub/internal/store"
)

func TestKnowledgeTeamIsolation(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE knowledge_doc`); err != nil {
		t.Fatal(err)
	}
	ks := store.NewKnowledgeStore(db)
	for _, doc := range []store.KnowledgeDoc{
		{Title: "payments login", Content: "payments runbook", Source: "payments/a.md", Team: "payments"},
		{Title: "other login", Content: "other runbook", Source: "other/a.md", Team: "other"},
		{Title: "global login", Content: "global runbook", Source: "global.md"},
	} {
		if err := ks.Upsert(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := ks.Search(ctx, "login", "payments", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %+v", docs)
	}
	for _, doc := range docs {
		if doc.Team == "other" {
			t.Fatalf("leaked other team doc: %+v", doc)
		}
	}
	all, err := ks.Search(ctx, "login", "", 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("empty team should return all: %+v err=%v", all, err)
	}
}
