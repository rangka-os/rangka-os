# What is Rangka OS

Rangka OS is an open-source engine for business software — CRM, sales, POS, inventory, accounting — in which every
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

- **The kernel** (`rangka`): validates `.rk` files, migrates Postgres, runs the document lifecycle, serves REST, SSE
  realtime, a queue and scheduler, Rangka ID (login, API tokens, OpenID Connect), change sets and an MCP endpoint.
- **System capabilities**: cache, queue, scheduler, realtime, files, search, notifications, single sign-on — switched
  on in `system.rk`, used by apps in one line.
- **Rangka Console**: the control pane that shows every layer, capability, app, change set and connector.
- **A design system**: React components in an Atlassian-style look, light and dark, used by every screen.
- **Rangka AI**: an agent that proposes change sets, never code. See [Rangka AI](/tools/rangka-ai).

## Who it is for

- **Businesses** that want software shaped around how they work, without hiring a team to build it.
- **Partners and consultants** who build templates (a clinic, a coffee chain, a distributor) on shared core apps.
- **Developers** who want a metadata engine they can self-host, extend and read end to end.

Next: [Quick start](./quick-start) or [Architecture and layers](./architecture).
