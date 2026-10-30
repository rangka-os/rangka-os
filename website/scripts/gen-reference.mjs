// Generates the .rk reference pages from rangka-os/schema/0.4, the validator code list from tools/, and copies the
// generated API reference page into public/. Runs before every dev/build so the docs never drift from the schemas.
import { readFileSync, writeFileSync, readdirSync, mkdirSync, cpSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..', '..')
const schemaDir = join(root, 'schema', '0.4')
const out = join(here, '..', 'reference', 'schema')
mkdirSync(out, { recursive: true })

const schemas = Object.fromEntries(readdirSync(schemaDir).filter(f => f.endsWith('.json'))
  .map(f => [f.replace('.json', ''), JSON.parse(readFileSync(join(schemaDir, f), 'utf8'))]))

const esc = s => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\{\{/g, '&#123;&#123;').replace(/\|/g, '\\|').replace(/\n/g, ' ')
const code = s => '`' + String(s).replace(/`/g, "'") + '`'

function resolve(node, self) {
  if (!node || !node.$ref) return node
  const [file, ptr] = node.$ref.split('#')
  const doc = file ? schemas[file.replace('.json', '')] : self
  if (!doc) return node
  let cur = doc
  for (const part of (ptr || '').split('/').filter(Boolean)) cur = cur?.[part]
  return cur ? { ...cur, description: node.description || cur.description, _ref: node.$ref } : node
}

function typeOf(node, self) {
  const n = resolve(node, self)
  if (!n) return ''
  if (n._ref && /#\/\$defs\/(name|label|cel)$/.test(n._ref)) return { name: 'name', label: 'label', cel: 'CEL' }[n._ref.split('/').pop()]
  if (n.enum) return n.enum.map(v => code(v)).join(' · ')
  if (n.const !== undefined) return code(n.const)
  if (n.oneOf) return n.oneOf.map(x => typeOf(x, self)).join(' or ')
  if (n.anyOf) return n.anyOf.map(x => typeOf(x, self)).join(' or ')
  if (n.type === 'array') return `list of ${typeOf(n.items, self) || 'items'}`
  if (Array.isArray(n.type)) return n.type.join(' or ')
  if (n.type === 'object' && n.additionalProperties && typeof n.additionalProperties === 'object' && !n.properties) return `map of ${typeOf(n.additionalProperties, self)}`
  return n.type || (n.properties ? 'object' : '')
}

function details(n) {
  const bits = []
  if (n.default !== undefined) bits.push(`default ${code(JSON.stringify(n.default))}`)
  if (n.pattern) bits.push(`pattern ${code(n.pattern)}`)
  if (n.minimum !== undefined) bits.push(`min ${n.minimum}`)
  if (n.maximum !== undefined) bits.push(`max ${n.maximum}`)
  if (n.minItems) bits.push(`at least ${n.minItems}`)
  if (n.maxItems) bits.push(`at most ${n.maxItems}`)
  if (n.format) bits.push(n.format)
  return bits.join(', ')
}

function rows(obj, self, prefix, depth, acc) {
  const props = obj.properties || {}
  const req = new Set(obj.required || [])
  for (const [k, raw] of Object.entries(props)) {
    if (k === '$schema') continue
    const n = resolve(raw, self) || {}
    const path = prefix ? `${prefix}.${k}` : k
    acc.push(`| ${code(path)}${req.has(k) ? ' **required**' : ''} | ${esc(typeOf(raw, self))} | ${esc(n.description || '')}${details(n) ? ` <br><small>${esc(details(n))}</small>` : ''} |`)
    if (depth < 3) {
      if (n.type === 'object' && n.properties) rows(n, self, path, depth + 1, acc)
      const it = n.type === 'array' ? resolve(n.items, self) : null
      if (it && it.type === 'object' && it.properties && !(n.items?.$ref || '').includes('$defs/field')) rows(it, self, `${path}[]`, depth + 1, acc)
    }
  }
  return acc
}

const examples = {
  model: 'rangka-apps/apps/crm/models/deal.rk', app: 'rangka-apps/apps/crm/app.rk', dashboard: 'rangka-apps/apps/crm/dashboards/crm_overview.rk',
  flow: 'rangka-apps/apps/crm/flows/whatsapp_to_lead.rk', role: 'rangka-apps/apps/crm/roles/sales_rep.rk', template: 'rangka-apps/templates/crm/template.rk',
  connector: 'rangka-connectors/whatsapp_business/connector.rk', system: 'rangka-apps/sites/kopi-senja/system.rk', theme: 'rangka-apps/sites/kopi-senja/themes/kopi_senja.rk',
  layout: 'rangka-apps/sites/kopi-senja/layouts/crm_first.rk', features: 'rangka-apps/sites/kopi-senja/features.rk', config: 'rangka-apps/sites/kopi-senja/config.rk'
}
const where = {
  app: 'apps/<app>/app.rk', model: 'apps/<app>/models/<model>.rk', dashboard: 'apps/<app>/dashboards/<name>.rk', flow: 'apps/<app>/flows/<name>.rk',
  role: 'apps/<app>/roles/<role>.rk', template: 'templates/<name>/template.rk', connector: '<connector>/connector.rk (rangka-connectors)',
  overlay: '<site>/custom/<model>.rk', system: '<site>/system.rk', theme: '<site>/themes/<name>.rk', layout: '<site>/layouts/<name>.rk',
  features: '<site>/features.rk', config: '<site>/config.rk', changeset: 'sent to /api/v1/changesets (not a file on disk)'
}
const workspace = join(root, '..')
const index = []
for (const [kind, s] of Object.entries(schemas).sort()) {
  const lines = [`# ${kind}.rk`, '', esc(s.description || s.title || ''), '', `**Lives in** ${code(where[kind] || '')} · **Schema** [${kind}.json](https://rangka-os.github.io/rangka-os/schema/0.4/${kind}.json) ([source](https://github.com/rangka-os/rangka-os/blob/main/schema/0.4/${kind}.json)) · first line: ${code(`# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/${kind}.json`)}`, '']
  lines.push('## Properties', '', '| Property | Type | Meaning |', '| --- | --- | --- |', ...rows(s, s, '', 0, []), '')
  for (const [dn, d] of Object.entries(s.$defs || {})) {
    if (!d.properties || ['label'].includes(dn)) continue
    lines.push(`## \`${dn}\``, '', esc(d.description || ''), '', '| Property | Type | Meaning |', '| --- | --- | --- |', ...rows(d, s, '', 1, []), '')
  }
  const ex = examples[kind] && join(workspace, examples[kind])
  if (ex && existsSync(ex)) {
    const text = readFileSync(ex, 'utf8').split('\n').slice(0, 60).join('\n')
    lines.push('## Example', '', `From [${examples[kind]}](https://github.com/rangka-os/${examples[kind].split('/')[0]}/blob/main/${examples[kind].split('/').slice(1).join('/')}) (first 60 lines):`, '', '```yaml', text.trimEnd(), '```', '')
  }
  lines.push('_Generated from the schema by `website/scripts/gen-reference.mjs`. Edit the schema, not this page._', '')
  writeFileSync(join(out, `${kind}.md`), lines.join('\n'))
  index.push(`| [${kind}.rk](./${kind}) | ${code(where[kind] || '')} | ${esc((s.description || s.title || '').split('. ')[0])} |`)
}
writeFileSync(join(out, 'index.md'), ['# .rk schemas', '', 'Every kind of `.rk` file, generated from `schema/0.4`.', '', '| Kind | Lives in | What it is |', '| --- | --- | --- |', ...index, ''].join('\n'))

// validator codes
const py = readFileSync(join(root, 'tools', 'rangka_validate.py'), 'utf8') + readFileSync(join(root, 'tools', 'rkfile.py'), 'utf8')
const codes = new Map()
for (const m of py.matchAll(/\.add\(\s*["'](error|warn)["']\s*,\s*[^,()]+(?:\([^)]*\))?\s*,\s*[^,]+,\s*["']([a-z][a-z_]+)["']/g)) if (!codes.has(m[2])) codes.set(m[2], m[1])
for (const m of py.matchAll(/RkError\([^)]*?["']([a-z_]+)["']\s*,/g)) if (!codes.has(m[1])) codes.set(m[1], 'error')
for (const m of py.matchAll(/problems\.append\(\([^,]+,\s*["']([a-z_]+)["']/g)) if (!codes.has(m[1])) codes.set(m[1], 'error')
const vlines = ['# Validator codes', '', 'Every problem prints as `file:line:col  level  code  path  message`; the Go kernel and `tools/rangka_validate.py` print byte-identical reports.', '',
  '| Code | Level |', '| --- | --- |', ...[...codes.entries()].sort().map(([c, l]) => `| ${code(c)} | ${l} |`), '',
  '_Generated from `tools/rangka_validate.py` and `tools/rkfile.py`._', '']
writeFileSync(join(here, '..', 'reference', 'validator-codes.md'), vlines.join('\n'))

// generated CRM API reference page
const site = join(root, 'docs', 'site')
if (existsSync(site)) cpSync(site, join(here, '..', 'public', 'api', 'crm'), { recursive: true })

// Publish the JSON Schemas with the site, so the first line of every .rk file
// (# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/<kind>.json) resolves.
cpSync(schemaDir, join(here, '..', 'public', 'schema', '0.4'), { recursive: true })
console.log(`reference: ${Object.keys(schemas).length} schemas, ${codes.size} validator codes`)
