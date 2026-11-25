# Rangka skills

Skills for AI agents working on Rangka OS, in the open [Agent Skills](https://agentskills.io) format: one folder per
skill, a `SKILL.md` with `name` and `description` frontmatter, optional `references/`. Contract:
[0035](../docs/contracts/0035-raka-skills.md).

| Skill | Use it for |
| --- | --- |
| [rangka-concepts](rangka-concepts/SKILL.md) | how apps, modules, the company layer and change sets fit together; what an agent never does |
| [rangka-changesets](rangka-changesets/SKILL.md) | proposing, validating and previewing a change, and telling the owner without claiming it is done |
| [rangka-compose](rangka-compose/SKILL.md) | finding what exists (`rangka catalogue`), explaining what a new app shares, adding or removing apps |
| [rangka-connectors](rangka-connectors/SKILL.md) | drafting a company connector (`x_`) for a system the catalogue lacks, the poll + upsert pattern, its test states and what to tell the owner |
| [rangka-themes](rangka-themes/SKILL.md) | changing how a business looks: bundled themes, one colour of the owner's, button, neutral, font, locked tokens (contract 0067) |
| [rangka-consult](rangka-consult/SKILL.md) | the studio interview as a consultant: peta alur, files as guesses, composing from every piece kind; domain packs per business type in `references/domains/` (contract 0071) |
| [rangka-modelling](rangka-modelling/SKILL.md) | designing data in `.rk` like a DBA: reuse master data, overlay vs company model, field types, links, child tables, document lifecycle |
| [rangka-flows](rangka-flows/SKILL.md) | automation: the lightest tool (rule, approval policy, flow), CEL, retries and idempotency, notifications that really deliver |
| [rangka-imports](rangka-imports/SKILL.md) | bringing in an owner's spreadsheets and exports: profiling, column mapping, opening balances, explaining preview errors |
| [rangka-dashboards](rangka-dashboards/SKILL.md) | answering "how is the business doing" and designing widgets owners act on: KPIs per business type, correct sums |
| [rangka-review](rangka-review/SKILL.md) | reviewing your own draft change set or studio offer before the owner sees it |
| [rangka-compliance-id](rangka-compliance-id/SKILL.md) | explaining and designing around PPN, PPh, BPJS, THR and UU PDP; never computing tax |

Each skill may carry `evals/*.yaml` (contract 0071 §4); rangka-ai runs them with `python -m raka.evals`.

Apps and modules in rangka-apps ship their own skills in `<unit>/skills/` (for example `rangka-app-kasir`).

## Use them

- **Any harness, with the CLI:** `npx @rangka-os/cli skills install` copies these plus every app, module and
  connector skill into `.claude/skills/` (`--harness agents` for `.agents/skills/`, or `--to <dir>`).
- **Claude Code plugin:** `/plugin marketplace add rangka-os/rangka-os`, then `/plugin install rangka-skills@rangka-os`
  (platform skills; the manifests are in `.claude-plugin/`).
- **Release zip:** each release attaches `rangka-skills-<version>.zip` (`unzip -d .claude/skills`).
- **Connected to a kernel over MCP** (Zafdoz does this): `rangka_skills`, `rangka_skill {name}`,
  `rangka_skill_file {name, path}`.

Per-harness details: [docs, Skills → Install](https://rangka-os.github.io/rangka-os/skills/install).

## Write or change one

- Keep the skill next to the capability it describes, and change both in the same PR.
- `name` is kebab-case, starts with `rangka-` and equals the folder; `description` says what it does and when to use
  it, at most 1024 characters.
- Give each action as the MCP tool and as the CLI or file equivalent; never assume one harness.
- Skills describe how, never whether: a ban belongs in the kernel (`ai_forbidden_path`, validator codes), and a skill
  only names it.
- A skill never tells an agent to approve, apply or read secrets, and never contains keys or customer data.
- App and module skills (rangka-apps) carry an **Offer** section (contract 0046 §6): what the app or module records,
  its work screens, what Zafdoz can automate with it, what needs the owner's approval, and when it fits or does not.
  `rangka-consult` fills the offer card from it.
- Run `rangka skills check skills` (here) or `rangka skills check apps modules` (rangka-apps). The kernel copies this
  folder into `kernel/internal/skills/platform/` with `make sync-schemas`; a test fails when they differ.
