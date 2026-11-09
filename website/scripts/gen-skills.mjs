// Generates the Skills pages (contract 0035) from the SKILL.md files: rangka-os skills/, every app's and module's
// skills/ in the sibling rangka-apps checkout, and rangka-connectors/skills. One page per skill plus the table that
// skills/index.md includes. Runs before every dev/build, like gen-reference.mjs, so the docs never drift from the skills.
import { readFileSync, writeFileSync, readdirSync, mkdirSync, existsSync, statSync, rmSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..', '..')
const ws = process.env.RANGKA_WORKSPACE || join(root, '..') // the folder holding rangka-apps and rangka-connectors
const out = join(here, '..', 'skills')
const GH = 'https://github.com/rangka-os'

// Every place skills live, in the kernel's order: platform, apps, modules, connectors. First name wins.
const folders = [{ dir: join(root, 'skills'), source: 'rangka-os', repo: 'rangka-os', rel: 'skills' }]
for (const [kind, base] of [['app', 'apps'], ['module', 'modules']]) {
  const d = join(ws, 'rangka-apps', base)
  if (!existsSync(d)) continue
  for (const u of readdirSync(d).sort()) folders.push({ dir: join(d, u, 'skills'), source: `${kind}:${u}`, repo: 'rangka-apps', rel: `${base}/${u}/skills` })
}
folders.push({ dir: join(ws, 'rangka-connectors', 'skills'), source: 'connectors', repo: 'rangka-connectors', rel: 'skills' })

function parse(text) {
  const t = text.replace(/\r\n/g, '\n')
  if (!t.startsWith('---\n')) return null
  const end = t.indexOf('\n---\n', 4)
  if (end < 0) return null
  const front = {}
  // the frontmatter is flat: name, description (often quoted), metadata as one flow map
  for (const line of t.slice(4, end).split('\n')) {
    const m = /^([a-z_]+):\s*(.*)$/.exec(line)
    if (!m) continue
    let v = m[2].trim()
    if (v.startsWith('"') && v.endsWith('"')) v = JSON.parse(v)
    else if (v.startsWith("'") && v.endsWith("'")) v = v.slice(1, -1).replace(/''/g, "'")
    front[m[1]] = v
  }
  return { front, body: t.slice(end + 5) }
}

const skills = []
const seen = new Set()
for (const f of folders) {
  if (!existsSync(f.dir) || !statSync(f.dir).isDirectory()) continue
  for (const name of readdirSync(f.dir).sort()) {
    const md = join(f.dir, name, 'SKILL.md')
    if (!existsSync(md)) continue
    const p = parse(readFileSync(md, 'utf8'))
    if (!p || !p.front.name || seen.has(p.front.name)) continue
    seen.add(p.front.name)
    skills.push({ ...p, source: f.source, url: `${GH}/${f.repo}/tree/main/${f.rel}/${name}` })
  }
}

// Clean out last run's generated pages (the hand-written ones stay).
const kept = new Set(['index.md', 'install.md', 'writing.md'])
mkdirSync(out, { recursive: true })
for (const f of readdirSync(out)) if (f.endsWith('.md') && !kept.has(f)) rmSync(join(out, f))

const esc = s => String(s ?? '').replace(/\|/g, '\\|').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\n/g, ' ')
const firstSentence = s => (/^(.+?\.)(\s|$)/.exec(s)?.[1] ?? s)

for (const s of skills) {
  // links inside a skill are relative to its folder: point them at the file on GitHub
  const body = s.body.replace(/\]\((?!https?:|mailto:|#)([^)\s]+)\)/g, (_, target) => `](${s.url.replace('/tree/', '/blob/')}/${target})`)
  const page = [
    '---',
    `title: ${s.front.name}`,
    'editLink: false',
    '---',
    '',
    `# ${s.front.name}`,
    '',
    `> ${s.front.description}`,
    '',
    `Source: \`${s.source}\` · [Folder on GitHub](${s.url}) · Install: [all harnesses](./install)`,
    '',
    '::: v-pre',
    body.replace(/^\s*#\s.*\n+/, ''), // the skill's own title duplicates the page title
    ':::',
    ''
  ].join('\n')
  writeFileSync(join(out, `${s.front.name}.md`), page)
}

const table = [
  '| Skill | From | Use it for |',
  '| --- | --- | --- |',
  ...skills.map(s => `| [${s.front.name}](./${s.front.name}) | \`${s.source}\` | ${esc(firstSentence(s.front.description))} |`)
].join('\n')
writeFileSync(join(out, '_list.md'), table + '\n')
writeFileSync(join(out, '_skills.json'), JSON.stringify(skills.map(s => ({ name: s.front.name, source: s.source })), null, 2) + '\n')
console.log(`skills: ${skills.length} page(s) from ${folders.filter(f => existsSync(f.dir)).length} folder(s)`)
