# Skills for AI agents

Rangka skills teach an AI agent how Rangka OS works and how to change a Rangka site safely. They are written in the
open [Agent Skills](https://agentskills.io) format: one folder per skill with a `SKILL.md` (frontmatter `name` and
`description`, then instructions) and optional reference files. The model reads only the names and descriptions until
a task needs a skill, then loads its instructions.

The same library serves every harness:

- **Raka**, the agent in Juragan and in the app, loads them from the running kernel.
- **Claude Code, Codex, Cursor and other harnesses** install them into the folder they read (see
  [Install](./install)).

Contract: [0035 Rangka skills](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0035-raka-skills.md).

## The skills

<!--@include: ./_list.md-->

Platform skills live in [`rangka-os/skills`](https://github.com/rangka-os/rangka-os/tree/main/skills). App and module
skills live next to their app or module in rangka-apps (`<unit>/skills/`), and connector skills in
rangka-connectors (`skills/`). Each skill changes in the same pull request as the capability it describes.

## What every skill relies on

- **The live catalogue.** `rangka catalogue --json` (or the MCP tool `rangka_catalogue`) lists every app, module and
  connector with its state for the site (`installed`, `available`, `not_in_plan`). For each app it also gives the
  installed modules it `shares` and the modules it `adds`. Skills tell agents to read it first and never to name an
  app from memory.
- **Change sets.** Every change to a site is a change set that a person approves. Skills never tell an agent to
  approve, apply or read secrets, and `rangka skills check` refuses one that does.
- **Proof before claim.** An agent says what it proposed, never that something is done. The app shows completion from
  the change set's status.

## Use them with a connected kernel (MCP)

An MCP client connected to a kernel (`POST /mcp`) gets the skills that match that kernel's version and catalogue
without installing anything:

| Tool | Returns |
| --- | --- |
| `rangka_skills` | name, description and source of every skill |
| `rangka_skill {name}` | one skill's instructions and its other files |
| `rangka_skill_file {name, path}` | one reference file |
| `rangka_catalogue` | the catalogue above |

See [MCP tools](/reference/mcp).
