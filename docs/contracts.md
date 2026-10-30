# Rangka contracts (v0, 30 Oct 2026)

Shared agreements between rangka-os (kernel, Console), rangka-ai and rangka-apps. Everything here is v0: change it
only by editing this file in a pull request, and update both validators and the affected clients in the same change.

## Repositories (cloned side by side)

| Repo | Contains | Language |
| --- | --- | --- |
| `rangka-os/` | `schema/0.4/*.json` (all .rk schemas), `tools/` (Python reference validator `rangka_validate.py`, strict YAML reader `rkfile.py`, docs + prototype builders), `templates/` (theme + layout templates), `web/design-system/` (bundle.js/css, tokens.json, index.d.ts), `web/prototype/` (browser-only renderer), `docs/site/` (API docs page), `kernel/` (Go, new), `web/console/` (Console, new) | Go, JS, Python tools |
| `rangka-apps/` | `apps/{core,sales,crm}` (app.rk, models, dashboards, flows, roles), `fixtures/*.rk`, `templates/crm`, `sites/kopi-senja/` (an installation: system.rk, features.rk, config.rk, themes/, layouts/) | .rk |
| `rangka-connectors/` | `whatsapp_business/connector.rk` | .rk |
| `rangka-ai/` | Agent (new) | TypeScript (Node 20+) |

## Layers

0 deploy targets · 1 kernel · 2 system capabilities (`system.rk`) · 3 integrations (connectors) · 4 organisation
(`features.rk`, `config.rk`, orgs/members in DB) · 5 apps (`apps/*`) · 6 presentation (`themes/*.rk`, `layouts/*.rk`).
Rangka AI may write layers 4–6 through change sets only. Layer 2 changes, secrets, roles/permissions = admin approval.

## .rk files

Strict YAML (see `rangka-os/tools/rkfile.py` docstring): YAML 1.2 core types; refuse unquoted yes/no/on/off/y/n,
leading-zero numbers, `1_000`, `08:30`; refuse duplicate keys, tabs, anchors/aliases, merge keys. `${ENV}` must be
quoted inside flow mappings (`url: '${DATABASE_URL}'`). First line `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/<kind>.json`
names the kind. Kinds: app, model, dashboard, flow, system, theme, layout, features, config, changeset, (connector, role,
template, fixtures: no schema yet).

Reference implementation of "meaning" checks: `rangka-os/tools/rangka_validate.py` (run `python3 rangka-os/tools/rangka_validate.py rangka-apps/apps` → 0 errors).
The Go kernel must reproduce its error codes (`unknown_field`, `unknown_model`, `bad_field_type`, `compute_cycle`, …) and report `file:line:col`.

## CEL dialect (rules, compute, defaults, depends_on, filters, flows)

Operators `== != < <= > >= && || ! + - * / %`, ternary, `in`, member access on link fields (`customer.payment_days`
loads the linked record), list literals. Functions: `today()` (ISO date string, org timezone), `now()`,
`duration('7d')`, date ± duration → date, `round(x)`, `size(x)`, `sum(list)`, `list.map(x, expr)`,
`option('field', 'attr')` (attribute of the selected option of a select field, e.g. probability), `app_installed('sales')`,
`truncate(s, n)`, `s.contains(t)`. Globals: `user` (`user.name`, `user.roles`), `event` (flows). Dates are ISO
strings and compare as strings. Missing values are `''` for data/date/link, `0` for numbers, `false` for check.

## Records and lifecycle

Every model → table `r_<model>` with typed columns (+ `org_id`, `name` PK per org, `docstatus` 0/1/2 for submittable,
`amended_from`, `owner`, `created_at`, `modified_at`, `x` JSONB for company-layer `x_` fields). Table fields (`type: table`)
→ child table `r_<child_model>` with `parent`, `parent_field`, `idx`. Submittable: create as draft with name `DRAFT-xxxx`;
naming pattern applied at submit (`INV-{YYYY}-{#####}` per org); submitted (1) is immutable (409); cancel → 2; amend →
new draft with `amended_from`. Rules run on `save` and `submit`; `level: warn` returns warnings, error blocks.
Computed fields recomputed on every save. Status for invoices: draft, submitted, partly, paid, overdue, cancelled (see
`rangka-os/web/prototype/engine.js` `status()`).

## REST (JSON, `/api/v1`)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/meta` | `{apps:[{name,version,title,menu,dashboards}], models:{…}, dashboards:{…}, theme:{name,mode,tokens:{light:{},dark:{}}}, layout:{…}}` |
| GET | `/r/{model}` | `?filter=<CEL>&sort=-field&limit=50&offset=0&q=text` → `{data, total}` |
| POST | `/r/{model}` | create → 201 record; 422 `{errors:[{field,code,message}], warnings:[…]}` |
| GET/PATCH/DELETE | `/r/{model}/{name}` | PATCH merges; 409 when submitted |
| POST | `/r/{model}/{name}/{submit,cancel,amend}` | `submit` takes `{force:true}` to accept warnings |
| POST | `/r/{model}/{name}/actions/{action}` | runs the hook; queued if `queue:` → 202 `{job_id}` |
| GET | `/dashboards/{name}?period=30` | `{name,label,period,widgets:[{name,type,label,span, …computed}]}`; computed shape = prototype `computeWidget()` (number: value,rupiah,count,prev,trend,delta; chart: data[{key,label,value,count,color}],rupiah; list: rows,total) |
| GET | `/events` | SSE. `event: record` `{model,name,op:created|updated|deleted|submitted|cancelled,docstatus}`; `event: changeset` `{id,status}`; `event: job` `{id,kind,state}`. `?models=deal,lead` filters. Heartbeat comment every 15s. |
| GET/POST | `/changesets` | list / propose (body = changeset.json without computed fields) → 201 with `layers`, `risk`, `status: proposed` |
| GET | `/changesets/{id}` | full change set incl. history |
| POST | `/changesets/{id}/{validate,preview,approve,reject,apply,revert}` | validate → `validated`/`invalid`; preview → dry run on a copy, `preview:{migrations,affected,reversible}`; approve needs a human session with an approver role (config.ai.approvers or owner); apply only after approve (or `auto_apply: low_risk` and risk low); revert re-applies the previous file versions |
| GET | `/system/status` | for Console: `{version,uptime_s,site,capabilities:{db:{driver,ok,latency_ms},cache:{driver,ok,hits,misses,keys},queue:{driver,ok,counts:{available,running,retryable,completed,discarded}},scheduler:{driver,ok,schedules:[{name,source,cron,next_run,last_run,last_status}]},realtime:{driver,transports,connections},files:{driver,ok},search:{driver,ok},notify:{email:{driver},push:{…},whatsapp:{…}},auth:{issuer,clients:[id]}},apps:[{name,version,models,dashboards}],orgs:n,members:n}` |
| GET | `/system/jobs?state=` | queue jobs |
| GET | `/system/audit?limit=50` | `{data:[{at,actor,kind,target,summary}]}` |
| GET | `/orgs`, `/orgs/{id}/members` | organisations and members |
| GET | `/files/meta/{path}` | raw .rk text + parsed JSON (Console, AI) |

Auth: `POST /auth/login {email,password}` → `rk_session` cookie; `POST /auth/logout`; `GET /auth/me`;
`POST /api/v1/tokens` → `rk_…` bearer token. Rangka ID OIDC provider: `/.well-known/openid-configuration`,
`/oauth/authorize` (code + PKCE S256), `/oauth/token`, `/oauth/userinfo`, `/oauth/jwks` (RS256). Clients come from
`system.rk auth.clients`. Seed user: `ceo@saga.co.id` / `rangka-demo` (owner of org `kopi-senja`).

## MCP (`POST /mcp`, JSON-RPC 2.0, Streamable HTTP, bearer token)

Tools (names fixed): `rangka_meta`, `rangka_get_file {path}`, `rangka_query {model, filter?, sort?, limit?}`,
`rangka_get_record {model, name}`, `rangka_dashboard {name, period?}`, `rangka_system_status`,
`rangka_changeset_propose {title, reason, ops}`, `rangka_changeset_validate {id}`, `rangka_changeset_preview {id}`,
`rangka_changeset_status {id}`. No approve/apply tools: a person approves in Console or the app.

## Change set paths

`apps/<app>/{app,models/<m>,dashboards/<d>,flows/<f>,roles/<r>}.rk` → files under the apps dir;
`system.rk`, `features.rk`, `config.rk`, `themes/<t>.rk`, `layouts/<l>.rk` → files under the site dir;
`connectors/<c>.rk` → connectors dir. Risk: high when touching `system.rk`, roles/permissions, a `currency`/stock field,
a submittable model's existing fields, or deleting anything; medium for new fields/rules on existing models; low for
themes, layouts, labels, dashboards.

## Kernel v0 additions (30 Oct 2026, rangka-os/kernel)

Additive; nothing above changed. Clients may rely on these.

- Errors other than 422 are `{error:{code,message}}`. 422 issues are `{field?, rule?, code, message}` with codes
  `required`, `min_rows`, `rule`, `link_not_found`, `bad_option`, `bad_value`, `unique`. Successful writes may carry
  `_warnings:[…]` (save rules with `level: warn`).
- `POST …/submit` with warnings and no `force` → 422 `{errors:[], warnings:[…], needs_force:true}`.
- Submittable records carry a derived `status`; `/meta` models get `app` and, for those, `status_field: true`.
  `/meta` also returns `installed`, `org`, `user`, `today`, `config` (locale, currency, timezone, tax, plan, theme, layout)
  and `capabilities` (from the site's plan).
- Extra endpoints: `GET /files/meta` (list of paths), `GET /system/jobs/{id}`, `GET /system/audit?kind=<prefix>`,
  `POST /flows/{name}/run {event:{…}}` (run a flow now, e.g. a connector event). `GET /dashboards/{name}` sets
  `X-Cache: hit|miss`; widget objects keep their definition keys (chart, aggregate, columns, span …) next to the
  computed ones.
- Change sets: validation items are validator items `{level,file,pos,path,code,message}` with `file` as a change-set
  path; `preview` may add `notes` (schema drift). Approve, reject and revert need a browser session (not a token)
  with an approver role; applying an approved change set needs an approver (session or token). MCP proposals
  default to author kind `ai`, API tokens to `cli`, sessions to `user`.
- OIDC access tokens are accepted on `/api`; on `/mcp` they need the `rangka.mcp` scope. API tokens (`rk_…`) work on both.

## v0.1 decisions (30 Oct 2026, after the first build round)

Settles the questions raised by kernel, Console and rangka-ai. Binding for the reconciliation round.

1. **Installed apps are explicit.** `config.rk` gets `apps: [core, crm, sales]` (schema: array of app names). The plan in
   `features.rk` limits what may be listed; the kernel refuses an app the plan doesn't allow. `/meta.installed` = config.apps.
2. **Company layer = overlay files, never edits to app files.** Fields, rules and view tweaks an organisation adds live in
   `<site>/custom/<model>.rk` (kind `overlay`, new schema `overlay.json`): `model: sales_invoice`, `fields: [...]` (names must
   start with `x_`), `rules: [...]`, `views: {form: {add: [{field, after}]}, list: {add: [...]}}`. The kernel merges overlays
   onto the app model at load; app upgrades never touch them. Change-set path `custom/<model>.rk`. Rangka AI writes new
   fields/rules here, not in `apps/`. Editing `apps/**` is for app authors (risk high unless the file is new).
3. **Field flags for risk.** Field properties `money: true` (currency fields are money by default) and `stock: true` (quantity
   fields that move stock). Risk: high for changes touching money/stock fields, `system.rk`, roles/permissions, `config.tax`,
   deletions, existing fields of submittable models; medium for `features.rk`, flows, new fields/rules/overlays, `config.apps`;
   low for themes, layouts, labels, dashboards.
4. **Approval.** Approve only from `previewed`. High-risk approval only by an `owner` (kernel returns 403 `owner_required`);
   medium/low by any role in `config.ai.approvers` (default `[owner]`).
5. **/meta** adds: `flows`, `roles`, `connectors` (installed, with triggers/actions/secret field names), `site:{config, features,
   themes:[names], layouts:[names], system:{parsed, secrets redacted}}`. Every model, dashboard and flow carries `app`.
   `GET /api/v1/files` → `[{path, kind, layer, app?, lines, sha}]` (replaces `/files/meta` list; keep the old one as alias).
   `GET /api/v1/files/{path}` → `{path, kind, layer, text, parsed}`.
6. **/system/status** adds: `deploy:{target: dev|docker|helm|desktop|cloud|partner, started_at}`, `schema_version`,
   `connectors:[{name, ok, events_24h, calls_24h, errors_24h, last_event_at}]`, and `auth.clients` as
   `[{id,label,redirect_uris,scopes,public}]`.
7. **Change sets** add `created_at`, `updated_at`, and `preview.files:[{path, before, after}]` (full text, for diffs).
8. **Shapes.** job `{id, kind, queue, state, attempt, max_attempts, created_at, scheduled_at, finished_at, error, args, org}`;
   member `{id, email, name, roles[], two_factor, status, last_seen_at}`; org `{id, name, plan, usage:{users, outlets,
   records, storage_mb, ai_credits_month}}`; audit entry `{at, actor, kind, target, summary, org}` with `?kind=&actor=&org=`.
9. **MCP results.** Every tool returns `content:[{type:"text", text:<json>}]` plus `structuredContent:<json>`; errors are
   `isError:true` with `{error:{code,message}}`. `rangka_get_file` → the `GET /files/{path}` shape; `rangka_meta` → `/meta`.
10. **System columns.** The record creator is `created_by` (not `owner`), so models may define their own `owner`/`status`
    fields. Derived `status` exists only for submittable models without their own `status` field.
11. **Schemas to add:** `overlay.json`, `role.json`, `connector.json`, `template.json`; flow `steps` typed (`find`, `find_all`,
    `create`, `update`, `notify`, `call` (connector action), `if`, `wait`).
12. **Theme dark derivation.** A colour given only as a string gets its dark value by: keep hue, lightness → 100 − L·0.6 for
    backgrounds/tints (`selected`, `*-tint`), and +15 lightness for foregrounds/accents (`primary`, `brand`, `chart-*`,
    `selected-ink`, `link`), then enforce the contrast floors. Both Console and kernel use this rule.
13. **Drop** the unnamed `theme.rk`/`layout.rk` change-set paths; use `themes/<t>.rk`, `layouts/<l>.rk`.
14. **Reference validator** (`rangka-os/tools/rangka_validate.py`) must also schema-check every kind, check site files
    (config.apps vs plan, theme/layout names exist, theme contrast), overlays, treat `in` as an operator, and report
    cycles deterministically. The Go validator keeps byte-identical output.

## v0.1 implementation notes (30 Oct 2026, kernel + reference validator)

How the kernel and `rangka_validate.py` implement the v0.1 decisions where the decision left details open.
Additive; nothing above changed.

- **Validator CLI:** `rangka_validate.py <apps> [--site <dir>] [--connectors <dir>] [app …]` (Go: `rangka validate`
  with the same flags; output byte-identical). New codes: `never_runs` (warn), `unknown_event`, `unknown_connector`,
  `unknown_trigger`, `unknown_action`, `unknown_app`, `unknown_plan`, `app_not_in_plan`, `missing_dependency`,
  `unknown_theme`, `unknown_layout`, `not_installed` (warn), `unknown_template` (warn), `low_contrast`,
  `optional_model` (layout nav), `unknown_widget`; overlays reuse `unknown_model`, `unknown_field`, `duplicate_field`,
  `name_mismatch`, `compute_cycle`. `SYSTEM_FIELDS` now has `created_by` instead of `owner`.
- **Overlays:** `x_` fields are stored in the record's `x` JSONB column (no DDL). Merge order: app fields, then overlay
  fields; rules appended; `views.form.add` inserts after `after` in the section or column that holds it (else at the
  end of the last section); `views.list.add` likewise in the columns. Overlays are layer 4. Records of a model whose
  overlay is removed keep their `x_` values.
- **Dark derivation (decision 12):** colorsys HLS; `selected` and `*-tint` → L' = 100 − L·0.6, every other token → L + 15
  (max 100). Floors are enforced against the dark partner in 1 % lightness steps (foreground up, background down).
  Pairs and floors: `ink`, `ink-2`, `muted`, `link` on `surface`, `selected-ink` on `selected`, `primary-ink` on `primary`,
  `ok|warn|bad|info|discovery` on their `-tint`: 4.5:1; `primary`, `brand`, `focus`, `chart-1…6` on `surface`: 3:1.
  `low_contrast` is reported only for pairs that involve a token the theme overrides (`contrast_check: false` skips it).
  `/meta.theme` adds `overrides: {token: {light, dark, dark_derived}}`.
- **Risk details:** editing an existing `apps/**` file is high; a new app file is low (dashboard), medium (model, flow,
  app) or high (role, or a new model with money/stock fields). `config.rk`: `tax` and `ai` high; `theme`, `layout`,
  `locale` low; anything else medium. Overlays: new → medium; money/stock fields, removed fields and changed fields of
  submittable models → high; views only → low.
- **Approval:** validate → preview → approve. Approve, reject, revert need a browser session; high risk needs `owner`
  (403 `owner_required`); otherwise a role in `config.ai.approvers` (owner always). Apply of an approved change set:
  any approver (session or token). `preview.files` holds `{path, before, after}` with `null` for an absent file.
- **Lists:** `GET /files`, `/system/jobs`, `/system/audit`, `/orgs`, `/orgs/{id}/members` answer `{data:[…]}`.
  Audit `?org=` must be an org the caller belongs to (else 403). Member `roles` has one role per org in v0;
  `two_factor` is false until 2FA ships. Org `usage.storage_mb` is 0 until file uploads ship; `ai_credits_month`
  counts AI-authored change sets this month.
- **Connectors:** installed = referenced by an installed flow (`on:` or `call:`) or by `system.rk notify.*.connector`.
  Health counts audit entries `connector.event` / `connector.call` / `connector.error` with target `connector/<name>`.
  The kernel has no connector runtime yet: `call:` steps are recorded (audit), not sent.
- **Flows:** `update: <variable>` needs a record found or created earlier; `if:` + `then:`/`else:` branches; `wait:`
  only at the top level (a duration, or `{until: cel}` re-checked every minute) queues the rest as a `flow` job.
  `POST /flows/{name}/run {event}` runs a flow now (records `connector.event` for connector-triggered flows).
- **Automations:** `when:` is `<field> changes to <CEL value>`, `<field> changes`, or `created|updated|submitted|cancelled`;
  `if:` is evaluated on the record; `do:` names a hook, run in the same transaction (audit `automation.run`). Built-in
  hooks: `crm.convert_lead`, `crm.mark_won` (won deal → draft sales invoice for its customer and the first outlet, once).
- **MCP errors:** `{error:{code,message}}` plus `errors`/`warnings` for validation failures and `detail` for change-set
  errors; `structuredContent` is set on errors too.
