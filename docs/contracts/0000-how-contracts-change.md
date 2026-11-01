# 0000: How contracts change

Status: proposed (1 Nov 2026)

## Context
Two engineers, each working with several AI sessions a day, across four repos cloned side by side.
Most conflicts come from the shared boundary, not from each person's own code:
`schema/*.json`, the validator, `contracts.md`, committed generated files, and AI editing a sibling repo.

## Decision
1. **Ownership.**
   - `rangka-os`, `rangka-edge` and `rangka-cloud`: owned by Dede (kernel, schema, validator, Console, outlet agent, cloud).
   - `rangka-apps` and `rangka-connectors`: owned by the second engineer (modules, apps, connectors).
   - `rangka-ai`: deprioritised; no changes until picked up again.
   - Licence: every repo except `rangka-cloud` is Elastic License 2.0 (ELv2), copyright Saga Tekno Studio.
   - Each repo has a `CODEOWNERS`. A PR touching another owner's repo needs that owner's review.
2. **One file per decision.** Contract changes are new files in `docs/contracts/NNNN-title.md`
   (status: proposed → accepted → implemented). `contracts.md` stays as the v0 baseline and is not edited further.
   New files never conflict.
3. **Contracts move through releases, not `main`.**
   - `rangka-os` tags releases (`v0.4.1`, `v0.5.0`) when a contract change is implemented, at least weekly while active.
   - `rangka-apps` and `rangka-connectors` pin a released `rangka-os` tag and build against it, never against `main`.
4. **Need a new key? Propose it, use `experimental:` meanwhile.**
   - The module/app/connector owner writes a proposal file here.
   - Until it is released, the key can live under an `experimental:` block, which the validator reports as a warning, not an error.
   - After release, move it to the real key.
5. **CI in both directions.**
   - `rangka-apps` and `rangka-connectors` CI: run the validator and the `make run-demo` smoke test from the pinned `rangka-os` tag.
   - `rangka-os` CI: run contract tests against `rangka-apps@main`, so kernel changes that would break apps show up before release.
6. **Do not commit generated files.** CI builds `rangka-ai/knowledge/*`, `web/design-system/bundle.*` and README tables.
7. **Guardrails for AI.** Every repo has a `CLAUDE.md` / `AGENTS.md` that states:
   - who owns the repo
   - never to edit files outside this repo
   - to write a proposal in `rangka-os/docs/contracts/` when a contract change is needed
8. **Inside a repo:** one AI task = one branch = one git worktree. Small PRs, merged daily. Avoid central list files that everyone edits.

## Consequences
The two engineers meet in two places only: proposal files and `rangka-os` releases. Everything else runs in parallel.
