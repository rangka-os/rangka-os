#!/bin/sh
# Copy rangka-os/web and rangka-os/templates into the binary's bundle (internal/bundle/files) for a release
# build. `scripts/bundle-assets.sh clean` empties it again. Run from rangka-os/kernel.
set -e
cd "$(dirname "$0")/.."
B=internal/bundle/files
find "$B" -mindepth 1 -maxdepth 1 ! -name README.md -exec rm -rf {} +
[ "$1" = "clean" ] && exit 0
mkdir -p "$B/web" "$B/templates"
# The prototype's meta.js is generated from the sibling rangka-apps checkout.
if [ -d ../../rangka-apps/apps ]; then
  python3 ../tools/build_proto.py ../../rangka-apps ../web/prototype/meta.js
else
  echo "warning: ../../rangka-apps not found; the prototype is bundled without meta.js" >&2
fi
for d in console design-system prototype; do
  [ -d "../web/$d" ] || continue
  mkdir -p "$B/web/$d"
  (cd "../web/$d" && find . -type f ! -name '*.py' ! -name 'README.md' ! -name '*.pyc') | while read -r f; do
    mkdir -p "$B/web/$d/$(dirname "$f")"; cp "../web/$d/$f" "$B/web/$d/$f"
  done
done
cp -R ../templates/. "$B/templates/"
echo "bundled $(find "$B" -type f | wc -l | tr -d ' ') files into $B"
