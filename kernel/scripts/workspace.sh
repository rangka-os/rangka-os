#!/usr/bin/env bash
# Print the workspace directory that holds rangka-apps/ and rangka-connectors/: $RANGKA_WORKSPACE, else the first
# ancestor of this repo's parent that has rangka-apps/apps (works from any git worktree), else two levels up.
set -euo pipefail
if [ -n "${RANGKA_WORKSPACE:-}" ]; then echo "$RANGKA_WORKSPACE"; exit 0; fi
repo=$(cd "$(dirname "$0")/../.." && pwd)
dir=$(dirname "$repo")
while [ "$dir" != "/" ]; do
  if [ -d "$dir/rangka-apps/apps" ]; then echo "$dir"; exit 0; fi
  dir=$(dirname "$dir")
done
echo "$repo/.."
