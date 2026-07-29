# Database migrations

The embedded migration set starts from a **baseline** plus additive steps:

```text
000001_init.up.sql / 000001_init.down.sql
000002_session_context_contracts.up.sql / 000002_session_context_contracts.down.sql
```

Historical step migrations (`000001`–`000061` before the squash) are preserved under
[`../migrations_archive/`](../migrations_archive/) for reference only. They are **not**
embedded into the binary and are not applied by `cmd/migrate`.

## Rules

- new schema changes continue as `000003_*.up.sql` / `000003_*.down.sql` and beyond;
- every new migration must have a matching down file until production policy says otherwise;
- schema changes belong here, never in service startup or repository code;
- migrations must not read, transform, or dual-write retired aggregate snapshots;
- use PostgreSQL `TIMESTAMPTZ`, textual status checks, explicit foreign-key behavior, and direct `workspace_id` columns as required by the architecture;
- use lowercase `snake_case` for database objects and names that describe the domain rather than a transport DTO;
- use `UUID` for entity identifiers and generate UUIDv7 in application code; do not add random UUID defaults to tables without an explicit design decision;
- rely on the database-level UTC setting installed by the baseline migration and still pass explicit UTC values at application boundaries;
- run migrations through `go run ./cmd/migrate up` (or the compiled `actweave-migrate` binary).

## Existing databases

Databases that already applied the pre-squash step chain (version `61`) are **not**
compatible with this baseline (version `1`) without a reset. Formal test and local
environments should drop and recreate the database, then run `migrate up`.

## Archive

See `migrations_archive/MANIFEST.txt` for the ordered list of retired step migrations.
