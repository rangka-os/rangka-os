#!/usr/bin/env bash
# Regenerate testdata/prototype-dashboards.json: the prototype engine's dashboard numbers for the seed data
# on 2026-10-30 (Asia/Jakarta). The Go dashboard test compares the kernel against this file.
set -euo pipefail
cd "$(dirname "$0")/.."
WS=$(cd ../.. && pwd)
TMP=$(mktemp -d)
python3 "$WS/rangka-os/tools/build_proto.py" "$WS/rangka-apps" "$TMP/meta.js" >/dev/null
TZ=Asia/Jakarta node scripts/proto-dashboards.js "$TMP/meta.js" "$WS/rangka-os/web/prototype/engine.js" > testdata/prototype-dashboards.json
rm -rf "$TMP"
echo "wrote testdata/prototype-dashboards.json"
