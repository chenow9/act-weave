// Command prompt-preview-purge runs one-shot create-preview body cleanup.
// Intended for recovery/rollback windows (TD2-A / TD5-A).
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"actweave/backend/internal/agent"
	"actweave/backend/internal/config"
	"actweave/backend/internal/storedobject"

	_ "github.com/lib/pq"
)

func main() {
	_ = flag.Bool("once", true, "run a single purge pass and exit")
	flag.Parse()

	cfgPath := os.Getenv(config.ConfigFileEnv)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigFile
	}
	cfg, err := config.Load(cfgPath, os.LookupEnv)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	ctx := context.Background()
	db, err := sql.Open("postgres", cfg.Database.DSN)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping database: %v", err)
	}

	objectRepository, err := storedobject.NewRepository(db)
	if err != nil {
		log.Fatalf("stored object repository: %v", err)
	}
	objectStore, err := storedobject.NewMinIOStore(storedobject.MinIOConfig{
		Endpoint:  cfg.Storage.MinIO.Endpoint,
		AccessKey: cfg.Storage.MinIO.AccessKey,
		SecretKey: cfg.Storage.MinIO.SecretKey,
		UseSSL:    cfg.Storage.MinIO.UseSSL,
		Region:    cfg.Storage.MinIO.Region,
	}, objectRepository, denyAllStoredObjectAuthorizer{})
	if err != nil {
		log.Fatalf("object store: %v", err)
	}

	purgeCfg := agent.DefaultPreviewPurgeConfig()
	if cfg.AgentPrompt.PreviewPurge.IntervalSeconds > 0 {
		purgeCfg.Interval = time.Duration(cfg.AgentPrompt.PreviewPurge.IntervalSeconds) * time.Second
	}
	if cfg.AgentPrompt.PreviewPurge.BatchLimit > 0 {
		purgeCfg.BatchLimit = cfg.AgentPrompt.PreviewPurge.BatchLimit
	}
	if cfg.AgentPrompt.PreviewPurge.ClaimLeaseSeconds > 0 {
		purgeCfg.ClaimLease = time.Duration(cfg.AgentPrompt.PreviewPurge.ClaimLeaseSeconds) * time.Second
	}

	worker, err := agent.NewPreviewPurgeWorker(db, objectStore, purgeCfg, nil)
	if err != nil {
		log.Fatalf("purge worker: %v", err)
	}
	n, err := worker.RunOnce(ctx)
	if err != nil {
		log.Fatalf("purge once: %v", err)
	}
	fmt.Fprintf(os.Stdout, "purged=%d\n", n)
}

type denyAllStoredObjectAuthorizer struct{}

func (denyAllStoredObjectAuthorizer) AuthorizeStoredObjectRead(
	context.Context, storedobject.ReadAuthorization,
) error {
	return storedobject.ErrNotFound
}
