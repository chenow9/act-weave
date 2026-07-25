package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	backendconfig "actweave/backend/internal/config"
	"actweave/backend/internal/database"
)

type fakeMigrationRunner struct {
	upCalls      int
	downSteps    []int
	versionCalls int
	version      database.Version
	operationErr error
	closeErr     error
}

func (f *fakeMigrationRunner) Up() error {
	f.upCalls++
	return f.operationErr
}

func (f *fakeMigrationRunner) Down(steps int) error {
	f.downSteps = append(f.downSteps, steps)
	return f.operationErr
}

func (f *fakeMigrationRunner) Version() (database.Version, error) {
	f.versionCalls++
	return f.version, f.operationErr
}

func (f *fakeMigrationRunner) Close() error {
	return f.closeErr
}

func TestRunMigrationOperations(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		version       database.Version
		wantOutput    string
		wantUpCalls   int
		wantDownSteps []int
		wantVersions  int
	}{
		{name: "up", args: []string{"up"}, wantOutput: "migrations applied\n", wantUpCalls: 1},
		{name: "down one", args: []string{"down"}, wantOutput: "rolled back 1 migration(s)\n", wantDownSteps: []int{1}},
		{name: "down several", args: []string{"down", "3"}, wantOutput: "rolled back 3 migration(s)\n", wantDownSteps: []int{3}},
		{
			name:         "version",
			args:         []string{"version"},
			version:      database.Version{Number: 7, Applied: true},
			wantOutput:   "version=7 dirty=false\n",
			wantVersions: 1,
		},
		{
			name:         "empty version",
			args:         []string{"version"},
			wantOutput:   "version=none dirty=false\n",
			wantVersions: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeMigrationRunner{version: test.version}
			var output bytes.Buffer
			var openedDSN string
			err := run(
				context.Background(),
				test.args,
				migrationConfig("postgres://migration-test"),
				&output,
				func(_ context.Context, dsn string) (migrationRunner, error) {
					openedDSN = dsn
					return fake, nil
				},
			)
			if err != nil {
				t.Fatalf("run migration: %v", err)
			}
			if openedDSN != "postgres://migration-test" {
				t.Fatalf("unexpected opened DSN %q", openedDSN)
			}
			if output.String() != test.wantOutput {
				t.Fatalf("unexpected output %q", output.String())
			}
			if fake.upCalls != test.wantUpCalls || fake.versionCalls != test.wantVersions {
				t.Fatalf("unexpected calls: up=%d version=%d", fake.upCalls, fake.versionCalls)
			}
			if len(fake.downSteps) != len(test.wantDownSteps) {
				t.Fatalf("unexpected down calls %v", fake.downSteps)
			}
			for index := range fake.downSteps {
				if fake.downSteps[index] != test.wantDownSteps[index] {
					t.Fatalf("unexpected down calls %v", fake.downSteps)
				}
			}
		})
	}
}

func TestRunRejectsInvalidInputBeforeOpeningDatabase(t *testing.T) {
	tests := [][]string{
		nil,
		{"unknown"},
		{"up", "extra"},
		{"version", "extra"},
		{"down", "0"},
		{"down", "not-a-number"},
		{"down", "1", "2"},
	}
	for _, args := range tests {
		opened := false
		err := run(
			context.Background(),
			args,
			migrationConfig("postgres://migration-test"),
			&bytes.Buffer{},
			func(context.Context, string) (migrationRunner, error) {
				opened = true
				return &fakeMigrationRunner{}, nil
			},
		)
		if err == nil {
			t.Fatalf("expected args %v to fail", args)
		}
		if opened {
			t.Fatalf("invalid args %v must not open a database", args)
		}
	}
}

func TestRunRequiresDSNWithoutLeakingIt(t *testing.T) {
	opened := false
	err := run(
		context.Background(),
		[]string{"up"},
		migrationConfig(""),
		&bytes.Buffer{},
		func(context.Context, string) (migrationRunner, error) {
			opened = true
			return &fakeMigrationRunner{}, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "database.dsn") {
		t.Fatalf("expected missing DSN error, got %v", err)
	}
	if opened {
		t.Fatal("missing DSN must not open a database")
	}
}

func TestRunJoinsOperationAndCloseErrors(t *testing.T) {
	operationErr := errors.New("operation failed")
	closeErr := errors.New("close failed")
	err := run(
		context.Background(),
		[]string{"up"},
		migrationConfig("postgres://migration-test"),
		&bytes.Buffer{},
		func(context.Context, string) (migrationRunner, error) {
			return &fakeMigrationRunner{operationErr: operationErr, closeErr: closeErr}, nil
		},
	)
	if !errors.Is(err, operationErr) || !errors.Is(err, closeErr) {
		t.Fatalf("expected joined operation and close errors, got %v", err)
	}
}

func migrationConfig(dsn string) backendconfig.Config {
	return backendconfig.Config{Database: backendconfig.DatabaseConfig{DSN: dsn}}
}
