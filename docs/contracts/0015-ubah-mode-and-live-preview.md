# 0015 Ubah mode and live preview

Status: proposed (stream S11, 4 Nov 2026)

The end-user web app (`/app/`) gets a **Pakai / Ubah** toggle. In Ubah a person rearranges and extends the app on
screen; every edit becomes a change set. Nothing bypasses change sets: low-risk edits are applied by the same person
through the normal propose → validate → preview → approve → apply chain (they are an approver), and undo is the
change set's `revert`. Medium and high risk edits stop at `previewed` and show their dry run as a card until an
approver (the owner for high risk) decides. AI tokens still cannot approve, apply an unapproved change set or revert.

Rangka Cloud (S12) embeds the same app and the same preview.

## 1. Who sees Ubah

`GET /api/v1/meta` adds

```json
"changesets": {"propose": true, "approve": true, "approve_high": true, "auto_apply": "never"}
```

- `propose`: the caller has a browser session and holds a role in `config.ai.approvers` (owner always) or in the new
  `config.ai.proposers` (default `[]`). The toggle is shown only when `propose` is true.
- `approve`: the caller may approve medium/low change sets (a human approver). `approve_high`: the caller is `owner`.
- `auto_apply`: `config.ai.auto_apply` as configured (`never` when absent). The app does not rely on it.

A low-risk Ubah edit by someone with `approve: false` stays a pending change set like a medium one.

## 2. Where each edit goes

| Edit in Ubah | Change-set ops | Risk |
| --- | --- | --- |
| Move / resize a dashboard widget (12-column grid, `span` 1–12) | `dashboards/<d>.rk` (site dashboard, §3): create on first edit, patch after | low |
| Reorder form fields | `custom/<model>.rk` `views.form.layout` | low |
| Reorder or hide list columns | `custom/<model>.rk` `views.list.columns` | low |
| Reorder kanban columns | `custom/<model>.rk` `views.kanban.columns` | low |
| Rename a table or a field in the company's words (contract 0049 §5) | `custom/<model>.rk` `label` (the model: page title, "Buat …", widget titles) and `labels: {<field>: label}` (fields of the app model) | low |
| Rename an app's menu group or sub-menu in the company's words (contract 0049) | `custom/app.<app>.rk` `menu_labels: {<Indonesian text of the group in app.rk>: label}` | low |
| Edit the menu (reorder, rename group, hide) | `layouts/<config.layout>.rk` `nav.side` (created when the site has no layout file, with `config.rk` `layout` set) | low |
| Add a field | `custom/<model>.rk` `fields` (`x_` name) + `views.form.add` (+ `views.list.add`) | medium (high for money/stock) |
| Add a status / stage | `custom/<model>.rk` `options: {<select field>: [{value, label}]}` | medium (high on a submittable model) |
| Add an approval | `custom/<model>.rk` `approvals` (contract 0007 shape) | medium |
| Add an automation | new `flows/<name>.rk` in the site company module (contract 0016) with `on: <model>.created|updated` | medium |
| Add a table (model) | new `models/x_<name>.rk` in the site company module (contract 0016; never `apps/` or a catalogue module) + a `nav.side` entry in the layout; `permissions` copied from a sibling model (the first model of the menu group the table joins, else the first model with `permissions` of the module of the model on screen, else of the app's first menu model), none when no sibling has them | medium (high with money/stock fields); without `permissions` an extra reason says every role has full access |

An overlay that only changes `views`, `label` or `labels` is low risk whether or not the file is new (this narrows "new overlay → medium"
in the v0.1 notes to overlays that add fields, rules, approvals or options).

## 3. New `.rk` keys (schema 0.5)

**Overlay** (`custom/<model>.rk`, kind `overlay`):

- `views.form.layout`: replaces the app model's form layout (same shape as model `views.form.layout`). Fields the
  layout leaves out are not shown on the form; `views.form.add` still applies after it.
- `views.list.columns`: replaces the list columns: a list of field names (schema `listcol`, strings only);
  `views.list.add` applies after it.
- `views.kanban.columns`: the order of the kanban's `group_by` option values; values left out are hidden.
- `options: {<field>: [{value, label, color?}]}`: options appended to a `select` field of the model.
- `label` (contract 0049 §5): replaces the app model's label, so the page title, the create button and references
  read the company's word ("Mobil" for a rental of cars). The model's name stays.
- `labels: {<field>: label}` (contract 0049 §5): replaces the label of fields of the app model; field names stay.
- App menu words (contract 0049): `custom/app.<app>.rk` holds `app: <app>` and `menu_labels`, keyed by the Indonesian
  text of a group or sub-menu label as written in the app's `app.rk` (groups have no other name). Each replaces that
  label wherever the app menu is drawn (side menu, menu search, Console); the groups, their order and their models
  stay the app's, and a site layout is not needed. Low risk, like `label`. Validator codes: `name_mismatch` (`app` is
  not the file name), `unknown_app`, `unknown_group` (a key that is not a group or sub-menu of that app's menu).

Validator codes: `unknown_field` (a layout, column, `options` or `labels` key that is not a field of the merged model),
`not_select` (`options` on a field that is not `type: select`), `bad_option` (a kanban column that is not an option
of `group_by`, or an option value that already exists), `no_kanban` (`views.kanban.columns` on a model without
`views.kanban`). Go and Python report them identically.

**Site dashboards** `<site>/dashboards/<d>.rk` (kind `dashboard`, same schema as app dashboards): replaces the
installed dashboard of the same name (it keeps that dashboard's app and menu place). A name that is not an installed
dashboard is `unknown_dashboard` (error). Change-set path `dashboards/<d>.rk`, layer 6.

## 4. Live preview

All read-only, for callers of the change set's org with `meta.changesets.propose` (else 403 `permission_denied`), change set status `proposed`, `validated`, `invalid`,
`previewed` or `approved` (else 409 `bad_status`; unknown id 404 `not_found`):

| Method | Path | Answer |
| --- | --- | --- |
| GET | `/api/v1/preview?changeset={id}` | `{changeset: {id, title, status, risk, layers}, meta: <the /meta body>}` |
| GET | `/api/v1/preview/{id}/meta` | the `/meta` body as the installation would serve it with the change applied |
| GET | `/api/v1/preview/{id}/r/{model}` | as `GET /r/{model}` |
| GET | `/api/v1/preview/{id}/r/{model}/{name}` | as `GET /r/{model}/{name}` |
| GET | `/api/v1/preview/{id}/dashboards/{name}` | as `GET /dashboards/{name}` |

The kernel loads the metadata from a temporary copy of the installation with the change set's ops applied (the
same copy validate uses) and answers from the live database without migrating it: a model whose table does not
exist yet lists no records; a field whose column does not exist yet is absent from records. Writes under
`/api/v1/preview` (any method but GET) answer 405 `preview_read_only`. A change set that changes who may do what has no preview (409 `preview_permissions`): the preview would apply rights the installation does not have yet. That covers editing a role or a model's `permissions`, flipping `is_child`, removing or retyping a field of an existing model (row scope reads fields), and giving an existing child model another parent (a child uses the permissions of the first model, by name, with a `table` field of it, so a new model with such a table can take that place). An `invalid` change set whose ops do not apply answers 422 `bad_ops`. A dashboard widget over a table that does not exist yet carries `error` instead of a value. Responses carry `X-Rangka-Preview: <id>`.

The app enters preview with `#/<route>?preview=<id>` (S12 links there) and leaves it with "Keluar pratinjau". It
enters Ubah with `?mode=ubah` on the page URL (`/app/?mode=ubah#/…`) or `?ubah=1` in the hash query, when
`meta.changesets.propose` is true. A client detects the preview feature by `GET /api/v1/preview` answering
anything but 404 `no such endpoint`.

**Record-event flows.** An automation added in Ubah is a flow on `<model>.updated` (§2). Such flows run in the
writer's transaction, as the writer; the event carries the record's fields plus `model`, `name`, `op`, `record` and
`before`. The automation Ubah writes fires on the transition only
(`record.<f> == <v> && (event.before == null || event.before.<f> != <v>)`). See the docs page
[Flows and automations](../../website/concepts/flows.md).

## 5. Embedding (Rangka Cloud)

When the app runs inside a frame (`window.parent !== window`) it posts to the parent, for every change set it
creates, applies or reverts:

```json
{"type": "rangka:changeset", "event": "created|applied|reverted", "changeset": {"id": "cs_…", "title": "…", "status": "…", "risk": "low|medium|high"}}
```

`created` fires once the change set is proposed (also for one that is then applied in the same step, followed by
`applied`). The message carries no record data, so it is posted with target origin `*`.
