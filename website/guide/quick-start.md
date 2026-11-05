# Quick start

Two ways to run Rangka OS: start a project with npm (no Go needed), or build the kernel from source.

## Start a project with npm

::: warning Pre-release
Only pre-releases exist so far (`v0.5.0-alpha.*`). They are published under the npm `next` tag, so ask for `@next`;
`@latest` still points at an older alpha. Not for production data yet.
:::

You need Node.js 18+ and Docker (for Postgres), or your own Postgres 16.

```bash
npm create @rangka-os@next my-shop   # asks for the business name, template (demo or blank) and owner email
cd my-shop
npm install          # installs the rangka binary for your platform (@rangka-os/cli)
npm run db           # Postgres 16 in Docker on port 54329
npm run setup        # validate the .rk files, create tables, load fixtures, create the owner
npm run dev          # http://localhost:8080/console/ and /app/, without sign-in (--dev-open)
```

The project folder holds everything: `modules/` and `apps/` (ready building blocks and apps), `site/` (your company
layer: config, theme, layout, overlays), `connectors/`, `fixtures/` and `.env` (database URL, owner account with a
random password). Change files in `site/`, then `npm run validate`. `npm start` serves with sign-in.

Without a project you can still call the CLI: `npx @rangka-os/cli@next version`. Binaries for macOS, Linux and
Windows are on [GitHub Releases](https://github.com/rangka-os/rangka-os/releases). Each release also pushes an image
tagged with its version, for example `ghcr.io/rangka-os/rangka:0.5.0-alpha.5` (pre-releases get no `:latest` tag).

## Build from source

Run the kernel with the demo business "Kopi Senja" (a coffee shop with CRM and sales) on your machine.

### Requirements

- Go 1.24 or newer
- PostgreSQL 16+ server binaries (`initdb`, `pg_ctl`, `postgres`) on the machine. `scripts/pgtemp.sh` finds Debian,
  Homebrew, DBngin and Postgres.app installs (else set `PGBIN`), and starts and removes a throwaway cluster
- Python 3 with `pyyaml`, `jsonschema` and `referencing`, for the reference tools and `make compare-python` (optional)

### Run the demo

Clone the repos side by side; the kernel's Makefile expects that layout.

```bash
mkdir rangka && cd rangka
git clone git@github.com:rangka-os/rangka-os.git
git clone git@github.com:rangka-os/rangka-apps.git
git clone git@github.com:rangka-os/rangka-connectors.git

cd rangka-os/kernel
make run-demo                # or another port: make run-demo ADDR=:8090
```

`make run-demo` builds the binary, starts a temporary Postgres, migrates it from the `.rk` files, seeds the demo data
and serves on port 8080 with `--dev-open` (every request acts as the owner, no sign-in). When `../rangka-ai` is built
(`npm run build` there), it also starts the scripted Raka sidecar, so the assistant answers in the app. Ctrl-C stops
everything and removes the database.

| Open | What you get |
| --- | --- |
| http://localhost:8080/app/ | The web app people use every day: dashboards, lists, forms, approvals, imports, print |
| http://localhost:8080/console/ | Rangka Console |
| http://localhost:8080/api/v1/meta | Every model, dashboard and setting as JSON |
| `POST http://localhost:8080/mcp` | MCP endpoint for AI clients |

Without `--dev-open` (a plain `rangka serve`), sign in with the seeded owner `ceo@saga.co.id` / `rangka-demo`.

## Change something

`make run-demo` serves the kernel's frozen test catalogue. To run on the real `rangka-apps` (modules + apps, the
example site `examples/sites/kopi-senja`), pass the folders:

```bash
make run-demo APPS=../../rangka-apps/apps SITE=../../rangka-apps/examples/sites/kopi-senja \
  RANGKA_FIXTURES=../../rangka-apps/examples/fixtures/kopi-senja
```

1. Open `rangka-apps/modules/pipeline/models/deal.rk` and add a field:
   ```yaml
     - {name: next_step, type: data, label: {id: Langkah berikutnya}, in_list: true}
   ```
2. Validate:
   ```bash
   make validate APPS=../../rangka-apps/apps SITE=../../rangka-apps/examples/sites/kopi-senja
   ```
3. Restart `make run-demo` with the same folders. The field is in the table, the API and the form.

In a running installation you would send the same edit as a [change set](/concepts/change-sets) instead, and it would
apply without a restart.

## Other commands

```bash
make test             # all kernel tests against a throwaway Postgres
make test-unit        # the tests that need no database
make validate         # rangka validate on the kernel's frozen test catalogue (kernel/testdata/catalogue-v0)
make compare-python   # Go validator vs tools/rangka_validate.py on many inputs, must be byte-identical
```

The Console also runs with no kernel at all:

```bash
cd rangka-os/web && python3 -m http.server 8000
# open http://localhost:8000/console/?demo=1
```
