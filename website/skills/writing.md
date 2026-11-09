# Write a skill

A skill belongs next to the capability it describes: a platform skill in rangka-os `skills/`, an app's or module's
in its folder's `skills/` in rangka-apps, a connector's in rangka-connectors `skills/`. Change the skill in the same
pull request as the capability. A capability that changes without its skill fails review.

## Shape

```text
skills/rangka-app-kasir/
  SKILL.md        # frontmatter + instructions
  references/     # optional: longer material the agent reads on demand
```

```markdown
---
name: rangka-app-kasir
description: "Rangka Kasir (app kasir): … Use when an owner asks for a cashier or POS …"
metadata: {rangka: {version: 1.0.0, app: kasir, requires: [rangka-compose, rangka-changesets]}}
---

# Rangka Kasir

Instructions in plain English …
```

- **`name`:** kebab-case, starts with `rangka-`, and equals the folder name.
- **`description`:** what the skill does and when to use it, at most 1024 characters. The model picks a skill from
  this line alone, so name the owner's own words ("mau pakai kasir", "tambahin POS").
- **Rangka keys** go only under `metadata.rangka`, so every harness still parses the file:
  - `version`: the release that ships it;
  - `requires`: other skills or kernel features;
  - `writes`: the layers its actions may change.

## Rules

- Give each action two ways: the MCP tool (`rangka_get_file`, `rangka_changeset_propose`, …) and its CLI or file
  equivalent. Never assume one harness's tool names.
- Read the catalogue before naming anything: `rangka_catalogue` / `rangka catalogue --json`.
- Never tell an agent to approve, apply, revert or read secrets, and never write keys or customer data into a skill.
- Tell the agent never to say something is done. It says what it proposed and what the owner does next.
- Say what does not exist. An agent that knows a capability is missing tells the owner; one that does not will invent
  it.
- Keep examples on made-up businesses.

## Check it

```bash
rangka skills check skills          # rangka-os, rangka-connectors
rangka skills check apps modules    # rangka-apps
```

The check runs in each repository's CI and refuses:

| Code | Problem |
| --- | --- |
| `skill_name_folder` | `name` differs from the folder |
| `skill_name` | not kebab-case or not `rangka-…` |
| `skill_description` | empty or over 1024 characters |
| `skill_body` | no instructions after the frontmatter |
| `skill_link` | a link to a file the skill does not have |
| `skill_unknown_tool` | names a `rangka_…` MCP tool this kernel does not serve |
| `skill_forbidden` | tells an agent to approve, apply or read secrets |
| `skill_secret` | contains something that looks like a key or token |
| `skill_duplicate` | two skills with the same name |

The release zip is packed with `rangka skills pack`, which refuses to pack while any skill fails the check.
