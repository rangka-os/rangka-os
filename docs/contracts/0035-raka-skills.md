# 0035 Rangka skills: one Agent Skills library for Raka and any other harness

Status: proposed (8 Nov 2026) · Owner of the change: rangka-os (skills, CLI, kernel tools, docs site) · Affects:
rangka-ai (loads skills instead of a big knowledge pack), rangka-apps and rangka-connectors (ship skills for their
apps and connectors), rangka-edge (one skill), rangka-cloud (private interview skills, not published) · Builds on:
0000, 0020, 0030, 0032, 0033

## Context

- **The Mogu case (8 Nov 2026).** Dede onboarded "Mogu", an F&B business: kimbab and es krim gabin, sold at an outlet
  and through GoFood, GrabFood and ShopeeFood, all manual today. Raka recommended Rangka F&B Cabang. Dede then asked
  for a POS. Raka said it had made one. Nothing had changed.
- **The POS exists.** rangka-apps ships `kasir`: module `pos` plus the cashier work screen (0030). Five of its six
  modules (`users`, `contacts`, `catalogue`, `outlets`, `stock`) are already in `fnb_cabang`. Adding it would add
  only `pos`, and the items, outlets, stock and people would stay one set of data across both apps.
- **Raka did not know how.**
  - Everything Raka knows about Rangka is one generated prompt, `rangka-ai/knowledge/rangka.md`, plus `apps.json`. Both
    are built from pinned sibling commits.
  - `apps.json` lists 6 of 17 apps; `kasir` is not one of them.
  - Nothing tells Raka how to add an app (a `config.apps` or `config.modules` change, 0032) or how to explain what it
    shares.
  - Nothing tells it that only a change set makes anything happen, so "sudah aku buatkan" went out with nothing behind it.
- **Raka is not the only agent.**
  - Developers and partners build on Rangka with Claude Code, Codex, Cursor and other harnesses. They hit the same
    gaps: `.rk` strict YAML rules, change-set lifecycle, which module owns what, connector format.
  - Each harness today gets that knowledge from `CLAUDE.md` files and luck.
- **The format exists.** Agent Skills (a folder with `SKILL.md`, YAML frontmatter `name` and `description`, optional
  `references/`, `scripts/`, `assets/`) loads progressively: only names and descriptions sit in the prompt, and a
  skill's body is read when the task needs it. Claude Code, the Claude API and apps, and a growing number of other
  harnesses read it as is. Others take the folder as plain instructions.

## Decision

### 1. One skills library, in the open Agent Skills format

- A **Rangka skill** is a standard Agent Skills folder:

  ```
  skills/rangka-compose/
    SKILL.md            # frontmatter + instructions
    references/         # longer material read on demand (module list rules, examples)
    evals/              # prompts with the expected outcome (§6); ignored by harnesses
  ```

- **Frontmatter:**
  - `name`: kebab-case, equal to the folder name, prefixed `rangka-`.
  - `description`: what the skill does and when to use it, ≤ 1024 characters, written so a model picks it from the
    description alone.
  - Rangka-specific keys sit only under `metadata:`, so every harness still parses the file:
    - `rangka.version`: the release that ships it;
    - `rangka.requires`: kernel version or feature, for example `module_sets` (0032);
    - `rangka.writes`: which layers its actions may change, for example `[config, site/custom]`.
- **Written for any harness.** Each action is given two ways:
  - the MCP tool (`rangka_meta`, `rangka_changeset_propose`, …), for an agent connected to a kernel, which is how
    Raka runs;
  - the CLI or file equivalent (`rangka validate`, editing `site/config.rk`, `rangka apply`), for an agent in a
    checkout.

  A skill never assumes one harness's tool names.
- **Scripts are optional helpers.** A skill must work without running them: Raka runs no code, and some harnesses
  cannot either. Skills never contain secrets, keys or customer data.

### 2. Who writes which skills (ownership as in 0000)

| Repo | Skills (v1) |
| --- | --- |
| rangka-os (`skills/`) | `rangka-concepts` (layers, apps vs modules, data shared across apps, what Raka may never do) · `rangka-rk-files` (strict YAML rules, schema header, validator codes) · `rangka-changesets` (propose → validate → preview, risk, never approve or apply, how to tell the owner) · `rangka-compose` (add or remove an app or module, read the catalogue, explain `shares`/`adds`) · `rangka-overlays` (`x_` fields, options, views) · `rangka-flows` · `rangka-dashboards` · `rangka-work-screens` (0030) · `rangka-theme-and-layout` · `rangka-imports` |
| rangka-apps (`apps/<app>/skill/`, `skills/`) | one per app, for example `rangka-app-kasir`: who it is for, what it shares and adds, its onboarding questions, roles and screens, and how to set it up on an existing site. Plus `rangka-app-authoring` for writing modules and apps. |
| rangka-connectors (`skills/`) | `rangka-connectors` (what exists, install, secrets form, test) · `rangka-connector-authoring` (the format, 0017 secrets rules, 0033 company drafts) |
| rangka-edge (`skills/`) | `rangka-edge-devices` (what works offline today, pairing, device connectors) |
| rangka-cloud (private) | `juragan-interview` and other Cloud-only skills. They are loaded by Raka in the Cloud and never published. |

Each skill lives next to the code it describes and changes in the same PR as that code. A PR that changes a
capability without its skill fails review, the same rule as docs.

### 3. Installable anywhere, documented on rangka-os

- **Release asset.** Each rangka-os release attaches `rangka-skills-<version>.zip`, holding the skills of all four
  public repos at their pinned commits. It is built by CI, never committed (0000 §6).
- **CLI**: `rangka skills install [--to <dir>]`, from `@rangka-os/cli`.
  - Defaults by harness: `.claude/skills/` (project) or `~/.claude/skills/`, `.agents/skills/` for harnesses that read
    that folder, or any `--to`.
  - `rangka skills list` and `rangka skills check` (the lint of §6).
- **Claude Code plugin.** rangka-os carries a plugin marketplace manifest, so
  `/plugin marketplace add rangka-os/rangka-os` installs the library and updates it per release.
- **From a running kernel.** MCP tools serve the skills that match that kernel's version and installed apps:
  - `rangka_skills` returns names and descriptions;
  - `rangka_skill {name}` returns the `SKILL.md` body;
  - `rangka_skill_file {name, path}` returns one reference file.

  Any MCP harness connected to a kernel can load them without installing anything.
- **Docs.** The docs site gets a **Skills** section, generated from the `SKILL.md` files by a script like
  `gen-reference.mjs` (the docs workflow already checks out rangka-apps and rangka-connectors). It has:
  - one page per skill, showing its description, metadata, body and references;
  - an install page per harness (Claude Code, Claude API and apps, Codex, Cursor, others);
  - a page on writing a Rangka skill (this contract's rules).

### 4. Raka is one consumer

- **Prompt.** Raka's system prompt lists the available skills by name and description (the kernel's `rangka_skills`,
  or the zip in the Cloud before any kernel runs). A tool `load_skill {name}` reads one when needed.
  `rangka-concepts` and `rangka-changesets` are always loaded. This works the same with every provider (Anthropic,
  OpenAI-compatible, DeepSeek), because rangka-ai does the loading itself.
- **Knowledge pack.** `knowledge/rangka.md` shrinks to what the skills do not cover. `knowledge/apps.json` goes: what
  exists comes from the live catalogue (§5).
- **Interview.** The Cloud's interview loads `rangka-compose` and the app skills, so composition and "mau sekalian
  POS" use the same text a developer's Claude Code uses.

### 5. Two kernel pieces the skills need

Skills are instructions. They cannot know what exists on a site, and they cannot stop a false claim, so:

- **Live catalogue.**
  - `rangka catalogue --json` and the MCP tool `rangka_catalogue` list each app, module and connector with:
    - its state, `installed`, `available` or `not_in_plan` (0028);
    - its title and summary;
    - for apps, `shares` (modules already installed that it reuses) and `adds` (modules it would add), computed by
      0032's resolver.
  - `rangka-compose` tells every agent to read this first and never to name an app from memory.
- **Proof before claim.**
  - Skills that write tell the agent never to say something is done. They say what was proposed and what the owner
    does next.
  - The studio and web app show completion from the change set's status, with a link to what changed.
  - rangka-ai enforces this too. A Raka turn that claims completion with no change set in that turn is repaired once,
    then replaced by a fixed line ("Aku belum mengubah apa pun. Ini usulanku: …"). The phrase list and tests live in
    rangka-ai.

### 6. Quality

- **Lint.** `rangka skills check` runs in each repo's CI and checks that:
  - the frontmatter is valid and `name` equals the folder;
  - the description length is within the limit;
  - every reference link resolves;
  - every validator code, MCP tool and CLI command the skill names really exists in that kernel version;
  - no secret-looking string appears.
- **Evals.** Each skill's `evals/*.yaml` gives a prompt, a starting site and the expected outcome. The expected
  outcome is one of:
  - a change set touching these paths with this risk;
  - an answer that names these apps;
  - an escalation.

  rangka-ai CI runs them with the scripted provider on every PR, and with a real provider nightly. First eval: "mau
  sekalian POS" on an F&B Cabang site proposes adding `kasir` and says it shares `catalogue`, `outlets`, `stock`
  and `users` and adds `pos`.
- **Missing pieces are said.**
  - `rangka-compose` and `rangka-connectors` tell the agent to say plainly when nothing fits, offer what does exist
    and file a gap.
  - For Mogu that means:
    - no GoFood, GrabFood or ShopeeFood connector exists, so it offers daily sales or a CSV import;
    - no offline POS on Edge yet, so the cashier works online for now.

### 7. Guardrails do not change

- A skill grants nothing. What an agent may do is still decided by the kernel and by rangka-ai's guardrails:
  - AI never approves or applies (0020);
  - 0033's escalations stay;
  - high risk is owner-only;
  - money and stock never depend on an LLM.
- A skill that tells an agent to approve, apply or read secrets fails `rangka skills check`.

## Contract changes

- **rangka-os:**
  - `skills/`;
  - CLI `rangka skills install|list|check` and `rangka catalogue --json`;
  - MCP `rangka_skills`, `rangka_skill`, `rangka_skill_file`, `rangka_catalogue`;
  - the release zip and the plugin marketplace manifest;
  - the docs site's Skills section.
- **rangka-apps, rangka-connectors, rangka-edge:** `skills/` folders, and `rangka skills check` in CI.
- **rangka-ai:**
  - `load_skill`, skills in the system prompt, evals;
  - the completion-claim guardrail;
  - `apps.json` removed and `rangka.md` reduced.
- **rangka-cloud:** private skills, the interview loads the published ones, and completion is shown from change sets.

## Order

1. **The Mogu fix:**
   - `rangka-concepts`, `rangka-changesets`, `rangka-compose`, `rangka-app-kasir`;
   - `rangka catalogue --json` and `rangka_catalogue`;
   - Raka's `load_skill` and the claim guardrail;
   - the POS eval.
2. Install paths (`rangka skills install`, zip, plugin manifest) and the docs Skills section.
3. The rest of the rangka-os skills, connector skills and app skills for every app.
4. `rangka-edge-devices` when Edge has device connectors.

## Open

- Whether rangka-apps keeps one skill per app or one per module plus app summaries.
- Whether the kernel should also expose skills as MCP prompts, beside the tools.
- Translations: skills are written in English (code and docs rule), and examples carry the Bahasa Indonesia owners
  type. Is that enough for Raka's replies?
