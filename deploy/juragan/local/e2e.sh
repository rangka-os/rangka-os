#!/usr/bin/env bash
# Local end to end test of the juragan.ai stack. Builds rangka:local, runs the compose stack with the
# local override (tls internal, *.localhost, ports 8081/8443) on the kopi-senja sample site, then
# asserts. Sections are functions run in order by main; later sections append below.
#   local/e2e.sh            run everything, tear down on exit
#   KEEP=1 local/e2e.sh     leave the stack running
#   RANGKA_APPS_REPO=…      path to the rangka-apps checkout (default: sibling of the main rangka-os clone)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY="$(dirname "$HERE")"
REPO="$(cd "$DEPLOY/../.." && pwd)"
WORK="$HERE/work"
PROJECT=juragan-e2e
HTTP_PORT=8081
HTTPS_PORT=8443
JASA_HOST=jasa.localhost
FNB_HOST=fnb.localhost
OWNER_EMAIL=owner@e2e.localhost

APPS_REPO="${RANGKA_APPS_REPO:-$(cd "$(git -C "$REPO" rev-parse --path-format=absolute --git-common-dir)/../.." && pwd)/rangka-apps}"

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ok   $*"; }
bad() { FAIL=$((FAIL + 1)); echo "  FAIL $*" >&2; }
check() { # check <description> <command...>
  local d="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$d"; else bad "$d"; fi
}

dc() {
  docker compose -p "$PROJECT" --env-file "$WORK/.env" \
    -f "$DEPLOY/compose.yaml" -f "$DEPLOY/local/compose.local.yaml" "$@"
}

cleanup() {
  local rc=$?
  if [ "${KEEP:-0}" = 1 ]; then
    echo "KEEP=1: stack left running (project $PROJECT)"
  else
    dc down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  echo "passed $PASS, failed $FAIL"
  [ "$rc" -ne 0 ] || [ "$FAIL" -eq 0 ] || rc=1
  exit "$rc"
}

# curl against a pilot host on the local HTTPS port, self-signed cert accepted.
https() { # https <host> <curl args...>
  local host="$1"; shift
  curl -sk --resolve "$host:$HTTPS_PORT:127.0.0.1" "$@" "https://$host:$HTTPS_PORT${URL_PATH:-/}"
}

prepare() {
  [ -d "$APPS_REPO/apps" ] || { echo "rangka-apps not found at $APPS_REPO (set RANGKA_APPS_REPO)" >&2; exit 2; }
  echo "== build rangka:local"
  docker build -q -t rangka:local -f "$HERE/Dockerfile.rangka" "$REPO" >/dev/null
  echo "== prepare pilot folders from $APPS_REPO"
  rm -rf "$WORK"
  mkdir -p "$WORK/pilots"
  for p in jasa fnb; do
    local d="$WORK/pilots/$p"
    mkdir -p "$d/data/files"
    cp -R "$APPS_REPO/apps" "$d/apps"
    cp -R "$APPS_REPO/fixtures" "$d/fixtures"
    cp -R "$APPS_REPO/sites/kopi-senja" "$d/site"
  done
  # the kernel runs as uid 65532 and writes site/custom and data/
  docker run --rm -v "$WORK/pilots:/p" alpine chown -R 65532:65532 /p
  OWNER_PASSWORD="$(openssl rand -hex 12)"
  cat >"$WORK/.env" <<ENV
RANGKA_TAG=local
RANGKA_IMAGE=rangka:local
PILOTS_DIR=$WORK/pilots
JASA_HOST=$JASA_HOST
FNB_HOST=$FNB_HOST
JASA_ORG=kopi-senja
FNB_ORG=kopi-senja
CADDY_TLS=internal
POSTGRES_PASSWORD=$(openssl rand -hex 16)
JASA_DB_PASSWORD=$(openssl rand -hex 16)
FNB_DB_PASSWORD=$(openssl rand -hex 16)
RANGKA_OWNER_EMAIL=$OWNER_EMAIL
RANGKA_OWNER_PASSWORD=$OWNER_PASSWORD
ENV
}

section_stack() {
  echo "== stack"

  # a missing required secret stops compose and names the variable
  grep -v '^JASA_DB_PASSWORD=' "$WORK/.env" >"$WORK/.env.missing"
  local err
  if err="$(env -u JASA_DB_PASSWORD docker compose -p "$PROJECT" --env-file "$WORK/.env.missing" \
      -f "$DEPLOY/compose.yaml" -f "$DEPLOY/local/compose.local.yaml" config 2>&1 >/dev/null)"; then
    bad "compose config without JASA_DB_PASSWORD should fail"
  elif grep -q JASA_DB_PASSWORD <<<"$err"; then
    ok "compose config without JASA_DB_PASSWORD fails and names it"
  else
    bad "compose config failed but did not name JASA_DB_PASSWORD: $err"
  fi

  dc up -d postgres >/dev/null
  local seed_out
  for p in jasa fnb; do
    if seed_out="$(dc run --rm "$p" seed 2>&1)"; then ok "seed $p"; else bad "seed $p: $seed_out"; fi
  done
  dc up -d >/dev/null

  local host code
  for host in "$JASA_HOST" "$FNB_HOST"; do
    code=000
    for _ in $(seq 1 40); do
      code="$(URL_PATH=/.well-known/openid-configuration https "$host" -o /dev/null -w '%{http_code}')" || code=000
      [ "$code" = 200 ] && break
      sleep 1
    done
    if [ "$code" = 200 ]; then ok "$host liveness 200"; else bad "$host liveness $code"; fi

    local hdr
    hdr="$(URL_PATH=/auth/login https "$host" -D - -o /dev/null -X POST -H 'Content-Type: application/json' \
      -d "{\"email\":\"$OWNER_EMAIL\",\"password\":\"$OWNER_PASSWORD\"}")" || hdr=""
    if grep -q '^HTTP/[0-9.]* 200' <<<"$hdr" && grep -qi '^set-cookie: rk_' <<<"$hdr"; then
      ok "$host login 200 with rk_ cookie"
    else
      bad "$host login: $(head -1 <<<"$hdr")"
    fi
  done

  # database isolation: role jasa is refused on database fnb
  local out
  out="$(dc exec -T -e PGPASSWORD="$(grep '^JASA_DB_PASSWORD=' "$WORK/.env" | cut -d= -f2)" postgres \
    psql -h localhost -U jasa -d fnb -c 'select 1' 2>&1 || true)"
  if grep -q 'permission denied for database' <<<"$out"; then ok "role jasa refused on database fnb"; else bad "isolation: $out"; fi
  out="$(dc exec -T -e PGPASSWORD="$(grep '^FNB_DB_PASSWORD=' "$WORK/.env" | cut -d= -f2)" postgres \
    psql -h localhost -U fnb -d jasa -c 'select 1' 2>&1 || true)"
  if grep -q 'permission denied for database' <<<"$out"; then ok "role fnb refused on database jasa"; else bad "isolation: $out"; fi

  # plain HTTP redirects to HTTPS
  local loc
  for host in "$JASA_HOST" "$FNB_HOST"; do
    loc="$(curl -s --resolve "$host:$HTTP_PORT:127.0.0.1" -o /dev/null -w '%{http_code} %{redirect_url}' "http://$host:$HTTP_PORT/")"
    if [[ "$loc" == 30[178]\ https://* ]]; then ok "$host http redirects to https ($loc)"; else bad "$host http redirect: $loc"; fi
  done

  # SSE through Caddy: event-stream content type, and the connection stays open (curl times out, exit 28)
  local jar="$WORK/cookies.txt" sse_rc=0 sse_hdr
  URL_PATH=/auth/login https "$JASA_HOST" -o /dev/null -c "$jar" -X POST -H 'Content-Type: application/json' \
    -d "{\"email\":\"$OWNER_EMAIL\",\"password\":\"$OWNER_PASSWORD\"}"
  sse_hdr="$(URL_PATH=/api/v1/events https "$JASA_HOST" -N --max-time 3 -b "$jar" -D - -o /dev/null)" || sse_rc=$?
  if [ "$sse_rc" = 28 ] && grep -qi '^content-type: text/event-stream' <<<"$sse_hdr"; then
    ok "SSE stays open with text/event-stream through caddy"
  else
    bad "SSE: curl exit $sse_rc, headers: $sse_hdr"
  fi

  # only Caddy publishes ports
  local running svc pub
  running="$(dc ps --status running --format '{{.Service}}' | sort | tr '\n' ' ')"
  for svc in caddy fnb jasa postgres; do
    if [[ " $running" == *" $svc "* ]]; then ok "$svc running"; else bad "$svc not running ($running)"; fi
  done
  pub="$(docker ps --filter "label=com.docker.compose.project=$PROJECT" --format '{{.Names}} {{.Ports}}' | grep -v caddy | grep -c -- '->' || true)"
  if [ "$pub" = 0 ]; then ok "only caddy publishes ports"; else bad "non-caddy services publish ports"; fi
}

main() {
  trap cleanup EXIT
  prepare
  section_stack
  [ "$FAIL" -eq 0 ]
}

main "$@"
