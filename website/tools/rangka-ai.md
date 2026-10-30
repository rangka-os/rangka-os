# Rangka AI

The open-source agent that understands Rangka OS and changes a running installation by proposing
[change sets](/concepts/change-sets). It never writes code, never edits records, and can never approve or apply.
Repository: [rangka-os/rangka-ai](https://github.com/rangka-os/rangka-ai).

## What it does

> "Tambah nomor PO di faktur, wajib untuk pelanggan grosir"

1. Reads the installation through MCP (`rangka_meta`, `rangka_get_file`).
2. Drafts overlays: `custom/customer.rk` adds `x_customer_group`; `custom/sales_invoice.rk` adds `x_po_number`, a submit
   rule and its place on the form.
3. Validates; when the validator rejects a draft, repairs it from the messages, up to a fixed number of tries.
4. Previews: affected records, before/after files.
5. Hands the owner a summary in Bahasa Indonesia: what changes, why, risk and reasons, what it will not touch, who must
   approve.

> "matikan login password"

Escalated to an admin before any model runs: `system.rk` is out of its reach.

## Guardrails are code

- It may write layers 4–6 only: overlays, `config.rk`, `features.rk`, themes, layouts, dashboards.
- Editing existing app files is refused (it redoes the change as an overlay); `system.rk`, secrets, roles,
  permissions, SSO and AI settings are escalated.
- It has no approve or apply tool; its MCP client refuses those names.
- Risk is computed the same way the kernel computes it.

## Onboarding for Rangka Cloud

A structured interview — business type, outlets, what they sell, tools they already use (WhatsApp, Tokopedia, Excel,
Accurate), pain points — leads to a recommended template, plan and connectors and an initial change set
(`config.rk`, home layout, theme). Rangka Cloud puts a chat in front of it.

## Run it

```bash
cd rangka-ai && npm install && npm test
npx tsx src/cli.ts propose "ganti warna utama jadi oranye"                       # offline, scripted provider
npx tsx src/cli.ts chat --kernel http://localhost:8080 --token rk_… --provider anthropic   # ANTHROPIC_API_KEY
```

Providers: Anthropic and any OpenAI-compatible API; a scripted provider runs every test offline.
