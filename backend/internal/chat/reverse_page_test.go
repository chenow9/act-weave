package chat_test

import (
	"context"
	"testing"
	"time"

	"actweave/backend/internal/chat"
	"actweave/backend/internal/database/dbtest"
)

func TestListMessagesReversePageBoundedOrder(t *testing.T) {
	testDatabase := dbtest.New(t)
	testDatabase.MigrateToLatest(t)
	db := testDatabase.Open(t)
	const (
		owner   = "d08f1f2e-7b5a-7c3d-8e9f-123456789001"
		ws      = "d08f1f2e-7b5a-7c3d-8e9f-123456789002"
		model   = "d08f1f2e-7b5a-7c3d-8e9f-123456789003"
		agent   = "d08f1f2e-7b5a-7c3d-8e9f-123456789004"
		session = "d08f1f2e-7b5a-7c3d-8e9f-123456789005"
		hash    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	must(`INSERT INTO users(id,username,display_name) VALUES($1,'p.owner','P')`, owner)
	must(`INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by) VALUES($1,'page','P','SANDBOX',$2,$2,$2)`, ws, owner)
	must(`INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by) VALUES($1,$2,'m','openai','https://x','m',$3,$3)`, model, ws, owner)
	must(`INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by) VALUES($1,$2,'a',$3,$4,$4)`, agent, ws, model, owner)
	must(`INSERT INTO chat_sessions(id,workspace_id,agent_id,title,created_by) VALUES($1,$2,$3,'s',$4)`, session, ws, agent, owner)

	base := time.Now().UTC().Add(-time.Hour)
	ids := []string{
		"d08f1f2e-7b5a-7c3d-8e9f-123456789010",
		"d08f1f2e-7b5a-7c3d-8e9f-123456789011",
		"d08f1f2e-7b5a-7c3d-8e9f-123456789012",
		"d08f1f2e-7b5a-7c3d-8e9f-123456789013",
		"d08f1f2e-7b5a-7c3d-8e9f-123456789014",
	}
	for i, id := range ids {
		ts := base.Add(time.Duration(i) * time.Minute)
		must(`INSERT INTO chat_messages(
			id,workspace_id,session_id,role,content,content_sha256,content_length,status,created_by,created_at
		) VALUES($1,$2,$3,'USER',$4,$5,$6,'RECEIVED',$7,$8)`,
			id, ws, session, "msg-"+id[len(id)-2:], hash, 10, owner, ts)
	}

	repo, err := chat.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	page1, err := repo.ListMessagesReversePage(context.Background(), ws, session, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Messages) != 2 || !page1.HasMore || page1.NextCursor == nil {
		t.Fatalf("page1: %+v", page1)
	}
	if page1.Messages[0].ID != ids[4] || page1.Messages[1].ID != ids[3] {
		t.Fatalf("expected newest first, got %s %s", page1.Messages[0].ID, page1.Messages[1].ID)
	}
	// Bodies stay inline; no object decrypt path exercised — content is present without objects.
	page2, err := repo.ListMessagesReversePage(context.Background(), ws, session, 2, page1.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Messages) != 2 {
		t.Fatalf("page2: %+v", page2)
	}
	if page2.Messages[0].ID != ids[2] {
		t.Fatalf("cursor continuity: %s", page2.Messages[0].ID)
	}
}
