---
name: rangka-changesets
description: "How to change a running Rangka site safely: write a change set, validate and preview it, read its risk, and tell the owner what happens next without claiming it is done. Use whenever you are about to propose any change to a Rangka site (config, fields, flows, themes), or to report the result of one."
metadata: {rangka: {version: 0.5.0, writes: [config, custom, models, flows, dashboards, themes, layouts]}}
---

# Rangka change sets

A change set is a list of file operations on the site's `.rk` metadata. It moves through
`proposed → validated → previewed → approved → applied` (or `invalid`, `rejected`, `reverted`). An agent takes it up
to **previewed**. A person approves and applies it in the app or the Console.

## Steps

1. **Read before you write.** Read every file you will change, so patches point at the right places.
   - MCP: `rangka_get_file {path: "config.rk"}`; for the whole installation `rangka_meta`.
   - In a checkout: open `site/config.rk` (or the file under `site/`).
2. **Propose** one change set per request, with a short title and the reason in the owner's words.
   - MCP: `rangka_changeset_propose {title, reason, ops}`. Each op is `create` or `replace` with the full `.rk` text,
     `patch` with an RFC 6902 list, or `delete`, on a change-set path such as `config.rk`, `custom/<model>.rk`,
     `models/x_<name>.rk`, `flows/<name>.rk`, `themes/<name>.rk`.
   - In a checkout: edit the file, then run `rangka validate --apps <apps> --site <site>`.
3. **Validate.** MCP: `rangka_changeset_validate {id}`. If it is `invalid`, read the errors (file, line, code), fix the
   ops and propose again. Do not explain validator errors to the owner; fix them.
4. **Preview.** MCP: `rangka_changeset_preview {id}`. It shows the migrations and how many records the touched models
   hold. Check the result with `rangka_changeset_status {id}`.
5. **Stop.** There is no tool for approving or applying, and there never will be one for an agent. The owner (or an
   approver named in `config.ai.approvers`) does it.

## Risk

The kernel computes risk; you cannot set it.

- **low**: themes, layouts, site dashboards.
- **medium**: `config.rk` changes such as `apps:` (adding or removing an app), new fields, new company models and
  flows.
- **high** (owner only): tax, AI settings, money or stock fields, removing data.

Mention the risk only when it is high. To say who approves, read `approvable_by` on the change set (MCP:
`rangka_changeset_status {id}`): when it is `["owner"]`, say "Perubahan ini perlu disetujui pemilik."

## When the platform refuses

If propose fails with `ai_forbidden_path`, the refused paths come back in the error detail (MCP:
`structuredContent.detail.paths`; REST: `error.paths`). Explain it as a platform limit, not your choice: those files
are changed by people, not by an assistant. Name who can: an operator for `system.rk`, `features.rk` and the plan; the
owner or an admin in the Console for roles, permissions, AI and sign-in settings; the module's maintainers for
`apps/` and `modules/`; the connector's maintainers for catalogue connectors. Offer what you can propose instead.

## Telling the owner

Your words never say a change is done. The app shows the change set's status and a link once it is applied.

- After proposing: say what you prepared and what the owner does next.
  "Aku sudah siapkan usulan: tambah Rangka Kasir. Barang, stok dan outlet tetap sama dengan F&B Cabang; yang baru
  hanya modul kasir. Cek pratinjaunya, lalu setujui kalau sudah pas."
- If validation failed and you could not fix it: say you could not prepare it yet, in one sentence, and what you
  will try or what you need from them.
- Never: "sudah aku buatkan", "sudah jadi", "sudah ditambahkan", "done", unless `rangka_changeset_status` says
  `applied` for that change set. Even then, prefer "Sudah diterapkan" next to the link the app shows.

## Many small or one large

One request, one change set. If the owner asks for two unrelated things, propose two change sets and say so.
