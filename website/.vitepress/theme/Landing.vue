<script setup lang="ts">
import { ref } from 'vue'
import { withBase } from 'vitepress'

const b = (p: string) => withBase(p)
const gh = 'https://github.com/rangka-os/rangka-os'

const tours = [
  { key: 'doc', label: 'Documents', file: 'invoice', note: 'Submittable documents with numbering, status, PPN and payments, generated from the model.' },
  { key: 'cmd', label: '⌘K', file: 'command', note: 'Search every record and screen, or ask Raka. Built into every app.' },
  { key: 'kanban', label: 'Kanban', file: 'kanban', note: 'One line in views.kanban turns any model into a board. Drag a card and the rules still run.' },
  { key: 'dash', label: 'Dashboards', file: 'dashboard-light', note: 'KPIs, charts and lists from dashboard.rk. Each widget is a query the owner can read.' },
  { key: 'console', label: 'Console', file: 'console-overview', note: 'The control pane for owners: apps, capabilities, connectors, themes and audit.' },
]
const tab = ref(tours[0].key)

const faqs = [
  ['Is Rangka OS ready for production?', 'Not yet. This is a v0 preview: the kernel, validator, Console and Rangka AI run end to end on a demo company, and several parts use in-tree stand-ins that will be replaced. Follow the roadmap for what lands next.'],
  ['Do I need to write code?', 'No for using and changing apps: models, rules, screens and dashboards are .rk files, and owners or Rangka AI change them through change sets. Yes if you want to contribute to the kernel itself, which is Go.'],
  ['How do you stop AI from breaking things?', 'AI can only propose. Every change set is validated against the schemas, previewed on a copy with the exact SQL it will run, and approved by a person; high-risk changes need the owner. Anything applied can be reverted.'],
  ['Can I host it myself?', 'Yes. One Go binary and one Postgres database run the kernel, queue, scheduler, realtime and cache. Your .rk files live in your own git repository.'],
  ['How is this different from Odoo or ERPNext?', 'Rangka is metadata-first: apps are plain files with JSON Schemas rather than Python modules, changes are reviewable diffs rather than settings scattered in a database, and AI is a first-class, guarded way to change them.'],
  ['What is Rangka Cloud?', 'A hosted way to start: describe your business in a chat, pick a template, and get a running instance through our deployment partners. It is not open source and is not available yet.'],
]
</script>

<template>
  <div class="lp">
    <!-- hero -->
    <section class="lp-hero">
      <a class="lp-pill" :href="b('/roadmap')"><span>Open source</span> v0 preview · see the roadmap →</a>
      <h1>The business OS your&nbsp;AI can <em>safely</em> change</h1>
      <p class="lp-sub">Rangka OS turns CRM, sales, POS and ERP into readable <code>.rk</code> files. Owners and AI propose changes, a person approves them, and the kernel ships them live.</p>
      <div class="lp-cta">
        <a class="lp-btn lp-btn-primary" :href="b('/guide/quick-start')">Get started</a>
        <a class="lp-btn" :href="gh" target="_blank" rel="noopener">
          <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>
          Star on GitHub
        </a>
      </div>
      <p class="lp-meta">One Go binary · one Postgres · your files in your git</p>
      <div class="lp-frame lp-hero-shot">
        <div class="lp-bar"><i></i><i></i><i></i><span>kopi-senja.rangka.app / crm</span></div>
        <img class="only-light" :src="b('/screens/dashboard-light.webp')" alt="Rangka CRM dashboard for a coffee shop, generated from .rk files" width="2000" height="1250" />
        <img class="only-dark" :src="b('/screens/dashboard-dark.webp')" alt="Rangka CRM dashboard in dark mode" width="2000" height="1250" />
      </div>
    </section>

    <!-- built on -->
    <section class="lp-strip">
      <p>Built on parts you already trust</p>
      <ul><li>PostgreSQL</li><li>Go</li><li>React</li><li>OpenID Connect</li><li>Model Context Protocol</li><li>JSON Schema</li></ul>
    </section>

    <!-- problem -->
    <section class="lp-sec">
      <p class="lp-eyebrow">The problem</p>
      <h2>Small businesses are stuck between two bad options</h2>
      <div class="lp-grid2">
        <div class="lp-card">
          <span class="lp-tag bad">Off the shelf</span>
          <h3>The app doesn't bend to how you work</h3>
          <p>You adapt the shop to the software. A missing field means a spreadsheet on the side, and the real process lives in WhatsApp.</p>
        </div>
        <div class="lp-card">
          <span class="lp-tag bad">Built for you</span>
          <h3>Custom software never quite finishes</h3>
          <p>Every change waits for a developer, costs a sprint and risks breaking something nobody remembers writing.</p>
        </div>
      </div>
    </section>

    <!-- approach -->
    <section class="lp-sec">
      <p class="lp-eyebrow">Our approach</p>
      <h2>Apps as files. Changes as proposals.</h2>
      <div class="lp-grid3">
        <div class="lp-card">
          <span class="lp-num">A</span>
          <h3>Describe it in <code>.rk</code></h3>
          <p>Models, rules, screens, dashboards and flows are strict YAML with schemas. The kernel turns them into tables, an API and UI.</p>
        </div>
        <div class="lp-card">
          <span class="lp-num">B</span>
          <h3>Propose a change set</h3>
          <p>An owner, the Console or Rangka AI writes the same kind of diff. It is validated and previewed with the SQL it will run.</p>
        </div>
        <div class="lp-card">
          <span class="lp-num">C</span>
          <h3>Approve and ship live</h3>
          <p>A person approves; the kernel applies it without a deploy and keeps the history, so any change can be reverted.</p>
        </div>
      </div>
    </section>

    <!-- pillars -->
    <section class="lp-sec">
      <div class="lp-pillar">
        <div class="lp-pillar-text">
          <span class="lp-big">01</span>
          <h2>Start with apps that already work</h2>
          <p>CRM, sales, POS and inventory ship as ready apps for Indonesian businesses: rupiah without decimals, PPN 12% on DPP 11/12, NPWP, and QRIS and WhatsApp connectors.</p>
          <ul><li>Install an app with one line in <code>config.rk</code></li><li>Lists, boards, forms and documents generated from models</li><li>Bahasa Indonesia first, English alongside</li></ul>
          <a :href="b('/ecosystem/apps')">Browse the apps →</a>
        </div>
        <div class="lp-frame"><img :src="b('/screens/kanban.webp')" alt="Deals kanban board generated from views.kanban" loading="lazy" width="2000" height="1250" /></div>
      </div>

      <div class="lp-pillar flip">
        <div class="lp-pillar-text">
          <span class="lp-big">02</span>
          <h2>Change it without an engineer</h2>
          <p>Ask Rangka AI for a PO number on invoices. It reads your models, writes an overlay in your company layer and explains why. You see the exact diff, the risk and the steps before anything goes live.</p>
          <ul><li>Proposed → validated → previewed → approved → applied</li><li>High-risk changes need the owner</li><li>Revert in one click, with a full audit log</li></ul>
          <a :href="b('/concepts/change-sets')">How change sets work →</a>
        </div>
        <div class="lp-frame"><img :src="b('/screens/console-changeset.webp')" alt="Console showing an AI-proposed change set with its diff" loading="lazy" width="2000" height="1250" /></div>
      </div>

      <div class="lp-pillar">
        <div class="lp-pillar-text">
          <span class="lp-big">03</span>
          <h2>Own every byte</h2>
          <p>Your business is a folder of files in your own git, running on one binary and one database. Queue, scheduler, realtime, cache, files and single sign-on are switched on in <code>system.rk</code>, not bolted on.</p>
          <ul><li>Self-host anywhere, or use Rangka Cloud later</li><li>REST, SSE and MCP from the same models</li><li>Rangka ID with OIDC built into the kernel</li></ul>
          <a :href="b('/guide/architecture')">Read the architecture →</a>
        </div>
        <div class="lp-term">
          <div class="lp-bar dark"><i></i><i></i><i></i><span>terminal</span></div>
<pre><span class="c"># check every .rk file against the schemas</span>
<b>$</b> rangka validate --apps ../rangka-apps

<span class="c"># DATABASE_URL points at your Postgres</span>
<b>$</b> rangka migrate --apps ../rangka-apps
<b>$</b> rangka seed --apps ../rangka-apps --org kopi-senja

<span class="c"># REST, SSE, MCP and Rangka ID on one port</span>
<b>$</b> rangka serve --apps ../rangka-apps --addr :8080</pre>
        </div>
      </div>
    </section>

    <!-- tour -->
    <section class="lp-sec">
      <p class="lp-eyebrow">Product tour</p>
      <h2>Every screen comes from the files</h2>
      <div class="lp-tabs" role="tablist">
        <button v-for="t in tours" :key="t.key" role="tab" :aria-selected="tab === t.key" :class="{ on: tab === t.key }" @click="tab = t.key">{{ t.label }}</button>
      </div>
      <template v-for="t in tours" :key="t.key">
        <div v-show="tab === t.key">
          <p class="lp-tour-note">{{ t.note }}</p>
          <div class="lp-frame"><img :src="b('/screens/' + t.file + '.webp')" :alt="t.label + ' screen'" loading="lazy" width="2000" height="1250" /></div>
        </div>
      </template>
    </section>

    <!-- family -->
    <section class="lp-sec">
      <p class="lp-eyebrow">Open by default</p>
      <h2>One family of repositories</h2>
      <div class="lp-repos">
        <a href="https://github.com/rangka-os/rangka-os" target="_blank" rel="noopener"><b>rangka-os</b><span>Kernel, schemas, validator and Console</span><em>Open source</em></a>
        <a href="https://github.com/rangka-os/rangka-apps" target="_blank" rel="noopener"><b>rangka-apps</b><span>CRM, sales, POS and inventory as .rk</span><em>Open source</em></a>
        <a href="https://github.com/rangka-os/rangka-connectors" target="_blank" rel="noopener"><b>rangka-connectors</b><span>QRIS, WhatsApp, e-Faktur and more</span><em>Open source</em></a>
        <a href="https://github.com/rangka-os/rangka-ai" target="_blank" rel="noopener"><b>rangka-ai</b><span>Guardrailed agent that proposes change sets</span><em>Open source</em></a>
        <a :href="b('/ecosystem/cloud')"><b>Rangka Cloud</b><span>Chat onboarding and hosted instances</span><em class="closed">Hosted · later</em></a>
      </div>
    </section>

    <!-- faq -->
    <section class="lp-sec lp-faq-wrap">
      <div>
        <p class="lp-eyebrow">FAQ</p>
        <h2>Questions, answered</h2>
        <p class="lp-muted">Something missing? <a :href="gh + '/issues'" target="_blank" rel="noopener">Ask on GitHub</a>.</p>
      </div>
      <div class="lp-faq">
        <details v-for="(f, i) in faqs" :key="i" :open="i === 0">
          <summary>{{ f[0] }}</summary>
          <p>{{ f[1] }}</p>
        </details>
      </div>
    </section>

    <!-- closing -->
    <section class="lp-close">
      <img :src="b('/raka.svg')" alt="" width="64" height="64" />
      <h2>Build your business on Rangka</h2>
      <p>Clone the repos, run the demo company on your laptop and change it with AI.</p>
      <div class="lp-cta">
        <a class="lp-btn lp-btn-light" :href="b('/guide/quick-start')">Get started</a>
        <a class="lp-btn lp-btn-ghost" :href="b('/guide/what-is-rangka')">What is Rangka OS?</a>
      </div>
    </section>

    <footer class="lp-foot">
      <div class="lp-foot-brand"><img :src="b('/raka.svg')" alt="" width="28" height="28" /><b>Rangka OS</b><span>Fondasi bisnismu, dibangun bersama AI.</span></div>
      <div><h4>Learn</h4><a :href="b('/guide/what-is-rangka')">What is Rangka OS</a><a :href="b('/guide/quick-start')">Quick start</a><a :href="b('/guide/first-app')">Your first app</a><a :href="b('/concepts/models')">Concepts</a></div>
      <div><h4>Reference</h4><a :href="b('/reference/schema/')">Schemas</a><a :href="b('/reference/cli')">CLI</a><a :href="b('/reference/rest-api')">REST API</a><a :href="b('/reference/mcp')">MCP tools</a></div>
      <div><h4>Project</h4><a :href="b('/roadmap')">Roadmap</a><a :href="b('/contributing')">Contributing</a><a href="https://github.com/rangka-os" target="_blank" rel="noopener">GitHub</a></div>
    </footer>
  </div>
</template>
