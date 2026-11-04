#!/usr/bin/env bash
# Usage: seed-orgs.sh [--env file] [--config file] [--list] <jasa|fnb> [org ...]
# Seeds the owner and each org's reference data into one pilot: every org in the bundle's site/config.rk `orgs:`.
#
# Without org arguments the orgs come from the `orgs:` keys of <PILOTS_DIR>/<pilot>/site/config.rk (block style, as
# the pilot bundles write it); with none there, the pilot's default org (JASA_ORG / FNB_ORG). Each org gets
# `rangka seed --org <org>`, with `--fixtures /srv/fixtures/<org>` when the bundle has that folder (otherwise the
# kernel's default fixtures). Seeding is idempotent: rerunning reports "already there".
#
# The owner's email and password come from RANGKA_OWNER_EMAIL / RANGKA_OWNER_PASSWORD when exported, else they are
# asked for (the password without echo). They reach the container through the environment only, never argv or .env.
#   --env file     the deploy's .env (default: .env next to this script); PILOTS_DIR and <PILOT>_ORG are read from it
#   --config file  read the org list from this config.rk instead of the pilot's
#   --list         print the orgs that would be seeded and exit
# Compose runs with --env-file <file> and the project directory of this script; COMPOSE_FILE / COMPOSE_PROJECT_NAME
# from the environment are honoured (the local e2e uses them).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="$HERE/.env"
CONFIG=""
LIST=0

log() { echo "seed-orgs: $*" >&2; }
die() { log "$*"; exit 2; }
usage() { sed -n '2p' "$0" | sed 's/^# //' >&2; exit 2; }

env_get() { # env_get <key> <default>: value from the env file
  local v
  v="$(grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true)"
  echo "${v:-$2}"
}

# orgs_in_config <config.rk>: the keys of the top-level block mapping `orgs:`, one per line. A flow-style
# `orgs: {...}` is refused rather than guessed at.
orgs_in_config() {
  awk '
    /^orgs:[[:space:]]*(#.*)?$/ { in_orgs = 1; next }
    /^orgs:/ { print "FLOW"; exit }
    in_orgs && /^[^[:space:]#]/ { exit }
    in_orgs && match($0, /^  [A-Za-z0-9_-]+:/) { k = substr($0, 3, RLENGTH - 3); print k }
  ' "$1"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --env) ENV_FILE="$2"; shift 2 ;;
    --config) CONFIG="$2"; shift 2 ;;
    --list) LIST=1; shift ;;
    -h|--help) usage ;;
    -*) die "unknown option $1" ;;
    *) break ;;
  esac
done
[ $# -ge 1 ] || usage
PILOT="$1"; shift
case "$PILOT" in jasa|fnb) ;; *) die "pilot must be jasa or fnb, not '$PILOT'" ;; esac
[ -f "$ENV_FILE" ] || die "env file not found: $ENV_FILE"

PILOTS_DIR="$(env_get PILOTS_DIR "")"
upper="$(tr '[:lower:]' '[:upper:]' <<<"$PILOT")"
default_org="$(env_get "${upper}_ORG" "$PILOT")"
[ -n "$CONFIG" ] || CONFIG="$PILOTS_DIR/$PILOT/site/config.rk"

orgs=("$@")
if [ ${#orgs[@]} -eq 0 ]; then
  [ -f "$CONFIG" ] || die "no org given and no config at $CONFIG (check PILOTS_DIR in $ENV_FILE)"
  found="$(orgs_in_config "$CONFIG")"
  [ "$found" != FLOW ] || die "$CONFIG writes orgs: in flow style; list the orgs as arguments instead"
  if [ -n "$found" ]; then
    while IFS= read -r o; do orgs+=("$o"); done <<<"$found"
  else
    log "no orgs: in $CONFIG; seeding the default org $default_org"
    orgs=("$default_org")
  fi
fi
if [ "$LIST" = 1 ]; then printf '%s\n' "${orgs[@]}"; exit 0; fi

if [ -z "${RANGKA_OWNER_EMAIL:-}" ]; then
  [ -t 0 ] || die "RANGKA_OWNER_EMAIL is not set and there is no terminal to ask"
  read -r -p 'owner email: ' RANGKA_OWNER_EMAIL
fi
if [ -z "${RANGKA_OWNER_PASSWORD:-}" ]; then
  [ -t 0 ] || die "RANGKA_OWNER_PASSWORD is not set and there is no terminal to ask"
  read -r -s -p 'owner password: ' RANGKA_OWNER_PASSWORD; echo >&2
fi
[ -n "$RANGKA_OWNER_EMAIL" ] && [ -n "$RANGKA_OWNER_PASSWORD" ] || die "owner email and password are required"
export RANGKA_OWNER_EMAIL RANGKA_OWNER_PASSWORD

compose=(docker compose --project-directory "$HERE" --env-file "$ENV_FILE")
[ -n "${COMPOSE_FILE:-}" ] || compose+=(-f "$HERE/compose.yaml")
failed=0
for org in "${orgs[@]}"; do
  args=(seed --org "$org")
  if [ -d "$PILOTS_DIR/$PILOT/fixtures/$org" ]; then
    args+=(--fixtures "/srv/fixtures/$org")
  else
    log "$org: no fixtures/$org/ in the bundle; the kernel's default fixtures are used"
  fi
  if "${compose[@]}" run --rm -T -e RANGKA_OWNER_EMAIL -e RANGKA_OWNER_PASSWORD "$PILOT" "${args[@]}"; then
    log "$org: seeded"
  else
    log "$org: seed FAILED"; failed=1
  fi
done
[ "$failed" = 0 ] || die "one or more orgs failed to seed"
log "done: ${orgs[*]}"
