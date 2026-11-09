# Contributing

Issues and pull requests are welcome in every repository of [github.com/rangka-os](https://github.com/rangka-os).

- **Kernel**: `make vet`, `make test`, `make validate` and `make compare-python` must pass (CI runs vet, test and
  compare-python on every push). A change to `.rk` semantics lands in both validators and the schema in the same pull
  request; a change that crosses repositories starts as a proposal in `docs/contracts/NNNN-title.md` (see
  `0000-how-contracts-change.md`).
- **Apps**: keep `rangka_validate.py` at 0 errors and add fixtures for new models.
- **Connectors**: link the vendor's API docs, mark every secret, give each trigger an idempotency key.
- **Rangka AI**: keep `npm test` green offline; any change that widens what the agent may write needs a test showing
  the escalation cases still hold.
- **Docs**: this site lives in `rangka-os/website`; schema pages and validator codes are generated
  (`npm run gen`), so edit the schema or the validator, not the page.

UI copy is Bahasa Indonesia; code, comments and docs are English.

Licence: Elastic License 2.0 (ELv2), see `LICENSE`. Rangka OS is source-available, not open source. Contributions need a contributor agreement with Saga Tekno
Studio (to be published).
