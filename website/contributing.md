# Contributing

Issues and pull requests are welcome in every repository of [github.com/rangka-os](https://github.com/rangka-os).

- **Kernel**: `make test`, `make validate` and `make compare-python` must pass. A change to `.rk` semantics lands in
  both validators and in `docs/contracts.md` in the same pull request.
- **Apps**: keep `rangka_validate.py` at 0 errors and add fixtures for new models.
- **Connectors**: link the vendor's API docs, mark every secret, give each trigger an idempotency key.
- **Rangka AI**: keep `npm test` green offline; any change that widens what the agent may write needs a test showing
  the escalation cases still hold.
- **Docs**: this site lives in `rangka-os/website`; schema pages are generated, so edit the schema, not the page.

UI copy is Bahasa Indonesia; code, comments and docs are English.

Licence: intended AGPL-3.0 with an exception for apps and templates, plus a contributor licence agreement. The
`LICENSE` file is not committed yet.
