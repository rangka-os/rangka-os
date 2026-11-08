---
name: rangka-concepts
description: "How Rangka OS is put together: apps built from shared modules, one business with one set of data, the company layer, change sets, and what an AI agent may and may not do. Read this before any other Rangka skill, and whenever you explain to a business owner what their system is or why something works the way it does."
metadata: {rangka: {version: 0.5.0, writes: []}}
---

# Rangka concepts

Rangka OS runs business apps (sales, stock, cashier, rental, courses and more) from declarative `.rk` metadata. One
installation serves one business (a **site**). Juragan is the product name owners see; Raka is its AI agent.

## Apps are made of shared modules

- A **module** owns models and their data: `contacts`, `catalogue` (items), `outlets`, `stock`, `pos`,
  `invoicing`, `payments`, `accounting` and so on.
- An **app** is a product an owner recognises (`fnb_cabang` "Rangka F&B Cabang", `kasir` "Rangka Kasir", `crm`,
  `sewa_alat`). It lists the modules it uses, its menu, dashboards, roles, work screens and onboarding questions.
- Two apps that use the same module **share that module's data**. Adding `kasir` to a site that runs `fnb_cabang`
  does not create a second item list or a second stock: the cashier sells the same items from the same stock at the
  same outlets. This is the main thing to explain to an owner who asks for "another app".
- Which apps a site runs is `apps:` in the site's `config.rk`. The site's plan (`plan:` and `features.rk`) limits
  which apps are allowed.
- Never name an app or module from memory. Read the catalogue (see `rangka-compose`).

## Layers and who changes what

| Layer | What | Who may change it |
| --- | --- | --- |
| 2 | `system.rk`: database, queue, mail, secrets | an operator |
| 3 | `connectors/`: integrations with outside services | proposed by an agent only through the connector skills |
| 4 | the company: `config.rk`, `features.rk`, `custom/<model>.rk` overlays (`x_` fields), the site's own `models/` and `flows/` | proposed by an agent, approved by a person |
| 5 | apps and modules (the catalogue) | their maintainers, never an agent on a site |
| 6 | themes and layouts | proposed by an agent, approved by a person |

A company's changes never edit an app or module: they go in the company layer.

## Change sets are the only way anything changes

Every change to a running site is a **change set**: proposed → validated → previewed → approved → applied, and it can
be reverted. The kernel computes its risk (low, medium, high). High risk is approved by the owner only. See
`rangka-changesets` for the steps.

## What an AI agent never does

- Never approves or applies a change set, never reverts one, never reads or sets a secret. A person does those.
- Never changes money or stock directly. Submitted documents (invoices, receipts, stock moves) are immutable: they
  are cancelled and amended by people.
- Never says something is done, made, added or connected unless a change set was applied. Until then, say what you
  proposed and what the owner does next (see `rangka-changesets`, "Telling the owner").
- Never invents a capability. If nothing in the catalogue fits, say so plainly and offer what exists.

## Words owners use

Owners speak Bahasa Indonesia, informally ("kamu"). Map their words to Rangka terms: "kasir" or "POS" → app `kasir`
(module `pos`); "stok" → module `stock`; "cabang" or "outlet" → module `outlets`; "ojol" orders (GoFood, GrabFood,
ShopeeFood) → there is no connector for them yet: the catalogue says what exists.
