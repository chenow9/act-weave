package protocolevent_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"actweave/backend/internal/database/dbtest"
	"actweave/backend/internal/protocolevent"
)

const (
	queryPlanEventCount = 100_000
	queryPlanItemCount  = 10_000
)

func TestProtocolEventQueryPlans(t *testing.T) {
	db, insertDuration := setupProtocolEventCapacity(t)

	runReplay := explainPlan(t, db, `
		SELECT global_position,id,sequence_no,event_type,payload
		FROM protocol_events
		WHERE workspace_id=$1 AND agent_id=$2 AND conversation_id=$3 AND run_id=$4
		  AND sequence_no>$5
		ORDER BY sequence_no
		LIMIT 100
	`, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID, queryPlanEventCount-100)
	assertIndexedPlan(t, "run replay", runReplay, "protocol_events")

	globalDelivery := explainPlan(t, db, `
		SELECT global_position,id,event_type,payload
		FROM protocol_events
		WHERE global_position>$1
		ORDER BY global_position
		LIMIT 100
	`, queryPlanEventCount-100)
	assertIndexedPlan(t, "global delivery", globalDelivery, "protocol_events")

	itemSnapshot := explainPlan(t, db, `
		SELECT id,ordinal,item_type,status,snapshot
		FROM run_items
		WHERE workspace_id=$1 AND agent_id=$2 AND run_id=$3
		ORDER BY ordinal
		LIMIT 100
	`, protocolWorkspaceID, protocolAgentID, protocolRunID)
	assertIndexedPlan(t, "item snapshot", itemSnapshot, "run_items")

	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	events, err := reader.ReadRunAfter(context.Background(), protocolRunScope(), queryPlanEventCount-100, 100)
	readDuration := time.Since(started)
	if err != nil || len(events) != 100 || events[0].Sequence != queryPlanEventCount-99 ||
		events[len(events)-1].Sequence != queryPlanEventCount {
		t.Fatalf("100k run tail read count=%d err=%v", len(events), err)
	}

	var totalBytes, tableBytes, indexBytes, averageRowBytes int64
	if err := db.QueryRow(`
		SELECT pg_total_relation_size('protocol_events'),
		       pg_relation_size('protocol_events'),
		       pg_indexes_size('protocol_events'),
		       avg(pg_column_size(pe))::BIGINT
		FROM protocol_events pe
	`).Scan(&totalBytes, &tableBytes, &indexBytes, &averageRowBytes); err != nil {
		t.Fatal(err)
	}
	if averageRowBytes <= 0 || averageRowBytes > 8*1024 || totalBytes > 512*1024*1024 {
		t.Fatalf("unexpected event storage average=%d total=%d", averageRowBytes, totalBytes)
	}
	t.Logf(
		"capacity baseline events=%d insert=%s throughput=%.0f events/s avg_row=%dB table=%dB indexes=%dB total=%dB tail_read=%s",
		queryPlanEventCount, insertDuration,
		float64(queryPlanEventCount)/insertDuration.Seconds(), averageRowBytes,
		tableBytes, indexBytes, totalBytes, readDuration,
	)
}

func setupProtocolEventCapacity(t testing.TB) (*sql.DB, time.Duration) {
	t.Helper()
	testDatabase := dbtest.New(t)
	version := testDatabase.MigrateToLatest(t)
	if !version.Applied || version.Number != 4 || version.Dirty {
		t.Fatalf("expected query-plan schema version 4, got %+v", version)
	}
	db := testDatabase.Open(t)
	insertProtocolEventFixtures(t, db)
	insertProtocolStream(t, db)

	started := time.Now()
	if _, err := db.Exec(`
		INSERT INTO protocol_events(
		 id,workspace_id,agent_id,conversation_id,run_id,stream_id,
		 sequence_no,event_type,spec_version,payload,occurred_at
		)
		SELECT
		 ('c0000000-0000-4000-8000-' || lpad(to_hex(value),12,'0'))::UUID,
		 $1::UUID,$2::UUID,$3::UUID,$4::UUID,$5::UUID,value,'item.delta','1.0',
		 jsonb_build_object(
		   'specVersion','1.0','type','item.delta',
		   'eventId',('c0000000-0000-4000-8000-' || lpad(to_hex(value),12,'0'))::UUID,
		   'streamId','run:' || $4::TEXT,'sequence',value,
		   'occurredAt','2026-07-20T09:00:00Z',
		   'workspaceId',$1::UUID,'agentId',$2::UUID,
		   'conversationId',$3::UUID,'runId',$4::UUID,
		   'traceId','trace-query-plan','data',jsonb_build_object('index',value)
		 ),
		 '2026-07-20T09:00:00Z'::TIMESTAMPTZ
		FROM generate_series(1,$6) AS value
	`, protocolWorkspaceID, protocolAgentID, protocolSessionID, protocolRunID,
		protocolStreamID, queryPlanEventCount); err != nil {
		t.Fatalf("insert query-plan events: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE protocol_event_streams SET next_sequence=$2+1 WHERE id=$1
	`, protocolStreamID, queryPlanEventCount); err != nil {
		t.Fatal(err)
	}
	insertDuration := time.Since(started)

	if _, err := db.Exec(`
		INSERT INTO run_items(
		 id,workspace_id,agent_id,run_id,ordinal,item_type,status,
		 source_type,snapshot,started_at,completed_at
		)
		SELECT
		 ('d0000000-0000-4000-8000-' || lpad(to_hex(value),12,'0'))::UUID,
		 $1::UUID,$2::UUID,$3::UUID,value,'notice','completed','RUNTIME',
		 jsonb_build_object(
		   'id',('d0000000-0000-4000-8000-' || lpad(to_hex(value),12,'0'))::UUID,
		   'type','notice','status','completed','code','CAPACITY_ITEM','message','capacity baseline'
		 ),
		 '2026-07-20T09:00:00Z'::TIMESTAMPTZ,
		 '2026-07-20T09:00:01Z'::TIMESTAMPTZ
		FROM generate_series(1,$4) AS value
	`, protocolWorkspaceID, protocolAgentID, protocolRunID, queryPlanItemCount); err != nil {
		t.Fatalf("insert query-plan items: %v", err)
	}
	if _, err := db.Exec(`ANALYZE protocol_events; ANALYZE run_items`); err != nil {
		t.Fatal(err)
	}
	return db, insertDuration
}

func explainPlan(t testing.TB, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN (ANALYZE FALSE, COSTS OFF, FORMAT TEXT) "+query, args...)
	if err != nil {
		t.Fatalf("explain query: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func assertIndexedPlan(t testing.TB, name, plan, relation string) {
	t.Helper()
	if strings.Contains(plan, "Seq Scan on "+relation) ||
		(!strings.Contains(plan, "Index Scan") && !strings.Contains(plan, "Index Only Scan")) {
		t.Fatalf("%s did not use an index:\n%s", name, plan)
	}
	t.Logf("%s plan:\n%s", name, plan)
}

func BenchmarkProtocolEventReadPage(b *testing.B) {
	db, _ := setupProtocolEventCapacity(b)
	reader, err := protocolevent.NewEventReader(db)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		after := int64(queryPlanEventCount - 100 - index%1000)
		events, err := reader.ReadRunAfter(context.Background(), protocolRunScope(), after, 100)
		if err != nil || len(events) == 0 {
			b.Fatalf("read page after %d: count=%d err=%v", after, len(events), err)
		}
	}
}

func Example_queryPlanCapacity() {
	fmt.Println("100k events per run; replay is indexed by scope and sequence")
	// Output: 100k events per run; replay is indexed by scope and sequence
}
