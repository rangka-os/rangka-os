# Contributing to Rangka OS

Issues and pull requests are welcome. This file covers this repository (kernel, schemas, validators, web app,
Console, design system, docs). The [contributing guide](https://rangka-os.github.io/rangka-os/contributing) covers the
other repositories of the family.

By taking part you agree to follow the [code of conduct](CODE_OF_CONDUCT.md). Report security problems privately, as
described in [SECURITY.md](SECURITY.md), never in a public issue.

## Before you start

- **Bugs**: search the [issues](https://github.com/rangka-os/rangka-os/issues) first, then open one with the bug
  template. A `.rk` file, the validator output or the exact steps that reproduce it help most.
- **Features**: open an issue before writing a large change, so we can agree on the shape first.
- **Changes that cross repositories** (a `.rk` format, an API another repository reads, a connector port) start as a
  contract proposal in [`docs/contracts/`](docs/contracts) (`NNNN-title.md`). Read
  [0000-how-contracts-change.md](docs/contracts/0000-how-contracts-change.md) first.

## Set up

Needs Go 1.24+, PostgreSQL 16 server binaries and Python 3.12 with `jsonschema referencing pyyaml`. The tests read
`rangka-apps` and `rangka-connectors` next to this repository, so clone them side by side:

```bash
mkdir rangka && cd rangka
git clone git@github.com:rangka-os/rangka-os.git
git clone git@github.com:rangka-os/rangka-apps.git
git clone git@github.com:rangka-os/rangka-connectors.git

cd rangka-os/kernel
make run-demo          # throwaway Postgres → migrate → seed → serve on :8080
```

`scripts/pgtemp.sh` finds Homebrew, DBngin, Postgres.app and Debian installs of PostgreSQL; otherwise set `PGBIN`.

## Checks

From `kernel/`, these must pass before you open a pull request (CI runs the same):

```bash
make vet
make test             # every package against a throwaway Postgres
make validate         # the catalogue, sites and connectors
make compare-python   # the Go and Python validators print identical reports
```

Changes to the web app or Console also run their suites (Playwright with Chromium):

```bash
python web/app/test_app.py --require-all
python web/console/test_console.py --react-dir web/app/vendor
```

## Rules

- A change to `.rk` semantics lands in **both validators** (`kernel/` and `tools/rangka_validate.py`) and in the
  schema under `schema/` in the same pull request, with a contract update when it affects other repositories.
- Schema pages and validator codes on the docs site are generated (`npm run gen` in `website/`): edit the schema or
  the validator, not the page.
- Money is integer rupiah, never floats. Money and stock never depend on an LLM.
- Code, comments, commit messages and docs are English. UI text is Bahasa Indonesia first: sentence case, "kamu",
  `Rp 1.250.000`, `27 Okt 2026`.
- Every screen works from phone to desktop ([responsive coverage](docs/responsive-coverage.md)) and stays usable by
  keyboard and screen reader ([ACCESSIBILITY.md](ACCESSIBILITY.md)).

## Pull requests

- Branch from `main`; keep one topic per pull request.
- Commit subjects name the area first, as in the history: `kernel: …`, `web/app: …`, `Contract 0030: …`.
- Fill in the pull request template: what changed, why, how you tested it, and the contract it touches if any.
- Keep CI green. A maintainer reviews and merges.

## Licence

Rangka OS is source-available under the [Elastic License 2.0](LICENSE). Contributions need a contributor agreement
with Saga Tekno Studio (to be published); a maintainer will point you to it before merging your first pull request.
