---
layout: home
title: Rangka OS
titleTemplate: Fondasi bisnismu, dibangun bersama AI

hero:
  name: Rangka OS
  text: Business apps as files your AI can safely change
  tagline: An open-source, declarative operating system for CRM, sales, POS and ERP, built for Indonesian small businesses. Every app is a .rk file. Every change is a reviewed change set.
  image:
    src: /raka.svg
    alt: Raka, the Rangka mascot
  actions:
    - theme: brand
      text: Quick start
      link: /guide/quick-start
    - theme: alt
      text: What is Rangka OS?
      link: /guide/what-is-rangka
    - theme: alt
      text: GitHub
      link: https://github.com/rangka-os/rangka-os

features:
  - title: Apps are .rk files
    details: Models, rules, dashboards, flows, themes and layouts are strict YAML with JSON Schemas. The kernel turns them into Postgres tables, a REST + MCP API, realtime events and screens. No application code.
  - title: Changed through change sets
    details: Owners, the Console and AI all propose the same diff. It is validated, previewed on a copy with the SQL it will run, approved by a person, applied live and can be reverted.
  - title: Hard backend parts, ready to switch on
    details: Queue, scheduler, realtime (SSE), cache, files, search, notifications and single sign-on are capabilities in system.rk. Apps ask in one line; one Postgres and one binary run them all.
  - title: Made for Indonesian businesses
    details: Bahasa Indonesia first, rupiah without decimals, PPN 12% × DPP 11/12, NPWP, QRIS and WhatsApp connectors, offline-tolerant POS on the roadmap.
---

<div class="rk-section">

## Seven layers, read from the bottom up

<p class="lead">Upper layers use lower ones, never the reverse. Owners and Rangka AI change layers 4–6 at runtime; the kernel and capabilities stay stable.</p>

<div class="rk-layers">
  <div class="rk-layer"><span class="n">6</span><b>Presentation</b><span>Themes and layouts from templates, light and dark · <code>themes/*.rk</code> <code>layouts/*.rk</code></span></div>
  <div class="rk-layer"><span class="n">5</span><b>Apps</b><span>Models, views, rules, dashboards, flows, roles · <code>apps/&lt;app&gt;/**.rk</code></span></div>
  <div class="rk-layer"><span class="n">4</span><b>Organisation</b><span>Installed apps, plan, config, company overlays · <code>config.rk</code> <code>features.rk</code> <code>custom/*.rk</code></span></div>
  <div class="rk-layer"><span class="n">3</span><b>Integrations</b><span>WhatsApp, QRIS, e-Faktur, marketplaces · <code>connector.rk</code></span></div>
  <div class="rk-layer"><span class="n">2</span><b>System capabilities</b><span>db, cache, queue, scheduler, realtime, files, search, notify, auth · <code>system.rk</code></span></div>
  <div class="rk-layer"><span class="n">1</span><b>Kernel</b><span>Validator, migrations, CEL, lifecycle, REST + MCP, Rangka ID · one Go binary</span></div>
  <div class="rk-layer"><span class="n">0</span><b>Deploy</b><span>Docker, Helm, desktop, partner one-click, Rangka Cloud</span></div>
</div>

</div>

<div class="rk-section">

## A model, and what the kernel makes of it

<div class="rk-two">
<div>

```yaml
# apps/crm/models/deal.rk
name: deal
label: {id: Peluang, en: Deal}
realtime: true
fields:
  - {name: customer, type: link, to: customer, required: true}
  - name: stage
    type: select
    options:
      - {value: prospect, label: {id: Prospek}, probability: 10}
      - {value: won, label: {id: Menang}, probability: 100}
  - {name: value, type: currency}
  - name: weighted_value
    type: currency
    compute: round(value * option('stage', 'probability') / 100)
views:
  kanban: {group_by: stage, sum: value}
```

</div>
<div>

- A table `r_deal` with typed columns, indexes on links, an audit trail.
- `GET/POST /api/v1/r/deal`, with rules and computed fields applied on every save.
- MCP tools so Claude, Omilo or any AI client can read and query it.
- A list, a form and a kanban board grouped by stage, summing value.
- Live updates to every open screen, because of `realtime: true`.
- Validation with `file:line:col` errors before anything runs.

</div>
</div>

</div>

<div class="rk-section">

## How Rangka AI changes a running system

<p class="lead">"Tambah nomor PO di faktur, wajib untuk pelanggan grosir." The agent never writes code and can never approve.</p>

<ol class="rk-steps">
  <li><b>Propose</b><span>Drafts overlays <code>custom/customer.rk</code> and <code>custom/sales_invoice.rk</code>.</span></li>
  <li><b>Validate</b><span>Same checks as <code>rangka validate</code>; repairs its own mistakes from the messages.</span></li>
  <li><b>Preview</b><span>Dry run on a copy: SQL, records affected, before and after of every file.</span></li>
  <li><b>Approve, apply</b><span>The owner approves; the field is live without a restart, and can be reverted.</span></li>
</ol>

</div>

<div class="rk-section">

## The Rangka family

<div class="rk-repos">
  <a class="rk-repo" href="https://github.com/rangka-os/rangka-os"><b>rangka-os</b><span>Kernel, schemas, validator, design system, Console, these docs.</span><i>open source</i></a>
  <a class="rk-repo" href="https://github.com/rangka-os/rangka-apps"><b>rangka-apps</b><span>Core, sales and CRM apps, the CRM template, an example site.</span><i>open source</i></a>
  <a class="rk-repo" href="https://github.com/rangka-os/rangka-connectors"><b>rangka-connectors</b><span>Plug-and-play integrations as connector.rk.</span><i>open source</i></a>
  <a class="rk-repo" href="https://github.com/rangka-os/rangka-ai"><b>rangka-ai</b><span>The agent that changes an installation through change sets.</span><i>open source</i></a>
  <a class="rk-repo" href="./ecosystem/cloud"><b>rangka-cloud</b><span>Managed hosting with an onboarding chat, for businesses that want it running today.</span><i class="closed">managed service</i></a>
</div>

</div>

<div class="rk-status">

**v0, October 2026.** The kernel, Console and Rangka AI run and are tested together, but the kernel still uses in-tree stand-ins for its Go libraries and several capabilities are logged rather than sent. Not ready for production. See [status and roadmap](./roadmap).

</div>
