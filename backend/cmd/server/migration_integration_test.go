package main

import (
	"context"
	"sync"
	"testing"

	"actweave/backend/internal/database"
	"actweave/backend/internal/database/dbtest"
)

func TestConcurrentServerStartupAppliesMigrationsOnce(t *testing.T) {
	testDatabase := dbtest.New(t)

	const serverInstances = 3
	start := make(chan struct{})
	errorsByInstance := make(chan error, serverInstances)
	var waitGroup sync.WaitGroup
	for instance := 0; instance < serverInstances; instance++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			errorsByInstance <- migrateDatabase(context.Background(), testDatabase.DSN())
		}()
	}
	close(start)
	waitGroup.Wait()
	close(errorsByInstance)
	for err := range errorsByInstance {
		if err != nil {
			t.Fatalf("concurrent startup migration failed: %v", err)
		}
	}

	migrator, err := database.Open(context.Background(), testDatabase.DSN())
	if err != nil {
		t.Fatalf("open migrator after concurrent startup: %v", err)
	}
	version, err := migrator.Version()
	if err != nil {
		_ = migrator.Close()
		t.Fatalf("read migration version after concurrent startup: %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migration version reader: %v", err)
	}
	if !version.Applied || version.Number < 1 || version.Dirty {
		t.Fatalf("expected one clean latest migration state, got %+v", version)
	}

	db := testDatabase.Open(t)
	var versionRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versionRows); err != nil {
		t.Fatalf("count migration version rows: %v", err)
	}
	if versionRows != 1 {
		t.Fatalf("expected exactly one migration state row, got %d", versionRows)
	}
}
