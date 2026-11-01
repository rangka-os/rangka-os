# CLI

One binary, `rangka`. Install it with npm (`npm i -D @rangka-os/cli`, or `npx @rangka-os/cli`), download it from
GitHub Releases, run the `ghcr.io/rangka-os/rangka` image, or build it from `rangka-os/kernel` (`make build`).
Release binaries carry the Console, design system and theme/layout templates inside; a source build reads them
from the workspace.

```text
rangka validate --apps <dir> [--site <dir>] [--connectors <dir>] [--json] [app ...]
rangka migrate  --apps <dir> [--site <dir>] --db <url> [--dry-run]
rangka seed     --apps <dir> [--site <dir>] --db <url> [--fixtures <dir>] [--org kopi-senja]
rangka serve    --apps <dir> [--site <dir>] --db <url> [--addr :8080] [--web <dir>] [--templates <dir>]
                [--connectors <dir>] [--deploy-target dev|docker|helm|desktop|cloud|partner] [--dev-open]
```

| Command | Does |
| --- | --- |
| `validate` | Loads every `.rk`, checks syntax, schema and meaning, prints `file:line:col  level  code  path  message`, exits 1 on errors. Output is byte-identical to `tools/rangka_validate.py`. |
| `migrate` | Compiles models (with overlays) into Postgres DDL: a table per model, child tables, indexes on links, docstatus and unique fields. Idempotent and diff-based; never drops. `--dry-run` prints the SQL. |
| `seed` | Creates the organisation (`--org`, `$RANGKA_ORG`, else the site folder name), loads `fixtures/*.rk`, and the owner account (`--email`/`--password`, `$RANGKA_OWNER_EMAIL`/`$RANGKA_OWNER_PASSWORD`; demo default `ceo@saga.co.id` / `rangka-demo`). |
| `serve` | REST, SSE, queue workers, scheduler, Rangka ID, change sets and MCP on one port; serves `rangka-os/web` at `/` (`/console/`, `/prototype/`). Refuses metadata with errors. `--dev-open` turns off auth for local demos. |

## Settings from the environment

Every command reads a `.env` file in the current directory first (variables already set win), so a project
made by `npm create @rangka-os` needs no flags:

| Variable | Flag |
| --- | --- |
| `DATABASE_URL` | `--db` |
| `RANGKA_APPS`, `RANGKA_SITE`, `RANGKA_CONNECTORS`, `RANGKA_FIXTURES` | `--apps`, `--site`, `--connectors`, `--fixtures` |
| `RANGKA_ORG` | `--org` (seed, serve) |
| `RANGKA_ADDR` | `--addr` |
| `RANGKA_OWNER_EMAIL`, `RANGKA_OWNER_PASSWORD` | `--email`, `--password` (seed) |
| `RANGKA_WEB`, `RANGKA_TEMPLATES` | `--web`, `--templates` |

Makefile shortcuts in `kernel/`: `make build`, `make test`, `make validate`, `make compare-python`, `make run-demo`,
`make rename-module MODULE=…`.
