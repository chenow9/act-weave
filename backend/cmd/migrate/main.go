package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	backendconfig "actweave/backend/internal/config"
	"actweave/backend/internal/database"
)

type migrationRunner interface {
	Up() error
	Down(steps int) error
	Version() (database.Version, error)
	Close() error
}

type openMigrationRunner func(context.Context, string) (migrationRunner, error)

func main() {
	config, _, err := backendconfig.LoadFromEnvironment(os.LookupEnv)
	if err == nil {
		err = run(context.Background(), os.Args[1:], config, os.Stdout, openRunner)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
		os.Exit(1)
	}
}

func openRunner(ctx context.Context, dsn string) (migrationRunner, error) {
	return database.Open(ctx, dsn)
}

func run(
	ctx context.Context,
	args []string,
	config backendconfig.Config,
	out io.Writer,
	open openMigrationRunner,
) (returnErr error) {
	if len(args) == 0 {
		return errors.New("usage: migrate <up|down [steps]|version>")
	}
	if open == nil {
		return errors.New("migration command is not configured")
	}

	operation := args[0]
	downSteps, err := parseOperation(operation, args[1:])
	if err != nil {
		return err
	}

	if err := config.ValidateDatabase(); err != nil {
		return fmt.Errorf("validate migration configuration: %w", err)
	}
	runner, err := open(ctx, config.Database.DSN)
	if err != nil {
		return fmt.Errorf("open migrator: %w", err)
	}
	defer func() {
		if closeErr := runner.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close migrator: %w", closeErr))
		}
	}()

	switch operation {
	case "up":
		if err := runner.Up(); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "migrations applied")
	case "down":
		if err := runner.Down(downSteps); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "rolled back %d migration(s)\n", downSteps)
	case "version":
		version, err := runner.Version()
		if err != nil {
			return err
		}
		if !version.Applied {
			_, _ = fmt.Fprintln(out, "version=none dirty=false")
			return nil
		}
		_, _ = fmt.Fprintf(out, "version=%d dirty=%t\n", version.Number, version.Dirty)
	}
	return nil
}

func parseOperation(operation string, args []string) (int, error) {
	switch operation {
	case "up", "version":
		if len(args) != 0 {
			return 0, fmt.Errorf("%s does not accept arguments", operation)
		}
		return 0, nil
	case "down":
		if len(args) > 1 {
			return 0, errors.New("down accepts at most one step count")
		}
		if len(args) == 0 {
			return 1, nil
		}
		steps, err := strconv.Atoi(args[0])
		if err != nil || steps < 1 {
			return 0, fmt.Errorf("down steps must be a positive integer: %q", args[0])
		}
		return steps, nil
	default:
		return 0, fmt.Errorf("unknown migration operation %q; expected up, down, or version", operation)
	}
}
