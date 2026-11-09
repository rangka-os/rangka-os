# Install the skills

Pick the way that fits your harness. Each way gives you the same `SKILL.md` folders.

## With the Rangka CLI (any harness)

In a Rangka project (one made with `npm create @rangka-os@latest`, or a checkout of rangka-apps), from its folder:

```bash
npx @rangka-os/cli skills install
```

This copies the platform skills (built into the binary) plus every app, module and connector skill it finds into
`.claude/skills/`. Options:

| Option | Installs into |
| --- | --- |
| *(none)* or `--harness claude` | `.claude/skills/` (Claude Code reads it per project) |
| `--harness agents` | `.agents/skills/` (harnesses that read the shared agents folder) |
| `--to <dir>` | any folder, for example `~/.claude/skills` for every project, or your harness's skills folder |
| `--apps`, `--modules`, `--connectors` | where to look for app, module and connector skills (defaults: `$RANGKA_APPS` or `apps/`, the `modules/` next to it, `$RANGKA_CONNECTORS` or a `rangka-connectors/` checkout found next to the apps' repository) |

A folder named like a Rangka skill (`rangka-…`) is replaced on every install, so running it again updates them.
Nothing else in the folder is touched. `rangka skills list` shows what it would install.

## Claude Code plugin

The rangka-os repository is a Claude Code plugin marketplace. In Claude Code:

```text
/plugin marketplace add rangka-os/rangka-os
/plugin install rangka-skills@rangka-os
```

The plugin carries the platform skills. App, module and connector skills come with the CLI or the zip below.

## Release zip (no CLI)

Every release attaches `rangka-skills-<version>.zip` to its
[GitHub release](https://github.com/rangka-os/rangka-os/releases). It holds every skill, platform, app, module and
connector, with each skill's folder at the top of the zip:

```bash
unzip rangka-skills-<version>.zip -d .claude/skills
```

Use the folder your harness reads instead of `.claude/skills` when it differs.

## Per harness

| Harness | Where skills go | How |
| --- | --- | --- |
| Claude Code | `.claude/skills/` (project) or `~/.claude/skills/` (you) | `rangka skills install`, or the plugin |
| Claude API and Claude apps | upload each skill folder as a skill | the release zip holds one folder per skill |
| Codex, Cursor and other Agent Skills harnesses | the skills folder the harness documents, often `.agents/skills/` | `rangka skills install --harness agents` or `--to <dir>` |
| An MCP client connected to a kernel | nothing to install | `rangka_skills`, `rangka_skill` (see [Skills](./)) |
| Anything else | as plain instructions | give the agent `SKILL.md` files from the zip |

Raka needs none of this: it reads the skills from the kernel it works on.
