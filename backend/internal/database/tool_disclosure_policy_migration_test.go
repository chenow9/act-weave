package database_test

import (
	"strings"
	"testing"

	"actweave/backend/internal/database/dbtest"
)

func TestLatestAssemblyCouplingRejectsNullCatalogDigest(t *testing.T) {
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 23 || version.Dirty {
		t.Fatalf("expected clean latest migration 23, got %+v", version)
	}
	db := testDatabase.Open(t)

	const (
		owner = "a18f1f2e-7b5a-7c3d-8e9f-a23456789001"
		ws    = "a18f1f2e-7b5a-7c3d-8e9f-a23456789002"
		model = "a18f1f2e-7b5a-7c3d-8e9f-a23456789003"
		agent = "a18f1f2e-7b5a-7c3d-8e9f-a23456789004"
		hash  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	if _, err := db.Exec(`INSERT INTO users(id,username,display_name) VALUES($1,'td.owner','TD')`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces(id,slug,display_name,mode,owner_user_id,created_by,updated_by)
		VALUES($1,'td-space','TD','SANDBOX',$2,$2,$2)
	`, ws, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO model_configs(id,workspace_id,name,provider,api_base,model_name,created_by,updated_by)
		VALUES($1,$2,'TD Model','openai','https://models.example','m',$3,$3)
	`, model, ws, owner); err != nil {
		t.Fatal(err)
	}
	var policy string
	if err := db.QueryRow(`SELECT tool_disclosure_policy::text FROM model_configs WHERE id=$1`, model).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(policy, "{}") {
		t.Fatalf("default tool_disclosure_policy=%q", policy)
	}
	if _, err := db.Exec(`
		INSERT INTO agents(id,workspace_id,name,model_config_id,created_by,updated_by)
		VALUES($1,$2,'TD Agent',$3,$4,$4)
	`, agent, ws, model, owner); err != nil {
		t.Fatal(err)
	}

	insertRun := func(id string) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO agent_runs(id,workspace_id,agent_id,status,trigger_type,triggered_by_type,triggered_by_id,trace_id,model_snapshot,capability_snapshot)
			VALUES($1,$2,$3,'RUNNING','CHAT','USER',$4,'t','{}','{}')
		`, id, ws, agent, owner); err != nil {
			t.Fatal(err)
		}
	}

	// client_bounded with otherwise-valid estimator/overhead but NULL digest.
	runClient := "a18f1f2e-7b5a-7c3d-8e9f-a23456789011"
	insertRun(runClient)
	if _, err := db.Exec(`
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,assembly_digest,estimated_total_tokens,
			tool_search_mode,tool_catalog_digest,
			immediate_tool_count,deferred_tool_count,max_loaded_tool_count,
			immediate_tools_tokens,deferred_metadata_tokens,dynamic_tool_load_reserve_tokens
		) VALUES (
			$1,$2,$3,'token_window',$4,$4,$4,$4,
			'o200k_base','contextwindow-estimator.agentic-openai-responses.v1',
			1000,100,10,15,$4,$4,50,
			'client_bounded',NULL,
			0,3,3,0,5,10
		)
	`, "a18f1f2e-7b5a-7c3d-8e9f-a23456789021", ws, runClient, hash); err == nil {
		t.Fatal("expected client_bounded NULL digest reject")
	}

	runPlat := "a18f1f2e-7b5a-7c3d-8e9f-a23456789012"
	insertRun(runPlat)
	if _, err := db.Exec(`
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,assembly_digest,estimated_total_tokens,
			tool_search_mode,tool_catalog_digest,
			immediate_tool_count,deferred_tool_count,max_loaded_tool_count,
			immediate_tools_tokens,deferred_metadata_tokens,dynamic_tool_load_reserve_tokens
		) VALUES (
			$1,$2,$3,'token_window',$4,$4,$4,$4,
			'o200k_base','contextwindow-estimator.agentic-openai-responses.v2',
			1000,100,10,12,$4,$4,50,
			'platform_bounded',NULL,
			1,9,5,2,0,10
		)
	`, "a18f1f2e-7b5a-7c3d-8e9f-a23456789022", ws, runPlat, hash); err == nil {
		t.Fatal("expected platform_bounded NULL digest reject")
	}

	runCarry := "a18f1f2e-7b5a-7c3d-8e9f-a23456789013"
	insertRun(runCarry)
	if _, err := db.Exec(`
		INSERT INTO agent_run_context_assemblies(
			id,workspace_id,run_id,mode,
			policy_snapshot_hash,model_snapshot_hash,capability_snapshot_hash,agent_snapshot_hash,
			estimator_profile,estimator_version,
			hard_input_ceiling_tokens,output_reserve_tokens,safety_margin_tokens,tools_overhead_tokens,
			system_prompt_hash,assembly_digest,estimated_total_tokens,
			tool_search_mode,tool_catalog_digest,
			immediate_tool_count,deferred_tool_count,max_loaded_tool_count,
			immediate_tools_tokens,deferred_metadata_tokens,dynamic_tool_load_reserve_tokens
		) VALUES (
			$1,$2,$3,'token_window',$4,$4,$4,$4,
			'o200k_base','contextwindow-estimator.agentic-openai-responses.v2',
			1000,100,10,8,$4,$4,50,
			'carry_all',NULL,
			4,0,0,8,0,0
		)
	`, "a18f1f2e-7b5a-7c3d-8e9f-a23456789023", ws, runCarry, hash); err == nil {
		t.Fatal("expected carry_all NULL digest reject")
	}
}
