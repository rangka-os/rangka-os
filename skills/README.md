# Rangka skills

Skills for AI agents working on Rangka OS, in the open [Agent Skills](https://agentskills.io) format: one folder per
skill, a `SKILL.md` with `name` and `description` frontmatter, optional `references/`. Contract:
[0035](../docs/contracts/0035-raka-skills.md).

| Skill | Use it for |
| --- | --- |
| [rangka-concepts](rangka-concepts/SKILL.md) | how apps, modules, the company layer and change sets fit together; what an agent never does |
| [rangka-changesets](rangka-changesets/SKILL.md) | proposing, validating and previewing a change, and telling the owner without claiming it is done |
| [rangka-compose](rangka-compose/SKILL.md) | finding what exists (`rangka catalogue`), explaining what a new app shares, adding or removing apps |

Apps and modules in rangka-apps ship their own skills in `<unit>/skills/` (for example `rangka-app-kasir`).

## Use them

- **Connected to a kernel over MCP** (Raka does this): `rangka_skills` lists them, `rangka_skill {name}` loads one,
  `rangka_skill_file {name, path}` reads a reference. The kernel serves these platform skills plus every app and
  module skill in its catalogue.
- **Claude Code, Codex, Cursor or another harness in a checkout:** copy the folders into the folder your harness
  reads, for example `.claude/skills/` (Claude Code) or `.agents/skills/`:

  ```bash
  cp -R skills/rangka-* .claude/skills/
  ```

  `rangka skills install`, a release zip and a Claude Code plugin come next (contract 0035, step 2).

## Write or change one

- Keep the skill next to the capability it describes, and change both in the same PR.
- `name` is kebab-case, starts with `rangka-` and equals the folder; `description` says what it does and when to use
  it, at most 1024 characters.
- Give each action as the MCP tool and as the CLI or file equivalent; never assume one harness.
- A skill never tells an agent to approve, apply or read secrets, and never contains keys or customer data.
- Run `rangka skills check skills` (here) or `rangka skills check apps modules` (rangka-apps). The kernel copies this
  folder into `kernel/internal/skills/platform/` with `make sync-schemas`; a test fails when they differ.
