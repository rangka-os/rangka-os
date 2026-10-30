# CLI

One binary, `rangka`, built from `rangka-os/kernel` (`make build` → `kernel/bin/rangka`).

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
| `seed` | Creates the organisation (named after the site folder), loads `fixtures/*.rk`, and the owner `ceo@saga.co.id` / `rangka-demo`. |
| `serve` | REST, SSE, queue workers, scheduler, Rangka ID, change sets and MCP on one port; serves `rangka-os/web` at `/` (`/console/`, `/prototype/`). Refuses metadata with errors. `--dev-open` turns off auth for local demos. |

Makefile shortcuts in `kernel/`: `make build`, `make test`, `make validate`, `make compare-python`, `make run-demo`,
`make rename-module MODULE=…`.
