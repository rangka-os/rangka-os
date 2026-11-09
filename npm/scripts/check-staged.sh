#!/usr/bin/env bash
# Stages the npm packages with a locally built kernel (this platform only), scaffolds every
# @rangka-os/create template with the staged starter and runs the project's own `npm run validate`
# through the staged @rangka-os/cli launcher. Fails on any validation error or warning.
#
#   npm/scripts/check-staged.sh            # rangka-apps and rangka-connectors next to rangka-os
#   APPS_REPO=... CONNECTORS_REPO=... RANGKA_BIN=... npm/scripts/check-staged.sh
#
# The release stages six platforms from goreleaser's dist/; this check stages one, which is enough to
# catch a starter that no longer matches the rangka-apps layout (what failed v0.5.0-alpha.1).
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
APPS_REPO=${APPS_REPO:-$(dirname "$REPO")/rangka-apps}
CONNECTORS_REPO=${CONNECTORS_REPO:-$(dirname "$REPO")/rangka-connectors}
RANGKA_BIN=${RANGKA_BIN:-$REPO/kernel/bin/rangka}
# modules:<a,b> scaffolds a composed site with --modules a,b (contract 0032) instead of a template.
TEMPLATES=${TEMPLATES:-"demo blank modules:contacts,invoicing"}
VERSION=0.0.0-check

if [ ! -x "$RANGKA_BIN" ]; then make -C "$REPO/kernel" build >&2; fi

# npm's platform key for this machine, as stage.mjs names the packages (darwin-arm64, linux-x64, ...).
read -r PLATFORM GOPLAT < <(node -e 'const g={darwin:"darwin",linux:"linux",win32:"windows"}[process.platform], c={x64:"amd64",arm64:"arm64"}[process.arch]; console.log(`${process.platform}-${process.arch} ${g}-${c}`)')

WORK=$(mktemp -d "${TMPDIR:-/tmp}/rangka-check-staged-XXXXXX")
[ -n "${KEEP:-}" ] || trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bins"
cp "$RANGKA_BIN" "$WORK/bins/rangka-$GOPLAT"

echo "== stage ($PLATFORM only)"
node "$REPO/npm/scripts/stage.mjs" --version "$VERSION" --bin-dir "$WORK/bins" --out "$WORK/out" \
  --apps-repo "$APPS_REPO" --connectors-repo "$CONNECTORS_REPO"
[ -x "$WORK/out/cli-$PLATFORM/bin/rangka" ] || { echo "check-staged: no staged binary for $PLATFORM" >&2; exit 1; }

status=0
for tpl in $TEMPLATES; do
  dir="$WORK/project-${tpl%%:*}"
  if [[ $tpl == modules:* ]]; then
    echo "== create --modules ${tpl#modules:}"
    node "$WORK/out/create/index.js" "$dir" --modules "${tpl#modules:}" --name "Toko Cek" --email owner@cek.test --yes >/dev/null
    if grep -q '^apps:' "$dir/site/config.rk" || ! grep -q '^modules: {' "$dir/site/config.rk"; then
      echo "check-staged: $tpl did not write config.modules without apps" >&2; status=1
    fi
  else
    echo "== create --template $tpl"
    node "$WORK/out/create/index.js" "$dir" --template "$tpl" --name "Toko Cek" --email owner@cek.test --yes >/dev/null
  fi
  for d in modules apps connectors site fixtures; do
    [ -d "$dir/$d" ] || { echo "check-staged: $tpl has no $d/" >&2; status=1; }
  done
  # What `npm install` would put in node_modules, without the registry: the staged launcher and binary.
  mkdir -p "$dir/node_modules/@rangka-os" "$dir/node_modules/.bin"
  cp -R "$WORK/out/cli" "$dir/node_modules/@rangka-os/cli"
  cp -R "$WORK/out/cli-$PLATFORM" "$dir/node_modules/@rangka-os/cli-$PLATFORM"
  ln -s ../@rangka-os/cli/bin/rangka.js "$dir/node_modules/.bin/rangka"
  echo "== $tpl: npm run validate"
  if ! out=$(cd "$dir" && npm run --silent validate 2>&1); then status=1; fi
  echo "$out"
  # The validator prints "N error(s), M warning(s)"; any error or warning fails the check, except
  # catalogue_summary_missing (contract 0036): rangka-apps and rangka-connectors add summaries after the schema ships.
  if ! grep -q '^0 error(s), ' <<<"$out" || grep -E '  warn  ' <<<"$out" | grep -vq 'catalogue_summary_missing'; then
    echo "check-staged: $tpl is not clean" >&2
    status=1
  fi
done

if [ "$status" -eq 0 ]; then echo "check-staged: ok ($TEMPLATES)"; else echo "check-staged: failed" >&2; fi
exit "$status"
