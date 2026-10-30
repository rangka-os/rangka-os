# Validator

Two implementations print byte-identical reports: the Go kernel (`rangka validate`) and the Python reference
`tools/rangka_validate.py`. Checks run in passes: syntax (strict YAML), shape (JSON Schema for every kind) and meaning
(references across files, CEL identifiers, compute cycles, dashboards, flows, site files, overlays, theme contrast,
optional-app guards).

```bash
python3 tools/rangka_validate.py ../rangka-apps/apps --site ../rangka-apps/sites/kopi-senja --connectors ../rangka-connectors
```

<!--@include: ./validator-codes.md{4,}-->
