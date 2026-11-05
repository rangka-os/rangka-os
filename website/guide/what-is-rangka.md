# What is Rangka OS

Rangka OS is a source-available engine (Elastic License 2.0) for business software — CRM, sales, POS, inventory, accounting — in which every
app is described in files instead of written in code. You describe a model ("a deal has a customer, a stage and a
value"), its rules, dashboards and screens in `.rk` files; the Rangka kernel turns them into a database, an API,
realtime events and a working user interface.

It is built for Indonesian small and medium businesses: Bahasa Indonesia first, rupiah, PPN and NPWP, WhatsApp and
QRIS as first-class integrations, and plans priced per company or outlet rather than per seat.

## Why files instead of code

| | Code-first ERP | Rangka OS |
| --- | --- | --- |
| A new field or rule | A developer edits code, tests, deploys | A change set to one `.rk` file, validated and previewed, applied live |
| Who can change it | Engineers | Owners in the Console, Rangka AI, app authors |
| What an AI can safely do | Write code that someone must review line by line | Propose a small schema-checked diff that a person approves |
| Upgrading an app | Merge conflicts with customisations | Company changes live in overlays that upgrades never touch |
| Infrastructure | Often tied to one vendor's stack | One Go binary and Postgres; runs anywhere, including a laptop |

## What is in the box

- **The kernel** (`rangka`): validates `.rk` files, migrates Postgres, runs the document lifecycle with approvals and
  number series, serves REST, SSE realtime, a queue and scheduler, Rangka ID (login, API tokens, OpenID Connect),
  permissions and row scope per outlet, attachments, print formats (HTML and PDF), imports, change sets with a live
  preview, backup and restore, and an MCP endpoint.
- **The web app** at `/app/`: dashboards, lists, forms, approvals, imports, print and the process canvas, drawn from
  the `.rk` files.
- **System capabilities**: db, cache, queue, scheduler, realtime, files, search, notifications, auth, declared in
  `system.rk` and used by apps in one line. Today the drivers are Postgres, a memory cache and local files;
  notifications go to the log. Valkey, S3, email and push delivery, and sign-in through another identity provider
  are not built yet.
- **Rangka Console**: the control pane that shows every layer, capability, app, change set and connector.
- **A design system**: React components with Frappe UI structure in Rangka colours (Ink, Bata, warm stone), light and dark, used by every screen.
- **Rangka AI** (Raka): an agent that proposes change sets, never code, and never approves or applies them. It runs as
  a sidecar the kernel relays chat to. See [Rangka AI](/tools/rangka-ai).

## Who it is for

- **Businesses** that want software shaped around how they work, without hiring a team to build it.
- **Partners and consultants** who build templates (a clinic, a coffee chain, a distributor) on shared core apps.
- **Developers** who want a metadata engine they can self-host, extend and read end to end.

Next: [Quick start](./quick-start) or [Architecture and layers](./architecture).
