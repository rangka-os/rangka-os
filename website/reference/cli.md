# CLI

One binary, `rangka`. Install it with npm (`npm i -D @rangka-os/cli`, or `npx @rangka-os/cli`), download it from
GitHub Releases, run the `ghcr.io/rangka-os/rangka` image, or build it from `rangka-os/kernel` (`make build`).
Release binaries carry the Console, design system and theme/layout templates inside; a source build reads them
from the workspace.

```text
rangka validate --apps <dir> [--site <dir>] [--connectors <dir>] [--json] [app ...]
rangka migrate  --apps <dir> [--site <dir>] --db <url> [--dry-run] [--allow-destructive] [--rollback n] [--files-dir <dir>]
rangka backup   --db <url> [--apps <dir>] [--site <dir>] [--out <file>] [--org <id>] [--files-dir <dir>]
rangka restore  <file> --db <url> [--into <dir>] [--files <dir>]
rangka seed     --apps <dir> [--site <dir>] --db <url> [--fixtures <dir>] [--org kopi-senja]
rangka serve    --apps <dir> [--site <dir>] --db <url> [--addr :8080] [--web <dir>] [--templates <dir>]
                [--connectors <dir>] [--deploy-target dev|docker|helm|desktop|cloud|partner] [--dev-open]
```

| Command | Does |
| --- | --- |
| `validate` | Loads every `.rk`, checks syntax, schema and meaning, prints `file:line:col  level  code  path  message`, exits 1 on errors. Output is byte-identical to `tools/rangka_validate.py`. |
| `migrate` | Compiles models (with overlays) into Postgres DDL: a table per model, child tables, indexes on links, docstatus and unique fields. Idempotent and diff-based. `--dry-run` prints each statement with its risk and changes nothing. `--rollback n` undoes the last n migrations. Destructive steps need `--allow-destructive`. See [Migrations](#migrations). |
| `backup` | Writes one verified `tar.gz`: every table, `site/`, `apps/` and the uploads directory, with checksums. See [Backup and restore](#backup-and-restore). |
| `restore` | Rebuilds an archive into an empty database and verifies it against the archive's checksums. |
| `seed` | Creates the organisation (`--org`, `$RANGKA_ORG`, else the site folder name), loads `fixtures/*.rk`, and the owner account (`--email`/`--password`, `$RANGKA_OWNER_EMAIL`/`$RANGKA_OWNER_PASSWORD`; demo default `ceo@saga.co.id` / `rangka-demo`). |
| `serve` | REST, SSE, queue workers, scheduler, Rangka ID, change sets and MCP on one port; serves `rangka-os/web` at `/` (`/app/`, `/console/`; `/` redirects to `/app/`). Refuses metadata with errors. `--dev-open` turns off auth for local demos. |

## Migrations

`rangka migrate` compares the models with the database and applies only the difference, in one transaction.
Every step records its reverse.

- `--dry-run` prints each statement as `SQL;  -- low|medium|high` and a summary line with the highest risk. It runs
  nothing, even on an empty database.
- **Destructive steps** are retyping a column to a type that is not wider, soft-deleting a column or table the model
  no longer has, and dropping a soft-deleted column or table older than 30 days. A plan that contains a retype is
  refused as a whole (exit 1, nothing applied) unless you pass `--allow-destructive`. Columns and tables removed from a
  model stay in the database and show up as notes.
- **Soft delete** renames the column or table to `_del_<YYYYMMDD>_<name>` (63 bytes at most), and its indexes the
  same way, so a column or table that comes back gets fresh indexes (a returning `unique: true` field is unique
  again). Rolling it back renames both back. It is dropped for good only after 30 days and only with
  `--allow-destructive`.
- `--rollback n` undoes the last n migrations (n of at least 1). A rollback whose reverse drops a column or table is
  destructive too and needs `--allow-destructive`. It supports `--dry-run`.
- `serve` migrates at start-up and refuses to start on a destructive retype, with a message that points to
  `rangka migrate --allow-destructive`.
- **Change sets never apply destructive DDL.** Validate and preview report such a step as an error that names
  `rangka migrate --allow-destructive`; an operator runs it.

## Backup and restore

```text
rangka backup  --db <url> --apps <dir> --site <dir> [--out rangka-all-20261103-020000.tar.gz] [--org <id>] [--files-dir <dir>]
rangka restore rangka-all-20261103-020000.tar.gz --db <url-of-an-empty-database> [--into restore-dir] [--files <dir>]
```

- The archive holds a manifest (kernel version, row counts, SHA-256 per table and per file), every table in schema
  `public` as `COPY` text, `site/` (without `data/` and `backups/`), `apps/` and the uploads directory.
- Backup is written in Go through the database connection (`COPY ... TO STDOUT`), so the single binary needs no
  Postgres client tools. All tables are read from one snapshot. The file is written as `<out>.partial` and renamed
  when complete. Every archive (also the pre-migrate ones) is created with mode `0600`: it holds password, token and
  session hashes and the OIDC signing key.
- `--org` archives one organisation: rows with that `org_id`, its members' users, and only its own area of the
  uploads directory (`files/<org>/`). Tables without an org link are left out and listed in the manifest's
  `excluded` field, except `rk_meta_files` and `rk_migrations`. Today that is `rk_keys`, so a restored org archive
  makes a fresh OIDC signing key on first use (OIDC clients fetch the new JWKS). Rows of the installation itself
  (`org_id` empty) are left out as well, so schedule whole-database backups for disaster recovery. Without `--org` the
  whole database is archived.
- `--files-dir` (`--files` also works for backup and restore) resolves like `serve`: `$RANGKA_FILES_DIR`, else `files.path`
  of `system.rk`, else `./data/files`. Relative paths are relative to the working directory; set `RANGKA_FILES_DIR` in
  deployments. A symlinked file inside the tree is copied as its content; a symlinked directory inside the tree is an
  error that names it.
- Backup fails when an attachment row in scope (`rk_attachments`) has no blob in the uploads directory, also when the
  directory does not exist. Run it where `serve` runs or point `--files-dir` / `RANGKA_FILES_DIR` at the same directory.
- `restore` needs an empty database. It migrates from the archived `apps/` and `site/`, adds archived columns the
  models no longer have, loads the tables in foreign-key order with `COPY ... FROM STDIN`, resets sequences, then
  recomputes the checksums and compares them with the manifest. It also checks that every restored attachment row
  has its blob. It writes `site/` and `apps/` into `--into` (default `./restore-<archive name>`) and the uploads into
  `--files` (directories `0750`, files `0640`, like the files store).
- An archive written by a newer kernel than the running binary is refused. A corrupt archive (bad checksum, unsafe
  entry, unexpected column type) is refused before anything is written.
- If a restore fails after the migration step, the schema is left behind. Retry into a fresh database.
- CI runs seed, backup, restore and a comparison of every table's checksums on each push (`.github/workflows/kernel.yml`).

## Production mode

Set `RANGKA_ENV=production` on a live installation. Then `rangka migrate`, `rangka migrate --rollback`, the
migration that `serve` runs at start-up and every change-set apply first write a whole-database backup:

```text
<RANGKA_BACKUP_DIR, default <site>/backups>/rangka-all-<YYYYMMDD-HHMMSS>-pre-migrate.tar.gz
```

Without `--site` the default is `./backups` in the working directory. The backup archives the uploads directory that
`serve` uses: pass `rangka migrate` the same `--files-dir` (or `RANGKA_FILES_DIR`) as `serve`, or the backup fails
when attachments exist.

The backup is taken only when there is something to apply, before the first statement. If it fails, the migration
does not run. Archives are never overwritten and never deleted by the kernel: clear old ones yourself, and copy
them off the machine. `--dry-run` takes no backup.

Scheduled backups (a `backup:` key in `system.rk`) are not built yet. Until then run `rangka backup` from cron or a
systemd timer.

## Permissions

Model `permissions:` are enforced in REST, MCP, SSE, print, attachments and the flows people start. Actions are the
schema keys: `read`, `create`, `write`, `delete`, `submit`, `cancel`, `amend`, `export`, `import`, `print`,
`customize`.

An actor is allowed an action on a model when any of these holds:

1. It is the kernel itself (seed, scheduled flows, automations, queue workers acting for the kernel).
2. It has the role `owner`.
3. The model has no `permissions:` list (models not yet annotated behave as before).
4. An entry whose `role` is one of the actor's roles has `<action>: true` and, when it has `if:`, the CEL expression
   is `true` on the record. A CEL error counts as false. Grants from several roles are added together.

With a record, `if:` runs on the stored record (read, write, delete, submit, cancel, amend, print) or on the new
record after defaults (create). `write` is checked on the stored record and on the record after the patch. Without a
record (a list, an SSE subscription, meta) a conditional grant counts as "maybe": lists drop the rows whose `if:` is
false and do not count them in `total`, and SSE checks each event's record.

A denied single record answers 403, not 404. See [REST API](/reference/rest-api#permissions). API tokens, MCP and
Omilo carry the member's roles and are checked exactly like a person. Child models use their parent's permissions.

## Settings from the environment

Every command reads a `.env` file in the current directory first (variables already set win), so a project
made by `npm create @rangka-os` needs no flags:

| Variable | Flag |
| --- | --- |
| `DATABASE_URL` | `--db` |
| `RANGKA_APPS`, `RANGKA_SITE`, `RANGKA_CONNECTORS`, `RANGKA_FIXTURES` | `--apps`, `--site`, `--connectors`, `--fixtures` |
| `RANGKA_ORG` | `--org` (seed, serve) |
| `RANGKA_FILES_DIR` | `--files-dir` (serve, migrate, backup), `--files` (restore) |
| `RANGKA_ENV` | `production` turns on the backup before migrations |
| `RANGKA_BACKUP_DIR` | where those backups go (default `<site>/backups`) |
| `RANGKA_ADDR` | `--addr` |
| `RANGKA_OWNER_EMAIL`, `RANGKA_OWNER_PASSWORD` | `--email`, `--password` (seed) |
| `RANGKA_WEB`, `RANGKA_TEMPLATES` | `--web`, `--templates` |

Makefile shortcuts in `kernel/`: `make build`, `make test`, `make validate`, `make compare-python`, `make run-demo`,
`make rename-module MODULE=…`.
