// protocolcompat compares the live Schema Registry against a frozen baseline.
//
//	go run ./cmd/protocolcompat -check
//	go run ./cmd/protocolcompat -write-baseline
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"actweave/backend/internal/protocolcompat"
)

func main() {
	check := flag.Bool("check", false, "fail on breaking changes vs baseline")
	writeBaseline := flag.Bool("write-baseline", false, "rewrite baseline from current schemas")
	reportPath := flag.String("report", "", "optional markdown report path")
	flag.Parse()
	if !*check && !*writeBaseline {
		*check = true
	}
	if err := run(*check, *writeBaseline, *reportPath); err != nil {
		fmt.Fprintf(os.Stderr, "protocolcompat: %v\n", err)
		os.Exit(1)
	}
}

func run(check, writeBaseline bool, reportPath string) error {
	schemaDir, baselinePath, defaultReport, err := resolvePaths()
	if err != nil {
		return err
	}
	if reportPath == "" {
		reportPath = defaultReport
	}

	docs, err := protocolcompat.LoadSchemaDocuments(schemaDir)
	if err != nil {
		return err
	}
	setSHA, err := schemaSetSHA(schemaDir, docs)
	if err != nil {
		return err
	}
	current := protocolcompat.ExtractBaseline("2026-08-11", "1.0", setSHA, docs)

	if writeBaseline {
		if err := protocolcompat.WriteBaselineJSON(baselinePath, current); err != nil {
			return err
		}
		fmt.Printf("protocolcompat: wrote baseline %s (schema-set %s)\n", baselinePath, setSHA)
		return nil
	}

	old, err := protocolcompat.ReadBaselineJSON(baselinePath)
	if err != nil {
		return fmt.Errorf("read baseline (run with -write-baseline first): %w", err)
	}
	report := protocolcompat.CompareBaselines(old, current)
	markdown := protocolcompat.FormatMarkdown(report, old.SchemaSetSHA256, current.SchemaSetSHA256)
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(reportPath, []byte(markdown), 0o644); err != nil {
		return err
	}
	fmt.Print(markdown)
	if check && report.HasBreaking() {
		return fmt.Errorf("%d breaking change(s) detected (see %s)", len(report.Breaking), reportPath)
	}
	fmt.Printf("protocolcompat: PASS (%d additive finding(s))\n", len(report.Additive))
	return nil
}

func resolvePaths() (schemaDir, baselinePath, reportPath string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", "", err
	}
	candidates := []string{
		filepath.Join(cwd, "internal", "protocolschema", "schemas", "aap", "v1"),
		filepath.Join(cwd, "backend", "internal", "protocolschema", "schemas", "aap", "v1"),
	}
	for _, dir := range candidates {
		if st, statErr := os.Stat(dir); statErr == nil && st.IsDir() {
			schemaDir = dir
			break
		}
	}
	if schemaDir == "" {
		return "", "", "", fmt.Errorf("cannot locate schemas/aap/v1")
	}
	// schemaDir → .../protocolschema/schemas/aap/v1
	protocolschemaRoot := filepath.Clean(filepath.Join(schemaDir, "..", "..", ".."))
	baselinePath = filepath.Join(protocolschemaRoot, "baseline", "aap-v1.baseline.json")
	repoRoot := filepath.Clean(filepath.Join(protocolschemaRoot, "..", "..", ".."))
	reportPath = filepath.Join(repoRoot, "docs", "verification", "protocol-compat-report.md")
	return schemaDir, baselinePath, reportPath, nil
}

func schemaSetSHA(dir string, docs map[string]map[string]any) (string, error) {
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)
	set := sha256.New()
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		_, _ = set.Write(raw)
	}
	return fmt.Sprintf("%x", set.Sum(nil)), nil
}

