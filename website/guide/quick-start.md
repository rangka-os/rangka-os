# Quick start

Two ways to run Rangka OS: start a project with npm (no Go needed), or build the kernel from source.

## Start a project with npm

::: warning From the first release
`@rangka-os/cli` and `@rangka-os/create` are published when the first version tag is pushed. Until then, use
[Build from source](#build-from-source).
:::

You need Node.js 18+ and Docker (for Postgres), or your own Postgres 16.

```bash
npm create @rangka-os@latest my-shop   # asks for the business name, template (demo or blank) and owner email
cd my-shop
npm install          # installs the rangka binary for your platform (@rangka-os/cli)
npm run db           # Postgres 16 in Docker on port 54329
npm run setup        # validate the .rk files, create tables, load fixtures, create the owner
npm run dev          # http://localhost:8080/console/
```

The project folder holds everything: `apps/` (ready apps), `site/` (your company layer: config, theme,
layout, overlays), `connectors/`, `fixtures/` and `.env` (database URL, owner account). Change files in
`site/`, then `npm run validate`.

Without a project you can still call the CLI: `npx @rangka-os/cli version`. A Docker image is published as
`ghcr.io/rangka-os/rangka` (`docker run -v "$PWD:/srv" -p 8080:8080 ghcr.io/rangka-os/rangka serve`).

## Build from source

Run the kernel with the demo business "Kopi Senja" (a coffee shop with CRM and sales) on your machine.

### Requirements

- Go 1.24 or newer
- PostgreSQL 16 binaries (`initdb`, `postgres`) on the machine — the scripts start and remove a throwaway cluster
- Python 3 with `pyyaml` and `jsonschema`, for the reference tools (optional)

### Run the demo

Clone the repos side by side; the kernel's Makefile expects that layout.

```bash
mkdir rangka && cd rangka
git clone git@github.com:rangka-os/rangka-os.git
git clone git@github.com:rangka-os/rangka-apps.git
git clone git@github.com:rangka-os/rangka-connectors.git

cd rangka-os/kernel
make run-demo
```

`make run-demo` builds the binary, starts a temporary Postgres, migrates it from the `.rk` files, seeds the demo data
and serves on port 8080. Ctrl-C stops everything and removes the database.

| Open | What you get |
| --- | --- |
| http://localhost:8080/console/ | Rangka Console. Sign in with `ceo@saga.co.id` / `rangka-demo` |
| http://localhost:8080/prototype/ | The CRM and sales apps, rendered from metadata |
| http://localhost:8080/api/v1/meta | Every model, dashboard and setting as JSON |
| `POST http://localhost:8080/mcp` | MCP endpoint for AI clients |

## Change something

1. Open `rangka-apps/apps/crm/models/deal.rk` and add a field:
   ```yaml
     - {name: next_step, type: data, label: {id: Langkah berikutnya}, in_list: true}
   ```
2. Validate:
   ```bash
   make validate
   ```
3. Restart `make run-demo`. The field is in the table, the API and the form.

In a running installation you would send the same edit as a [change set](/concepts/change-sets) instead, and it would
apply without a restart.

## Other commands

```bash
make test             # all kernel tests against a throwaway Postgres
make validate         # rangka validate on rangka-apps + the kopi-senja site + rangka-connectors
make compare-python   # Go validator vs tools/rangka_validate.py, must be byte-identical
```

The Console also runs with no kernel at all:

```bash
cd rangka-os/web && python3 -m http.server 8000
# open http://localhost:8000/console/?demo=1
```
