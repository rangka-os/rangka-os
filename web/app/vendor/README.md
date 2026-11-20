# Vendored libraries

The app loads these from here, not from a CDN, so it works without internet access to cdnjs and runs exactly the
bytes reviewed in this repo.

| File | Library | Version | Source | Licence |
| --- | --- | --- | --- | --- |
| `react.production.min.js` | React (UMD, production) | 18.3.1 | https://cdnjs.cloudflare.com/ajax/libs/react/18.3.1/umd/react.production.min.js (npm `react@18.3.1`, `umd/`) | MIT, Copyright (c) Facebook, Inc. and its affiliates |
| `react-dom.production.min.js` | ReactDOM (UMD, production) | 18.3.1 | https://cdnjs.cloudflare.com/ajax/libs/react-dom/18.3.1/umd/react-dom.production.min.js (npm `react-dom@18.3.1`, `umd/`) | MIT, Copyright (c) Facebook, Inc. and its affiliates |
| `zxing.min.js` | ZXing barcode reader (UMD) | 0.21.3 | npm `@zxing/library@0.21.3`, `umd/index.min.js`, with a one-line licence header added | Apache-2.0, text in `zxing.LICENSE` |
| `mermaid.min.js` | Mermaid (minified bundle) | 11.4.1 | https://registry.npmjs.org/mermaid/-/mermaid-11.4.1.tgz, `package/dist/mermaid.min.js` | MIT, text in `mermaid.LICENSE` |
| `leaflet.min.js`, `leaflet.css` | Leaflet | 1.9.4 | https://registry.npmjs.org/leaflet/-/leaflet-1.9.4.tgz, `package/dist/leaflet.js` and `leaflet.css` | BSD-2-Clause, text in `leaflet.LICENSE` |
| `leaflet.markercluster.min.js` (+ `MarkerCluster.css` appended to `leaflet.css`) | Leaflet.markercluster | 1.5.3 | https://registry.npmjs.org/leaflet.markercluster/-/leaflet.markercluster-1.5.3.tgz, `package/dist/leaflet.markercluster.js` | MIT, text in `leaflet.markercluster.LICENSE` |

`mermaid.min.js` (2.5 MB) is loaded by the design system's `Diagram` on first use, after the host calls `Rangka.configureDiagrams({src})`. It is not precached by the service worker.

Leaflet (rangka-os#248, contract 0072 §1) is not loaded by `index.html`: `map.js` (`RangkaApp.loadLeaflet()`) adds
`leaflet.css`, `leaflet.min.js` and `leaflet.markercluster.min.js` on the first map, and the service worker precaches
them for offline use. Changes from the upstream files: the trailing `//# sourceMappingURL=` line is removed from both
scripts (the maps are not vendored); in `leaflet.css` the three `url(images/*.png)` references are replaced with
`data:` URIs of the same PNGs from `package/dist/images/`, and `MarkerCluster.css` (1.5.3) is appended. The app does
not use Leaflet's PNG marker icons (pins are `divIcon`s styled with tokens in `app.css`).

`zxing.min.js` is not loaded by `index.html`: `scan.js` loads it on first need, only where the browser has no
`BarcodeDetector` (iPhone Safari, Firefox); the service worker precaches it for offline use.

SHA-256:

```
d949f1c3687aedadcedac85261865f29b17cd273997e7f6b2bfc53b2f9d4c4dd  react.production.min.js
35f4f974f4b2bcd44da73963347f8952e341f83909e4498227d4e26b98f66f0d  react-dom.production.min.js
83a81fe1abc1288034c5663f946d7cd60929b79ad6d3f4b32d7be44e5b4d6cde  zxing.min.js
a43bc1afd446f9c4cc66ac5dd45d02e8d65e26fc5344ec0ef787f88d6ddb6f9e  mermaid.min.js
dc71f8a6880bc3ca1bd9fa8dc5f1af48c702dc510b0a78240a07c5feed7ce935  leaflet.min.js
b1ef34ccee0fb36d856ff4719c759046a0e14293dbbf9d531d969735481168bc  leaflet.markercluster.min.js
20c2a9f409d54459d98e502a94b7affe696fb8d7ac564e2787e8913503a2eb78  leaflet.css
```

Every script keeps its licence header (`@license` or `@preserve`). To upgrade, replace the files with the UMD builds of the new version, update
this table and the checksums, and run the live suite (`python3 web/app/test_app.py`).
