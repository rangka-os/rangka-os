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

# shellcheck source-path=SCRIPTDIR source=../backup/lib.sh
. "$DEPLOY/backup/lib.sh"

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ok   $*"; }
bad() { FAIL=$((FAIL + 1)); echo "  FAIL $*" >&2; }
same() { # same <description> <actual> <expected>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi
}
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

# psql_as <role> <db> <psql args...>: runs psql in the postgres container as a pilot role; the password goes
# through the environment (PGPASSWORD), never argv.
psql_as() {
  local role="$1" db="$2" pw
  shift 2
  pw="$(grep "^$(tr '[:lower:]' '[:upper:]' <<<"$role")_DB_PASSWORD=" "$WORK/.env" | cut -d= -f2)"
  PGPASSWORD="$pw" dc exec -T -e PGPASSWORD postgres psql -h localhost -U "$role" -d "$db" "$@"
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
    mkdir -p "$d/data/files" "$d/backups" # backups/ = RANGKA_BACKUP_DIR, must be writable by the kernel
    cp -R "$APPS_REPO/apps" "$d/apps"
    cp -R "$APPS_REPO/fixtures" "$d/fixtures"
    cp -R "$APPS_REPO/sites/kopi-senja" "$d/site"
  done
  # the kernel runs as uid 65532 and writes site/custom and data/
  docker run --rm -v "$WORK/pilots:/p" alpine:3.20 chown -R 65532:65532 /p
  OWNER_PASSWORD="$(openssl rand -hex 12)"
  OFFBOX_KEY=e2eminio
  OFFBOX_SECRET="$(openssl rand -hex 12)"
  # age keypair for the backup test: built from the backup image, identity stays under work/
  docker build -q -t juragan-backup-keygen --build-arg RANGKA_IMAGE=rangka:local "$DEPLOY/backup" >/dev/null \
    || { echo "cannot build the backup image (needed for age-keygen)" >&2; exit 2; }
  docker run --rm --entrypoint age-keygen juragan-backup-keygen >"$WORK/age-identity.txt" \
    || { echo "age-keygen failed in the backup image" >&2; exit 2; }
  AGE_RECIPIENT="$(sed -n 's/^# public key: //p' "$WORK/age-identity.txt")"
  [ -n "$AGE_RECIPIENT" ] || { echo "no age public key in $WORK/age-identity.txt" >&2; exit 2; }
  mkdir -p "$WORK/backups"
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
BACKUP_DIR=$WORK/backups
BACKUP_PING_URL=http://pinger:8080/
BACKUP_BUCKET=juragan-backups
BACKUP_AGE_RECIPIENT=$AGE_RECIPIENT
BACKUP_RCLONE_RETRIES=1
BACKUP_RCLONE_LOW_LEVEL_RETRIES=1
BACKUP_RCLONE_CONTIMEOUT=5s
RCLONE_CONFIG_OFFBOX_PROVIDER=Minio
RCLONE_CONFIG_OFFBOX_ENDPOINT=http://offbox:9000
RCLONE_CONFIG_OFFBOX_ACCESS_KEY_ID=$OFFBOX_KEY
RCLONE_CONFIG_OFFBOX_SECRET_ACCESS_KEY=$OFFBOX_SECRET
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
  out="$(psql_as jasa fnb -c 'select 1' 2>&1 || true)"
  if grep -q 'permission denied for database' <<<"$out"; then ok "role jasa refused on database fnb"; else bad "isolation: $out"; fi
  out="$(psql_as fnb jasa -c 'select 1' 2>&1 || true)"
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
  sse_hdr="$(URL_PATH=/api/v1/events https "$JASA_HOST" -N --max-time 3 -H 'Accept-Encoding: gzip, zstd' -b "$jar" -D - -o /dev/null)" || sse_rc=$?
  if [ "$sse_rc" = 28 ] && grep -qi '^content-type: text/event-stream' <<<"$sse_hdr"; then
    ok "SSE stays open with text/event-stream through caddy"
  else
    bad "SSE: curl exit $sse_rc, headers: $sse_hdr"
  fi
  if grep -qi '^content-encoding:' <<<"$sse_hdr"; then bad "SSE response is compressed"; else ok "SSE response is not compressed"; fi

  # normal responses are still compressed when asked
  local json_hdr
  json_hdr="$(URL_PATH=/.well-known/openid-configuration https "$JASA_HOST" -H 'Accept-Encoding: gzip' -D - -o /dev/null)" || json_hdr=""
  if grep -qi '^content-encoding: \(gzip\|zstd\)' <<<"$json_hdr"; then ok "JSON response is compressed"; else bad "JSON not compressed: $json_hdr"; fi

  # only Caddy publishes ports
  local running svc pub
  running="$(dc ps --status running --format '{{.Service}}' | sort | tr '\n' ' ')"
  for svc in caddy fnb jasa postgres; do
    if [[ " $running" == *" $svc "* ]]; then ok "$svc running"; else bad "$svc not running ($running)"; fi
  done
  pub="$(docker ps --filter "label=com.docker.compose.project=$PROJECT" --format '{{.Names}} {{.Ports}}' | grep -v caddy | grep -c -- '->' || true)"
  if [ "$pub" = 0 ]; then ok "only caddy publishes ports"; else bad "non-caddy services publish ports"; fi
}

# Runs the backup container; stdout of the container, exit status preserved.
run_backup() { dc run --rm -T backup "$@"; }

# Decrypts an archive from the backups dir inside the backup image and prints "sha=ok members=ok apps=ok nobackups=ok rk=<n> total=<rows> lead=<rows> method=<m>".
# shellcheck disable=SC2016 # the script runs inside the container
INSPECT='set -euo pipefail
d="$(mktemp -d)"; cd "$d"
age -d -i /age.txt "/backups/$1/$2" | tar -x
sha256sum -c SHA256SUMS >/dev/null && printf "sha=ok "
[ "$(ls | LC_ALL=C sort | tr "\n" " ")" = "SHA256SUMS apps.tar db.dump files.tar manifest.json site.tar " ] && printf "members=ok "
tar -tf apps.tar | grep -q "^apps/" && printf "apps=ok "
[ -z "$(for t in site files apps; do tar -tf $t.tar; done | grep "^backups")" ] && printf "nobackups=ok "
jq -r "\"rk=\(.tables | to_entries | map(select((.key | test(\"[.]rk_\")) and .value.rows > 0)) | length) total=\([.tables[].rows] | add) lead=\([.tables | to_entries[] | select(.key | test(\"[.]r_lead\$\")) | .value.rows] | add // 0) method=\(.method)\"" manifest.json'
inspect_archive() { # inspect_archive <pilot> <archive file name>
  dc run --rm -T --no-deps -v "$WORK/age-identity.txt:/age.txt:ro" --entrypoint bash backup -c "$INSPECT" _ "$1" "$2"
}

# local archives of a pilot, oldest first
local_archives() { find "$WORK/backups/$1" -name '*.tar.age' 2>/dev/null | sort; }

# number of requests the local ping receiver has logged
ping_hits() { dc logs --no-log-prefix pinger 2>/dev/null | grep -c '"GET / HTTP' || true; }

offbox_list() { dc run --rm -T --no-deps --entrypoint rclone backup lsf "offbox:juragan-backups/$1/"; }

section_backup() {
  echo "== backup"
  dc build -q backup >/dev/null 2>&1 || bad "build backup image"
  dc up -d pinger >/dev/null 2>&1 || bad "start ping receiver"
  if dc up -d --wait offbox >/dev/null 2>&1; then ok "offbox (S3) healthy"; else bad "offbox did not become healthy"; fi
  if dc run --rm -T --no-deps --entrypoint rclone backup mkdir offbox:juragan-backups >/dev/null 2>&1; then ok "bucket juragan-backups created"; else bad "bucket creation"; fi

  # create one record on jasa through the API so its data differs from fnb
  local jar="$WORK/cookies.txt" code
  URL_PATH=/auth/login https "$JASA_HOST" -o /dev/null -c "$jar" -X POST -H 'Content-Type: application/json' \
    -d "{\"email\":\"$OWNER_EMAIL\",\"password\":\"$OWNER_PASSWORD\"}"
  code="$(URL_PATH=/api/v1/r/lead https "$JASA_HOST" -b "$jar" -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' -d '{"lead_name":"Backup Test","phone":"+6281200000001"}')"
  if [[ "$code" == 2?? ]]; then ok "created a lead on jasa ($code)"; else bad "create lead on jasa: $code"; fi

  local out
  if out="$(run_backup all 2>&1)"; then ok "backup all exits 0"; else bad "backup all failed: $out"; fi

  local hits_good
  hits_good="$(ping_hits)"
  if [ "$hits_good" = 1 ]; then ok "success ping received once after the good run"; else bad "ping hits after good run: $hits_good"; fi

  local p n
  for p in jasa fnb; do
    n="$(offbox_list "$p" 2>/dev/null | grep -c "^$p-.*[.]tar[.]age$" || true)"
    if [ "$n" = 1 ]; then ok "off-box has one $p archive"; else bad "off-box $p archive count: $n"; fi
  done

  local f_jasa f_fnb s_jasa s_fnb
  f_jasa="$(basename "$(local_archives jasa | tail -1)")"
  f_fnb="$(basename "$(local_archives fnb | tail -1)")"
  s_jasa="$(inspect_archive jasa "$f_jasa" 2>/dev/null)" || s_jasa=""
  s_fnb="$(inspect_archive fnb "$f_fnb" 2>/dev/null)" || s_fnb=""
  for p in jasa fnb; do
    local s="s_$p"
    if [[ "${!s}" =~ ^sha=ok\ members=ok\ apps=ok\ nobackups=ok\ rk=([0-9]+)\ total=([0-9]+) ]] && [ "${BASH_REMATCH[1]}" -gt 0 ]; then
      ok "$p archive decrypts, SHA256SUMS verify, members incl. apps.tar, no backups/, ${BASH_REMATCH[1]} rk_ tables with rows (${!s})"
    else
      bad "$p archive inspection: ${!s}"
    fi
  done
  local t_jasa t_fnb l_jasa l_fnb
  t_jasa="$(sed -n 's/.*total=\([0-9]*\).*/\1/p' <<<"$s_jasa")"; t_fnb="$(sed -n 's/.*total=\([0-9]*\).*/\1/p' <<<"$s_fnb")"
  l_jasa="$(sed -n 's/.*lead=\([0-9]*\).*/\1/p' <<<"$s_jasa")"; l_fnb="$(sed -n 's/.*lead=\([0-9]*\).*/\1/p' <<<"$s_fnb")"
  if [ -n "$t_jasa" ] && [ -n "$t_fnb" ] && [ "$t_jasa" -gt "$t_fnb" ] && [ "${l_jasa:-0}" -eq $((${l_fnb:-0} + 1)) ]; then
    ok "jasa manifest has more rows than fnb ($t_jasa > $t_fnb, leads $l_jasa vs $l_fnb)"
  else
    bad "row totals: jasa=$t_jasa fnb=$t_fnb leads jasa=$l_jasa fnb=$l_fnb"
  fi
  if [[ "$s_jasa" == *method=pg_dump* ]]; then ok "method is pg_dump (current binary has no rangka backup)"; else bad "method: $s_jasa"; fi

  # off-box down: non-zero exit, a new local archive is kept, remote listing unchanged afterwards
  local before_remote after_remote before_local after_local
  before_remote="$(offbox_list jasa)"; before_local="$(local_archives jasa | wc -l)"
  dc stop offbox >/dev/null
  sleep 2 # archive names are second-resolution
  if out="$(dc run --rm -T backup jasa 2>&1)"; then bad "backup with off-box down should fail"; else ok "backup exits non-zero when off-box is down"; fi
  after_local="$(local_archives jasa | wc -l)"
  if [ "$after_local" -eq $((before_local + 1)) ]; then ok "failed upload keeps the new local archive"; else bad "local archives $before_local -> $after_local"; fi
  if [ "$(ping_hits)" = "$hits_good" ]; then ok "no ping after the failed upload"; else bad "ping sent after a failed upload"; fi
  dc up -d --wait offbox >/dev/null
  after_remote="$(offbox_list jasa)"
  if [ "$before_remote" = "$after_remote" ]; then ok "off-box listing unchanged after the failed run"; else bad "off-box listing changed"; fi

  # a failure inside the archive step (tar of site/ fails) must abort: non-zero, nothing uploaded, no ping,
  # no half-written archive left behind. tar is shadowed by a shim that fails on site.tar only.
  mkdir -p "$WORK/shim"
  cat >"$WORK/shim/tar" <<'SHIM'
#!/bin/sh
case "$*" in *"site.tar site") echo "injected tar failure" >&2; exit 2 ;; esac
exec /usr/bin/tar "$@"
SHIM
  chmod 755 "$WORK/shim/tar"
  before_local="$(local_archives jasa | wc -l)"
  sleep 2
  if out="$(dc run --rm -T -v "$WORK/shim:/shim:ro" -e PATH=/shim:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin backup jasa 2>&1)"; then
    bad "backup with a failing archive step should exit non-zero: $out"
  else
    ok "backup exits non-zero when the archive step fails"
  fi
  same "failed archive step uploads nothing" "$(offbox_list jasa)" "$before_remote"
  same "failed archive step leaves no local archive" "$(local_archives jasa | wc -l | tr -d ' ')" "$(echo "$before_local" | tr -d ' ')"
  if [ -z "$(find "$WORK/backups/jasa" -name '.work.*' -o -name '*.part')" ]; then ok "no work dir or .part left behind"; else bad "leftovers in backups/jasa"; fi
  same "no ping after the archive failure" "$(ping_hits)" "$hits_good"

  # docker stop during a slow archive step: plaintext work dir and .part must be gone, nothing uploaded, no ping.
  cat >"$WORK/shim/tar" <<'SHIM'
#!/bin/sh
case "$*" in *"site.tar site") sleep 60 ;; esac
exec /usr/bin/tar "$@"
SHIM
  sleep 2
  dc run --rm -T --name juragan-e2e-bkterm -v "$WORK/shim:/shim:ro" \
    -e PATH=/shim:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin backup jasa >"$WORK/term.log" 2>&1 &
  local bgpid=$! waited=0
  until [ -n "$(find "$WORK/backups/jasa" -name db.dump 2>/dev/null)" ] || [ "$waited" -ge 60 ]; do sleep 1; waited=$((waited + 1)); done
  if [ -n "$(find "$WORK/backups/jasa" -name db.dump 2>/dev/null)" ]; then ok "plaintext db.dump exists mid-run"; else bad "slow run never reached the archive step"; fi
  docker stop -t 20 juragan-e2e-bkterm >/dev/null 2>&1 || true
  if wait "$bgpid"; then bad "stopped backup should exit non-zero"; else ok "stopped backup exits non-zero"; fi
  if [ -z "$(find "$WORK/backups/jasa" -name '.work.*' -o -name '*.part')" ]; then ok "docker stop leaves no .work.* or .part"; else bad "leftovers after docker stop: $(find "$WORK/backups/jasa" -name '.work.*' -o -name '*.part')"; fi
  same "docker stop: nothing uploaded" "$(offbox_list jasa)" "$before_remote"
  same "docker stop: no ping" "$(ping_hits)" "$hits_good"
}

drill_projects() { docker ps -a --format '{{.Names}}' | grep -c '^juragan-drill-' || true; }
drill_networks() { docker network ls --format '{{.Name}}' | grep -c '^juragan-drill-' || true; }

# flip one byte in the middle of <file> (in place)
flip_byte() {
  local f="$1" off
  off=$(( $(wc -c <"$f") / 2 ))
  # shellcheck disable=SC2059 # the format is built on purpose: printf turns \xNN into the byte
  printf "$(printf '\\x%02x' $(( ($(od -An -tu1 -j "$off" -N1 "$f") + 1) % 256 )))" | dd of="$f" bs=1 seek="$off" conv=notrunc 2>/dev/null
}

section_drill() {
  echo "== drill"
  local drill=("$DEPLOY/restore-drill.sh" --env "$WORK/.env" --identity "$WORK/age-identity.txt" --log "$WORK/drill.log")
  export DRILL_OFFBOX_NETWORK="${PROJECT}_default" # the off-box store lives in this project's network
  local out rc=0 p

  # the real drill: newest archive of each pilot from off-box, fresh project, restore, boot
  out="$("${drill[@]}" --pilot all 2>&1)" || rc=$?
  same "drill --pilot all exits 0" "$rc" 0
  [ "$rc" = 0 ] || echo "$out" >&2
  for p in jasa fnb; do
    if grep -q "^DRILL PASSED $p $p-[0-9TZ]*[.]tar[.]age " <<<"$out"; then ok "DRILL PASSED $p"; else bad "no DRILL PASSED $p"; fi
  done
  local seq
  seq="$(grep -oE '^(restore ok: [0-9]+ tables, [0-9]+ rows match|DRILL PASSED [a-z]+)' <<<"$out" | sed -E 's/^restore ok.*/restore ok/' | tr '\n' ',')"
  same "each pilot: restore ok (manifest compared) right before its DRILL PASSED" "$seq" "restore ok,DRILL PASSED jasa,restore ok,DRILL PASSED fnb,"
  same "drill.log has both lines" "$(grep -c '^DRILL PASSED' "$WORK/drill.log" 2>/dev/null || true)" 2
  same "no drill project left after success" "$(drill_projects)/$(drill_networks)" 0/0

  # tampering: a flipped byte in the encrypted archive fails at decryption (age authenticates every chunk)
  local newest tdir="$WORK/tamper"
  rm -rf "$tdir"; mkdir -p "$tdir"
  newest="$(local_archives jasa | tail -1)"
  cp "$newest" "$tdir/$(basename "$newest")"
  flip_byte "$tdir/$(basename "$newest")"
  rc=0; out="$("${drill[@]}" --pilot jasa --archive "$tdir/$(basename "$newest")" 2>&1)" || rc=$?
  if [ "$rc" != 0 ] && grep -qi 'authentication\|checksum' <<<"$out" && ! grep -q 'DRILL PASSED' <<<"$out"; then
    ok "flipped byte in the .age archive: drill fails ($(grep -oi 'age authentication error\|checksum[a-z ]*' <<<"$out" | head -1))"
  else
    bad "tampered .age archive: rc=$rc: $out"
  fi
  same "no drill project left after the .age tamper" "$(drill_projects)/$(drill_networks)" 0/0

  # the checksum step itself: decrypt, flip one byte in the middle of the db.dump member's data (offset from the
  # tar header block), run with the plain .tar
  local plain="$tdir/${newest##*/}"
  plain="${plain%.age}"
  # shellcheck disable=SC2016 # the script runs inside the container
  local tamper='set -euo pipefail
age -d -i /age.txt -o "/t/$1" "/backups/jasa/$2"
line="$(tar -tv --block-number -f "/t/$1" | awk "\$NF == \"db.dump\"")"
blk="$(awk "{gsub(\":\", \"\", \$2); print \$2}" <<<"$line")"; size="$(awk "{print \$5}" <<<"$line")"
off=$(( (blk + 1) * 512 + size / 2 ))
b=$(od -An -tu1 -j "$off" -N1 "/t/$1")
printf "$(printf "\\x%02x" $(( (b + 1) % 256 )))" | dd of="/t/$1" bs=1 seek="$off" conv=notrunc 2>/dev/null
echo "flipped byte at offset $off of db.dump (size $size)"'
  dc run --rm -T --no-deps -v "$WORK/age-identity.txt:/age.txt:ro" -v "$tdir:/t" --entrypoint bash backup \
    -c "$tamper" _ "$(basename "$plain")" "$(basename "$newest")" || bad "could not prepare the tampered plain archive"
  rc=0; out="$("${drill[@]}" --pilot jasa --archive "$plain" 2>&1)" || rc=$?
  if [ "$rc" != 0 ] && grep -q 'checksum' <<<"$out" && grep -q 'no database was touched' <<<"$out" && ! grep -q 'pg_restore' <<<"$out"; then
    ok "flipped byte inside db.dump: fails at the checksum step before any database work"
  else
    bad "tampered plain archive: rc=$rc: $out"
  fi
  same "no drill project left after the checksum failure" "$(drill_projects)/$(drill_networks)" 0/0

  # a manifest without tables is refused (archive rebuilt with valid sums so only that rule can fail it)
  # shellcheck disable=SC2016 # the script runs inside the container
  local notables='set -euo pipefail
d="$(mktemp -d)"; cd "$d"; tar -xf "/t/$1"
jq ".tables = {}" manifest.json >m && mv m manifest.json
sha256sum db.dump site.tar files.tar apps.tar manifest.json >SHA256SUMS
tar -cf "/t/$2" manifest.json db.dump site.tar files.tar apps.tar SHA256SUMS'
  rm -f "$plain"
  # shellcheck disable=SC2016 # runs inside the container
  dc run --rm -T --no-deps -v "$WORK/age-identity.txt:/age.txt:ro" -v "$tdir:/t" --entrypoint bash backup \
    -c 'age -d -i /age.txt -o "/t/$1" "/backups/jasa/$2"' _ clean.tar "$(basename "$newest")" >/dev/null 2>&1 || bad "decrypt clean archive"
  dc run --rm -T --no-deps -v "$tdir:/t" --entrypoint bash backup -c "$notables" _ clean.tar notables.tar >/dev/null 2>&1 \
    || bad "could not build the no-tables archive"
  rc=0; out="$("${drill[@]}" --pilot jasa --archive "$tdir/notables.tar" 2>&1)" || rc=$?
  if [ "$rc" != 0 ] && grep -q 'manifest lists no tables' <<<"$out" && ! grep -q 'DRILL PASSED' <<<"$out"; then
    ok "manifest with zero tables is refused"
  else
    bad "no-tables archive: rc=$rc: $out"
  fi
  same "no drill project left after the no-tables archive" "$(drill_projects)/$(drill_networks)" 0/0

  # truncated archive
  head -c $(( $(wc -c <"$tdir/clean.tar") / 2 )) "$tdir/clean.tar" >"$tdir/trunc.tar"
  rc=0; out="$("${drill[@]}" --pilot jasa --archive "$tdir/trunc.tar" 2>&1)" || rc=$?
  if [ "$rc" != 0 ] && grep -qi 'truncated\|damaged\|checksum' <<<"$out" && ! grep -q 'DRILL PASSED' <<<"$out"; then
    ok "truncated archive is rejected"
  else
    bad "truncated archive: rc=$rc: $out"
  fi
  same "no drill project left after the truncated archive" "$(drill_projects)/$(drill_networks)" 0/0

  # a bucket prefix with no archive: clear message, nothing left running
  sed 's/^BACKUP_BUCKET=.*/BACKUP_BUCKET=juragan-empty/' "$WORK/.env" >"$WORK/.env.emptybucket"
  dc run --rm -T --no-deps --entrypoint rclone backup mkdir offbox:juragan-empty >/dev/null 2>&1 || bad "create the empty bucket"
  rc=0; out="$("$DEPLOY/restore-drill.sh" --env "$WORK/.env.emptybucket" --identity "$WORK/age-identity.txt" --log "$WORK/drill.log" --pilot jasa 2>&1)" || rc=$?
  if [ "$rc" != 0 ] && grep -q 'no archive for jasa' <<<"$out" && ! grep -q 'DRILL PASSED' <<<"$out"; then
    ok "empty off-box prefix fails with 'no archive for jasa'"
  else
    bad "empty prefix: rc=$rc: $out"
  fi
  same "no drill project left after the empty prefix" "$(drill_projects)/$(drill_networks)" 0/0

  # the migrate gate parses the plan: only exactly 0 passes
  local f="$WORK/migrate.fake"
  printf 'CREATE TABLE x (a int);\n-- would apply 10 statement(s); 3 metadata file(s), 0 changed since the last migrate\n' >"$f"
  same "migrate_statements reads 10, not 0, from 'would apply 10 statement(s)'" "$(migrate_statements "$f")" 10
  printf -- '-- note: x\n-- would apply 0 statement(s); 3 metadata file(s), 0 changed since the last migrate\n' >"$f"
  same "migrate_statements reads 0" "$(migrate_statements "$f")" 0
  printf -- '-- applied 0 statement(s); 3 metadata file(s)\n' >"$f"
  if migrate_statements "$f" >/dev/null; then bad "migrate_statements accepted an 'applied' line"; else ok "migrate_statements fails when the would-apply line is missing"; fi
  if grep -q 'migrate_statements' "$DEPLOY/restore-drill.sh"; then ok "restore-drill.sh uses migrate_statements"; else bad "drill does not use migrate_statements"; fi

  # restore.sh against the live jasa database is refused, and the live data is unchanged
  local before after live="$WORK/live-srv"
  mkdir -p "$live"
  before="$(psql_as jasa jasa -qAtX -c "select count(*) from public.r_lead" 2>&1 || true)"
  rc=0
  out="$(dc run --rm -T -v "$WORK/age-identity.txt:/run/secrets/age-identity:ro" -v "$live:/restore/jasa" \
    --entrypoint /usr/local/bin/restore.sh backup "/backups/jasa/$(basename "$newest")" jasa 2>&1)" || rc=$?
  if [ "$rc" != 0 ] && grep -q 'not empty' <<<"$out"; then ok "restore into the live jasa database is refused (not empty)"; else bad "live restore: rc=$rc: $out"; fi
  after="$(psql_as jasa jasa -qAtX -c "select count(*) from public.r_lead" 2>&1 || true)"
  if [[ "$before" =~ ^[0-9]+$ ]]; then same "live jasa data unchanged after the refused restore" "$after" "$before"; else bad "could not count live leads: $before"; fi
  same "refused restore wrote nothing to the folder" "$(find "$live" -mindepth 1 | wc -l | tr -d ' ')" 0
  if [ "$(drill_projects)" = 0 ]; then ok "no juragan-drill-* container at the end"; else bad "drill containers left"; fi
}

main() {
  trap cleanup EXIT
  prepare
  section_stack
  section_backup
  section_drill
  [ "$FAIL" -eq 0 ]
}

main "$@"
