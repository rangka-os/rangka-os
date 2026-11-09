# Rangka skills

Skills for AI agents working on Rangka OS, in the open [Agent Skills](https://agentskills.io) format: one folder per
skill, a `SKILL.md` with `name` and `description` frontmatter, optional `references/`. Contract:
[0035](../docs/contracts/0035-raka-skills.md).

| Skill | Use it for |
| --- | --- |
| [rangka-concepts](rangka-concepts/SKILL.md) | how apps, modules, the company layer and change sets fit together; what an agent never does |
| [rangka-changesets](rangka-changesets/SKILL.md) | proposing, validating and previewing a change, and telling the owner without claiming it is done |
| [rangka-compose](rangka-compose/SKILL.md) | finding what exists (`rangka catalogue`), explaining what a new app shares, adding or removing apps |
| [rangka-consult](rangka-consult/SKILL.md) | the studio interview as a consultant: peta alur, files as guesses, composing from every piece kind |

Apps and modules in rangka-apps ship their own skills in `<unit>/skills/` (for example `rangka-app-kasir`).

## Use them

- **Any harness, with the CLI:** `npx @rangka-os/cli skills install` copies these plus every app, module and
  connector skill into `.claude/skills/` (`--harness agents` for `.agents/skills/`, or `--to <dir>`).
- **Claude Code plugin:** `/plugin marketplace add rangka-os/rangka-os`, then `/plugin install rangka-skills@rangka-os`
  (platform skills; the manifests are in `.claude-plugin/`).
- **Release zip:** each release attaches `rangka-skills-<version>.zip` (`unzip -d .claude/skills`).
- **Connected to a kernel over MCP** (Raka does this): `rangka_skills`, `rangka_skill {name}`,
  `rangka_skill_file {name, path}`.

Per-harness details: [docs, Skills → Install](https://rangka-os.github.io/rangka-os/skills/install).

## Write or change one

- Keep the skill next to the capability it describes, and change both in the same PR.
- `name` is kebab-case, starts with `rangka-` and equals the folder; `description` says what it does and when to use
  it, at most 1024 characters.
- Give each action as the MCP tool and as the CLI or file equivalent; never assume one harness.
- A skill never tells an agent to approve, apply or read secrets, and never contains keys or customer data.
- Run `rangka skills check skills` (here) or `rangka skills check apps modules` (rangka-apps). The kernel copies this
  folder into `kernel/internal/skills/platform/` with `make sync-schemas`; a test fails when they differ.
