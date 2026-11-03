# Vendored libraries

The app loads these from here, not from a CDN, so it works without internet access to cdnjs and runs exactly the
bytes reviewed in this repo.

| File | Library | Version | Source | Licence |
| --- | --- | --- | --- | --- |
| `react.production.min.js` | React (UMD, production) | 18.3.1 | https://cdnjs.cloudflare.com/ajax/libs/react/18.3.1/umd/react.production.min.js (npm `react@18.3.1`, `umd/`) | MIT, Copyright (c) Facebook, Inc. and its affiliates |
| `react-dom.production.min.js` | ReactDOM (UMD, production) | 18.3.1 | https://cdnjs.cloudflare.com/ajax/libs/react-dom/18.3.1/umd/react-dom.production.min.js (npm `react-dom@18.3.1`, `umd/`) | MIT, Copyright (c) Facebook, Inc. and its affiliates |

SHA-256:

```
d949f1c3687aedadcedac85261865f29b17cd273997e7f6b2bfc53b2f9d4c4dd  react.production.min.js
35f4f974f4b2bcd44da73963347f8952e341f83909e4498227d4e26b98f66f0d  react-dom.production.min.js
```

Both files keep their `@license` header. To upgrade, replace the files with the UMD builds of the new version, update
this table and the checksums, and run the live suite (`python3 web/app/test_app.py`).
