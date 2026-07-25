# Database migrations

Migration files use the `golang-migrate` naming convention:

```text
NNNNNN_description.up.sql
NNNNNN_description.down.sql
```

Rules:

- every migration must have a tested down file until the final production cutover policy says otherwise;
- schema changes belong here, never in service startup or repository code;
- migrations must not read, transform, or dual-write retired aggregate snapshots;
- use PostgreSQL `TIMESTAMPTZ`, textual status checks, explicit foreign-key behavior, and direct `workspace_id` columns as required by the architecture;
- use lowercase `snake_case` for database objects and names that describe the domain rather than a transport DTO;
- use `UUID` for entity identifiers and generate UUIDv7 in application code; do not add random UUID defaults to tables without an explicit design decision;
- rely on the database-level UTC setting installed by the baseline migration and still pass explicit UTC values at application boundaries;
- run migrations through `go run ./cmd/migrate up` (or the compiled `actweave-migrate` binary).
