# 0005: Pilot readiness

Status: accepted (1 Nov 2026) · Owner: rangka-os · Must be done before the first pilot business uses real data

## 1. Replace in-tree stand-ins
Use the real pgx, chi, cel-go, River, jsonschema, yaml.v3 and bcrypt, each behind its existing package boundary. All current tests must stay green. One PR per library.

## 2. Backup and restore
- `rangka backup [--out file]` writes a single archive containing:
  - a Postgres dump of the site's data (per org when `isolation: shared`)
  - the files storage
  - `site/` and `custom/`
  - `rangka.lock`
  - a manifest with versions and checksums
- `rangka restore <file>` restores into an empty database and checks the versions against the binary.
- Scheduled backups go in `system.rk` (`backup: {schedule: "0 2 * * *", keep: 14, driver: local|s3}`).
- Restore is tested in CI: seed demo → backup → restore → compare record counts and checksums.

## 3. Safe migrations
- `rangka migrate --dry-run` prints the SQL plan and its risk level, and changes nothing.
- Every migration runs in a transaction and records its reverse step. `rangka migrate --rollback [n]` undoes the last n.
- Destructive steps (dropping a column or table, narrowing a type) are refused unless `--allow-destructive` is given. Soft-delete columns for 30 days, as planned.
- An automatic backup is taken before any migration in production mode.

## 4. Enforce `permissions:`
- Enforce model `permissions:` (read, create, update, submit, cancel, delete, print, export) per role in REST, MCP, realtime and the Console.
  - A refusal returns 403 `permission_denied`, with the model and the action.
- Row scope for company and branch (outlet) through Postgres RLS on shared tables.
- Tests: a permission matrix per role over the demo site. MCP tokens (Omilo helpers) get the same checks as people.

## Gate
A pilot install on a fresh machine can be backed up, restored elsewhere, upgraded and rolled back, and a `sales_rep` cannot read another branch's invoices through any interface.
