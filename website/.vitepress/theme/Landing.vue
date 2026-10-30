<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { withBase, useData } from 'vitepress'

const b = (p: string) => withBase(p)
const gh = 'https://github.com/rangka-os/rangka-os'
const { isDark } = useData()

/* live star count from GitHub; hidden if the request fails */
const stars = ref<string | null>(null)
onMounted(async () => {
  try {
    const r = await fetch('https://api.github.com/repos/rangka-os/rangka-os')
    if (r.ok) {
      const n = (await r.json()).stargazers_count as number
      if (n >= 10) stars.value = n >= 1000 ? (n / 1000).toFixed(1) + 'K' : String(n)
    }
  } catch { /* offline: no count */ }
})

/* sticky pillars: which step is in view */
const step = ref(0)
const stepEls = ref<HTMLElement[]>([])
let io: IntersectionObserver | null = null
onMounted(() => {
  io = new IntersectionObserver((es) => {
    es.forEach((e) => { if (e.isIntersecting) step.value = Number((e.target as HTMLElement).dataset.i) })
  }, { rootMargin: '-45% 0px -45% 0px' })
  stepEls.value.forEach((el) => io!.observe(el))
})
onBeforeUnmount(() => io?.disconnect())

const steps = [
  { n: '01', a: 'Start with', b: 'ready-made', c: 'business apps', text: 'CRM, sales, POS and inventory ship as .rk apps for Indonesian businesses: rupiah without decimals, PPN 12% on DPP 11/12, NPWP, QRIS and WhatsApp. Install one with a line in config.rk.', link: '/ecosystem/apps', cta: 'Browse the apps', shot: 'kanban' },
  { n: '02', a: 'Change it', b: 'without', c: 'an engineer', text: 'Ask Rangka AI for a PO number on invoices. It writes an overlay in your company layer, explains why, and shows the exact diff and risk. A person approves; the kernel applies it live and can revert it.', link: '/concepts/change-sets', cta: 'How change sets work', shot: 'console-changeset' },
  { n: '03', a: 'Stay in control with', b: 'open-source', c: 'infrastructure', text: 'Your business is a folder of files in your git, served by one Go binary and one Postgres. Queue, scheduler, realtime, cache and single sign-on are switched on in system.rk.', link: '/guide/architecture', cta: 'Read the architecture', shot: '' },
]

const tabs = [
  { title: 'Production-grade core', icon: 'blocks', text: 'Strict YAML with JSON Schemas, a validator with 47 checks, generated tables, REST, SSE and MCP. Nothing hand-wired per app.', label: 'Concept', link: '/guide/rk-files', linkText: '.rk files' },
  { title: 'AI for rapid changes', icon: 'spark', text: 'Rangka AI reads your models and proposes change sets in layers 4–6 only. Guardrails live in code, not in a prompt.', label: 'Tool', link: '/tools/rangka-ai', linkText: 'Rangka AI' },
  { title: 'Control without drag', icon: 'shield', text: 'Validated, previewed on a copy, approved by a person, applied live, reverted in one click. High-risk changes need the owner.', label: 'Concept', link: '/concepts/change-sets', linkText: 'Change sets' },
]

const features = [
  { title: 'Familiar, modern interface', text: 'Atlassian-grade lists, boards and forms with quick filters, pagination and skeleton loading.', shot: 'kanban', pos: '30% 30%', icon: 'M3 4h18v4H3zM3 10h8v10H3zM13 10h8v10h-8z' },
  { title: 'Search and ask in one place', text: '⌘K finds any record or screen, or hands the question to Raka.', shot: 'command', pos: '50% 20%', icon: 'M10 4a6 6 0 104.47 10l4.77 4.77 1.41-1.41-4.77-4.77A6 6 0 0010 4z' },
  { title: 'Every change is a diff', text: 'See the file, the lines and the SQL before anything goes live.', shot: 'console-changeset', pos: '20% 62%', icon: 'M6 3v12a3 3 0 103 3V9h6a3 3 0 10-3-3H9V3z' },
  { title: 'Documents that behave', text: 'Numbering at submit, cancel and amend, PPN and payments, generated from the model.', shot: 'invoice', pos: '40% 25%', icon: 'M6 2h9l5 5v15H6zM14 3v5h5' },
  { title: 'Dashboards from files', text: 'Number cards, charts and lists declared in dashboard.rk and computed on open.', shot: 'dashboard-light', pos: '55% 40%', icon: 'M4 20V10h4v10zm6 0V4h4v16zm6 0v-7h4v7z' },
  { title: 'A control pane for owners', text: 'Apps, capabilities, connectors, members, themes and audit in the Console.', shot: 'console-overview', pos: '30% 30%', icon: 'M12 2l9 5v10l-9 5-9-5V7z' },
]

const scenarios = [
  { kicker: 'Example scenario', text: 'A coffee chain with three outlets wants the customer’s PO number on invoices. Raka proposes a one-field overlay; the owner approves it from her phone and it is live before lunch.', who: 'Kopi Senja', role: 'Demo company in rangka-apps' },
  { kicker: 'Example scenario', text: 'A distributor moves from spreadsheets to the sales app. Customers, items and prices are imported, PPN is calculated the way their tax consultant expects, and nothing is custom code.', who: 'Distributor template', role: 'Planned in rangka-apps' },
  { kicker: 'Example scenario', text: 'A partner deploys a POS template for a client in an afternoon, then hands the owner Rangka AI to keep adjusting it. The partner reviews high-risk change sets.', who: 'Partner deployment', role: 'Rangka Cloud, later' },
]
const sc = ref(0)

const faqs = [
  ['Is Rangka OS really open source?', 'Yes. The kernel, schemas, validator, Console, apps, connectors and Rangka AI are public on GitHub. Only the hosted Rangka Cloud is closed.'],
  ['Is it ready for production?', 'Not yet. This is a v0 preview that runs end to end on a demo company. Several kernel parts use in-tree stand-ins that will be replaced; see the roadmap.'],
  ['Do I need a developer to customize it?', 'No for apps: models, rules, screens and dashboards are .rk files, changed by owners or Rangka AI through change sets. Yes if you want to work on the Go kernel.'],
  ['How do you stop AI from breaking things?', 'AI can only propose. Every change set is validated, previewed on a copy with its SQL, and approved by a person; high-risk changes need the owner. Applied changes can be reverted.'],
  ['Can I host it myself?', 'Yes. One Go binary and one Postgres run everything, including the queue, scheduler, realtime and cache.'],
  ['How is this different from Odoo or ERPNext?', 'Apps are plain files with schemas rather than code modules, changes are reviewable diffs, and AI is a guarded, first-class way to change them.'],
]
const open = ref<number | null>(0)
</script>

<template>
  <div class="tw">
    <!-- header -->
    <header class="tw-head">
      <a class="tw-logo" :href="b('/')" aria-label="Rangka OS home"><img :src="b('/raka.svg')" alt="" width="30" height="30" /></a>
      <nav class="tw-links">
        <a :href="b('/guide/what-is-rangka')">Guide</a><i></i>
        <a :href="b('/concepts/models')">Concepts</a><i></i>
        <a :href="b('/reference/')">Reference</a><i></i>
        <a :href="b('/ecosystem/apps')">Ecosystem</a>
      </nav>
      <div class="tw-right">
        <a class="tw-chip" :href="gh" target="_blank" rel="noopener">
          <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path fill="currentColor" d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>
          <span>{{ stars ?? 'GitHub' }}</span><em>↗</em>
        </a>
        <button class="tw-theme" type="button" :aria-label="isDark ? 'Switch to light mode' : 'Switch to dark mode'" @click="isDark = !isDark">
          <svg v-if="isDark" viewBox="0 0 24 24" width="16" height="16"><path fill="currentColor" d="M12 7a5 5 0 100 10 5 5 0 000-10zm0-5l1 3h-2zm0 20l-1-3h2zM2 12l3-1v2zm20 0l-3 1v-2zM4.9 4.9l2.8 1.4-1.4 1.4zm14.2 14.2l-2.8-1.4 1.4-1.4zM4.9 19.1l1.4-2.8 1.4 1.4zM19.1 4.9l-1.4 2.8-1.4-1.4z"/></svg>
          <svg v-else viewBox="0 0 24 24" width="16" height="16"><path fill="currentColor" d="M21 14.5A9 9 0 019.5 3a9 9 0 1011.5 11.5z"/></svg>
        </button>
        <a class="tw-btn ghost sm" :href="b('/guide/what-is-rangka')"><span>Docs</span></a>
        <a class="tw-btn sm" :href="b('/guide/quick-start')"><span>Get started</span></a>
      </div>
    </header>

    <!-- hero -->
    <section class="tw-hero">
      <h1>The business OS <br /><span>your AI can safely change</span></h1>
      <p>Rangka OS gives businesses the building blocks for CRM, sales, POS and ERP as readable .rk files, and keeps adapting as the business changes.</p>
      <div class="tw-ctas">
        <a class="tw-btn" :href="b('/guide/quick-start')"><span>Get started</span></a>
        <a class="tw-btn ghost" :href="gh" target="_blank" rel="noopener"><span>Star on GitHub</span></a>
      </div>
      <div class="tw-window">
        <div class="tw-window-bar"><i></i><i></i><i></i><span>Rangka</span></div>
        <img v-show="!isDark" :src="b('/screens/dashboard-light.webp')" alt="Rangka CRM dashboard generated from .rk files" width="2000" height="1250" />
        <img v-show="isDark" :src="b('/screens/dashboard-dark.webp')" alt="Rangka CRM dashboard in dark mode" width="2000" height="1250" />
      </div>
    </section>

    <!-- built on strip -->
    <section class="tw-strip">
      <div class="tw-strip-row">
        <span class="tw-mono">Built on</span>
        <ul><li>PostgreSQL</li><li>Go</li><li>React</li><li>OpenID Connect</li><li>MCP</li><li>JSON Schema</li></ul>
        <a class="tw-mono" :href="b('/guide/architecture')">+ Architecture</a>
        <b class="x tl"></b><b class="x tr"></b><b class="x bl"></b><b class="x br"></b>
      </div>
    </section>

    <!-- problem -->
    <section class="tw-problem">
      <div class="tw-problem-art"><img :src="b('/art/frame.png')" alt="" loading="lazy" /></div>
      <div class="tw-problem-text">
        <p class="tw-eyebrow"><i></i>The problem.</p>
        <h2>Business software gives you an edge, <span>but fitting it to your business comes with tradeoffs</span></h2>
        <dl>
          <div><dt>Off-the-shelf apps</dt><dd>You bend the shop to the software. A missing field becomes a spreadsheet on the side, and the real process lives in WhatsApp.</dd></div>
          <div><dt>Custom builds</dt><dd>Version one ships, then every change waits for a developer and risks breaking something nobody remembers writing.</dd></div>
        </dl>
      </div>
    </section>

    <!-- approach: folder-tab cards -->
    <section class="tw-sec">
      <p class="tw-eyebrow"><i></i>Stop settling for tradeoffs.</p>
      <h2 class="tw-h2-left">Assemble, change and own your business apps, <span>with AI that asks first</span></h2>
      <p class="tw-lead">Declare apps in files, change them through reviewed proposals, run them on infrastructure you own.</p>
      <div class="tw-tabs">
        <article v-for="t in tabs" :key="t.title" class="tw-tabcard">
          <div class="tw-tab"><span>{{ t.title }}</span></div>
          <div class="tw-tabbody">
            <div class="tw-icon"><img :src="b('/art/icon-' + t.icon + '.png')" alt="" loading="lazy" /></div>
            <p>{{ t.text }}</p>
            <div class="tw-tabfoot">
              <span><b>{{ t.label }}</b><i></i>{{ t.linkText }}</span>
              <a class="tw-arrow" :href="b(t.link)" :aria-label="'Read about ' + t.linkText">→</a>
            </div>
          </div>
        </article>
      </div>
    </section>

    <!-- sticky numbered pillars -->
    <section class="tw-pillars">
      <div class="tw-pillars-text">
        <div class="tw-rail" aria-hidden="true">
          <span v-for="(s, i) in steps" :key="s.n" :class="{ on: step === i }">{{ s.n }}</span>
        </div>
        <div v-for="(s, i) in steps" :key="s.n" :ref="(el) => { if (el) stepEls[i] = el as HTMLElement }" :data-i="i" class="tw-step" :class="{ on: step === i }">
          <h2>{{ s.a }} <br /><span>{{ s.b }} {{ s.c }}</span></h2>
          <p>{{ s.text }}</p>
          <a class="tw-link" :href="b(s.link)">{{ s.cta }} →</a>
          <div class="tw-step-mobile">
            <img v-if="s.shot" :src="b('/screens/' + s.shot + '.webp')" alt="" loading="lazy" />
          </div>
        </div>
      </div>
      <div class="tw-pillars-art">
        <div class="tw-sticky">
          <img class="tw-field" :src="b('/art/field.png')" alt="" />
          <template v-for="(s, i) in steps" :key="s.n">
            <div class="tw-float" :class="{ on: step === i }">
              <img v-if="s.shot" :src="b('/screens/' + s.shot + '.webp')" :alt="s.a + ' ' + s.b + ' ' + s.c" loading="lazy" />
              <div v-else class="tw-term">
<pre><span class="c"># check every .rk file against the schemas</span>
<b>$</b> rangka validate --apps ../rangka-apps

<span class="c"># DATABASE_URL points at your Postgres</span>
<b>$</b> rangka migrate --apps ../rangka-apps
<b>$</b> rangka seed --apps ../rangka-apps --org kopi-senja

<span class="c"># REST, SSE, MCP and Rangka ID on one port</span>
<b>$</b> rangka serve --apps ../rangka-apps --addr :8080</pre>
              </div>
            </div>
          </template>
        </div>
      </div>
    </section>

    <!-- feature grid -->
    <section class="tw-sec">
      <p class="tw-eyebrow"><i></i>For the people who run the business.</p>
      <h2 class="tw-h2-left">Give your team apps <span>they’ll actually use</span></h2>
      <div class="tw-feats">
        <article v-for="f in features" :key="f.title" class="tw-feat">
          <div class="tw-feat-shot"><img :src="b('/screens/' + f.shot + '.webp')" :style="{ '--o': f.pos }" alt="" loading="lazy" /></div>
          <h3><span class="tw-ficon"><svg viewBox="0 0 24 24" width="12" height="12"><path fill="currentColor" :d="f.icon" /></svg></span>{{ f.title }}</h3>
          <p>{{ f.text }}</p>
        </article>
      </div>
    </section>

    <!-- centered statement -->
    <section class="tw-center">
      <div class="tw-vline"></div>
      <p class="tw-eyebrow"><i></i>Made for Indonesia.</p>
      <h2>Built for how <br /><span>Indonesian businesses run</span></h2>
      <ul class="tw-facts">
        <li><b>Rp</b><span>Rupiah without decimals</span></li>
        <li><b>12%</b><span>PPN on DPP 11/12</span></li>
        <li><b>QRIS</b><span>and WhatsApp connectors</span></li>
        <li><b>ID</b><span>Bahasa Indonesia first</span></li>
      </ul>
    </section>

    <!-- scenarios carousel -->
    <section class="tw-quote">
      <div class="tw-quote-art"><img :src="b('/art/icon-blocks.png')" alt="" loading="lazy" /><span class="tw-count">{{ sc + 1 }}/{{ scenarios.length }}</span></div>
      <div class="tw-quote-body">
        <p class="tw-eyebrow"><i></i>{{ scenarios[sc].kicker }}</p>
        <blockquote>{{ scenarios[sc].text }}</blockquote>
        <div class="tw-quote-foot">
          <div class="tw-nav">
            <button type="button" aria-label="Previous scenario" :disabled="sc === 0" @click="sc--">←</button>
            <button type="button" aria-label="Next scenario" :disabled="sc === scenarios.length - 1" @click="sc++">→</button>
          </div>
          <div class="tw-who"><b>{{ scenarios[sc].who }}</b><span>{{ scenarios[sc].role }}</span></div>
        </div>
      </div>
    </section>

    <!-- dark: CTA + FAQ -->
    <section class="tw-dark">
      <img class="tw-dark-art" :src="b('/art/field-dark.png')" alt="" loading="lazy" />
      <div class="tw-dark-inner">
        <p class="tw-eyebrow light"><i></i>Any questions?</p>
        <h2>Stop bending your business. <br /><span>Start building, with Rangka</span></h2>
        <div class="tw-ctas left">
          <a class="tw-btn light" :href="b('/guide/quick-start')"><span>Get started</span></a>
          <a class="tw-btn ghost dark" :href="gh + '/issues'" target="_blank" rel="noopener"><span>Ask on GitHub</span></a>
        </div>
        <div class="tw-faq">
          <div v-for="(f, i) in faqs" :key="i" class="tw-q" :class="{ open: open === i }">
            <button type="button" :aria-expanded="open === i" @click="open = open === i ? null : i">
              <span class="tw-glyph">▭</span><span class="tw-qt">{{ f[0] }}</span><span class="tw-plus">{{ open === i ? '−' : '+' }}</span>
            </button>
            <p v-show="open === i">{{ f[1] }}</p>
          </div>
        </div>
      </div>
    </section>

    <!-- footer -->
    <footer class="tw-foot">
      <div class="tw-foot-art"><img :src="b('/art/wordmark.png')" alt="" loading="lazy" /></div>
      <div class="tw-foot-card">
        <img :src="b('/raka.svg')" alt="Rangka OS" width="36" height="36" />
        <div class="tw-foot-cols">
          <div><h4>Learn</h4><a :href="b('/guide/what-is-rangka')">What is Rangka OS</a><a :href="b('/guide/quick-start')">Quick start</a><a :href="b('/guide/first-app')">Your first app</a><a :href="b('/concepts/models')">Concepts</a><b class="p">+</b></div>
          <div><h4>Reference</h4><a :href="b('/reference/schema/')">Schemas</a><a :href="b('/reference/cli')">CLI</a><a :href="b('/reference/rest-api')">REST API</a><a :href="b('/reference/mcp')">MCP tools</a><b class="p">+</b></div>
          <div><h4>Project</h4><a :href="b('/roadmap')">Roadmap</a><a :href="b('/contributing')">Contributing</a><a :href="b('/ecosystem/cloud')">Rangka Cloud</a><b class="p">+</b></div>
          <div><h4>Connect</h4><a href="https://github.com/rangka-os" target="_blank" rel="noopener">GitHub</a>
            <div class="tw-foot-btns"><a class="tw-btn sm" :href="b('/guide/quick-start')"><span>Get started</span></a><a class="tw-btn ghost sm" :href="b('/guide/what-is-rangka')"><span>Read the docs</span></a></div>
          </div>
        </div>
        <div class="tw-foot-bottom"><span>© 2026 · Rangka contributors · AGPL-3.0 (planned)</span><span>Fondasi bisnismu, dibangun bersama AI.</span></div>
      </div>
    </footer>
  </div>
</template>
