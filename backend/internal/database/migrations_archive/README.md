# Retired step migrations (pre-squash)

These files are the historical golang-migrate step chain (`000001`–`000061`) that
was squashed into `migrations/000001_init.{up,down}.sql` for formal testing.

They are **not** embedded into the application binary and are **not** applied by
`cmd/migrate`. Keep them only as a reference for schema archaeology and review.

See `MANIFEST.txt` for the ordered list of retired steps.
