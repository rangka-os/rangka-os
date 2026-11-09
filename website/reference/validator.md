# Validator

Two implementations print byte-identical reports: the Go kernel (`rangka validate`) and the Python reference
`tools/rangka_validate.py`. Checks run in passes: syntax (strict YAML), shape (JSON Schema for every kind) and meaning
(references across files, modules and apps, CEL identifiers, compute cycles, dashboards, flows, approvals, number
series, imports, print formats and letterheads, processes, site files, overlays, theme contrast, optional-app
guards). One code is kernel only: `template_error`, a print template Go's `html/template` cannot parse.

```bash
python3 tools/rangka_validate.py ../rangka-apps/apps --modules ../rangka-apps/modules --site ../rangka-apps/examples/sites/kopi-senja --connectors ../rangka-connectors
```

<!--@include: ./validator-codes.md{4,}-->
