import { defineConfig } from 'vitepress'

// GitHub Pages serves this repo at /rangka-os/. A custom domain (e.g. rangka.dev) sets DOCS_BASE=/ in the workflow.
const base = process.env.DOCS_BASE || '/rangka-os/'
const repo = 'https://github.com/rangka-os/rangka-os'

const kinds = ['app', 'model', 'dashboard', 'flow', 'overlay', 'role', 'connector', 'template', 'system', 'theme', 'layout', 'features', 'config', 'changeset']

export default defineConfig({
  base,
  lang: 'en',
  title: 'Rangka OS',
  description: 'Open-source, declarative operating system for business apps. Every app is a .rk file; AI changes it through reviewed change sets.',
  cleanUrls: true,
  ignoreDeadLinks: 'localhostLinks',
  lastUpdated: true,
  head: [
    ['link', { rel: 'icon', href: `${base}favicon.ico`, sizes: '48x48' }],
    ['link', { rel: 'icon', href: `${base}favicon.svg`, type: 'image/svg+xml' }],
    ['link', { rel: 'apple-touch-icon', href: `${base}apple-touch-icon.png` }],
    ['link', { rel: 'manifest', href: `${base}site.webmanifest` }],
    ['meta', { name: 'theme-color', content: '#B4432A' }],
    ['meta', { property: 'og:title', content: 'Rangka OS' }],
    ['meta', { property: 'og:description', content: 'Fondasi bisnismu, dibangun bersama AI. Open-source business apps as declarative .rk files.' }]
  ],
  themeConfig: {
    logo: { light: '/brand/rangka-horizontal.svg', dark: '/brand/rangka-horizontal-reversed.svg', alt: 'Rangka' },
    siteTitle: false,
    nav: [
      { text: 'Guide', link: '/guide/what-is-rangka', activeMatch: '/guide/' },
      { text: 'Concepts', link: '/concepts/models', activeMatch: '/concepts/' },
      { text: 'Reference', link: '/reference/', activeMatch: '/reference/' },
      { text: 'Ecosystem', items: [
        { text: 'Rangka Console', link: '/tools/console' },
        { text: 'Rangka AI', link: '/tools/rangka-ai' },
        { text: 'Apps and templates', link: '/ecosystem/apps' },
        { text: 'Connectors', link: '/ecosystem/connectors' },
        { text: 'Rangka Cloud', link: '/ecosystem/cloud' }
      ] },
      { text: 'v0', items: [
        { text: 'Status and roadmap', link: '/roadmap' },
        { text: 'Contributing', link: '/contributing' }
      ] }
    ],
    sidebar: {
      '/guide/': [
        { text: 'Getting started', items: [
          { text: 'What is Rangka OS', link: '/guide/what-is-rangka' },
          { text: 'Quick start', link: '/guide/quick-start' },
          { text: 'Architecture and layers', link: '/guide/architecture' },
          { text: '.rk files', link: '/guide/rk-files' },
          { text: 'Your first app', link: '/guide/first-app' }
        ] }
      ],
      '/concepts/': [
        { text: 'Apps (layer 5)', items: [
          { text: 'Models and fields', link: '/concepts/models' },
          { text: 'Relations', link: '/concepts/relations' },
          { text: 'Documents and lifecycle', link: '/concepts/documents' },
          { text: 'Expressions (CEL)', link: '/concepts/expressions' },
          { text: 'Rules, actions, hooks', link: '/concepts/rules' },
          { text: 'Dashboards', link: '/concepts/dashboards' },
          { text: 'Flows and automations', link: '/concepts/flows' }
        ] },
        { text: 'Installation', items: [
          { text: 'System capabilities', link: '/concepts/capabilities' },
          { text: 'Organisation and overlays', link: '/concepts/organisation' },
          { text: 'Themes and layouts', link: '/concepts/presentation' },
          { text: 'Change sets', link: '/concepts/change-sets' },
          { text: 'Rangka ID and access', link: '/concepts/rangka-id' }
        ] }
      ],
      '/reference/': [
        { text: 'Reference', items: [
          { text: 'Overview', link: '/reference/' },
          { text: 'CLI', link: '/reference/cli' },
          { text: 'REST API', link: '/reference/rest-api' },
          { text: 'MCP tools', link: '/reference/mcp' },
          { text: 'Validator codes', link: '/reference/validator' },
          { text: 'Contracts (full)', link: '/reference/contracts' }
        ] },
        { text: '.rk schemas', collapsed: false, items: kinds.map(k => ({ text: `${k}.rk`, link: `/reference/schema/${k}` })) }
      ],
      '/tools/': [
        { text: 'Tools', items: [
          { text: 'Rangka Console', link: '/tools/console' },
          { text: 'Rangka AI', link: '/tools/rangka-ai' }
        ] }
      ],
      '/ecosystem/': [
        { text: 'Ecosystem', items: [
          { text: 'Apps and templates', link: '/ecosystem/apps' },
          { text: 'Connectors', link: '/ecosystem/connectors' },
          { text: 'Rangka Cloud', link: '/ecosystem/cloud' }
        ] }
      ]
    },
    socialLinks: [{ icon: 'github', link: 'https://github.com/rangka-os' }],
    editLink: { pattern: `${repo}/edit/main/website/:path`, text: 'Edit this page on GitHub' },
    search: { provider: 'local' },
    outline: { level: [2, 3] },
    footer: {
      message: 'Rangka OS is open source. Licence: AGPL-3.0 with an app exception (planned).',
      copyright: 'Rangka contributors'
    }
  }
})
