# .rk files

Every piece of Rangka metadata is a `.rk` file: YAML with strict rules, so a file means exactly the same thing in the
kernel, the Python tools, the browser and an AI agent.

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: lead
label: {id: Lead, en: Lead}
fields:
  - {name: phone, type: phone, label: {id: Telepon}}
  - name: status
    type: select
    default: "'new'"          # a CEL expression, so the string is quoted inside
    options:
      - {value: new, label: {id: Baru}}
```

## The rules

- **YAML 1.2 types only**: `true`/`false`, `null`, numbers. Dates stay text (`2026-10-30`).
- **Values YAML tools read differently are refused unless quoted**: `yes`, `no`, `on`, `off`, `y`, `n`, numbers with
  a leading zero (phone numbers, NPWP, item codes), `1_000`, and times like `08:30`. Keys are always text, so
  `on: save` is fine.
- **No duplicate keys, no tabs, no anchors or aliases (`&`, `*`), no merge keys (`<<`).** One document per file.
- **`${ENV}` values must be quoted inside `{ }`** — `url: '${DATABASE_URL}'` — and are the only way to give secrets.
- **The first line names the schema**, which gives VS Code (Red Hat YAML extension) completion and errors as you type.

Errors always point at a position:

```text
pipeline/models/lead.rk:12:14  error  ambiguous_value  (syntax)  yes means different things in different YAML tools; write "yes" in quotes for text, or true or false
pipeline/models/deal.rk:15:9   error  unknown_model    fields/1/to  "custommer" is not a model in this app or its dependencies; did you mean "customer"?
```

## Kinds

Schema 0.5 is current (0.4 is still published, for `app.rk` files in the old layout). It has 19 kinds; each has a
schema and a reference page.

| Kind | Where | Layer |
| --- | --- | --- |
| [module](/reference/schema/module) | `modules/<module>/module.rk` | 5 |
| [model](/reference/schema/model), [flow](/reference/schema/flow), [role](/reference/schema/role) | `models/`, `flows/`, `roles/` in a module | 5 |
| [print](/reference/schema/print), [import](/reference/schema/import), [process](/reference/schema/process) | `print/`, `imports/`, `processes/` in a module, an app or the site | 5 |
| [app](/reference/schema/app), [dashboard](/reference/schema/dashboard) | `apps/<app>/app.rk`, `apps/<app>/dashboards/` | 5 |
| [template](/reference/schema/template) | `templates/<name>/` (deprecated: an app replaces it, [contract 0001](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0001-modules-and-apps.md)) | 5 |
| [overlay](/reference/schema/overlay), [config](/reference/schema/config), [features](/reference/schema/features), [letterhead](/reference/schema/letterhead) | site (`custom/<model>.rk`, `config.rk`, `features.rk`, `letterhead.rk`) | 4 |
| [model](/reference/schema/model), [flow](/reference/schema/flow) | site `models/x_<name>.rk`, `flows/<name>.rk`: the company's own tables and automations ([contract 0016](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0016-site-company-module.md)) | 4 |
| [connector](/reference/schema/connector) | rangka-connectors | 3 |
| [system](/reference/schema/system) | site | 2 |
| [theme](/reference/schema/theme), [layout](/reference/schema/layout) | site `themes/`, `layouts/` | 6 |
| [changeset](/reference/schema/changeset) | sent to the API | — |

See [Print formats](/guide/print-formats) and [Process canvas](/guide/process-canvas).

Accepted extensions: `.rk` (preferred), `.rk.yaml`, `.rk.yml`; legacy `.json` still loads, and
`tools/rk_convert.py` converts it, refusing any file that would not read back identically.
