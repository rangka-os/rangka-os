#!/usr/bin/env bash
# Start a throwaway Postgres cluster in a temp dir and print its URL.
#   scripts/pgtemp.sh start [port]   -> prints DATABASE_URL, writes the dir to .pgtemp
#   scripts/pgtemp.sh stop           -> stops it and removes the dir
set -euo pipefail
# PGBIN, else the newest server install found: Debian/Ubuntu, PATH, pg_config, Homebrew, DBngin, Postgres.app.
# A directory counts only with both initdb and postgres (Homebrew's libpq ships initdb without a server).
find_pgbin() {
  local d
  for d in $(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -rV) \
           "$(dirname "$(command -v postgres 2>/dev/null || echo /nonexistent/x)")" \
           "$(pg_config --bindir 2>/dev/null)" \
           $(ls -d /opt/homebrew/opt/postgresql@*/bin /usr/local/opt/postgresql@*/bin 2>/dev/null | sort -rV) \
           $(ls -d /Users/Shared/DBngin/postgresql/*/bin 2>/dev/null | sort -rV) \
           $(ls -d /Applications/Postgres.app/Contents/Versions/*/bin 2>/dev/null | sort -rV); do
    [ -x "$d/initdb" ] && [ -x "$d/postgres" ] && { echo "$d"; return; }
  done
}
PGBIN=${PGBIN:-$(find_pgbin)}
if [ "${1:-start}" = start ] && [ -z "$PGBIN" ]; then
  echo "pgtemp: no PostgreSQL server binaries found; install PostgreSQL 16+ or set PGBIN to the dir with initdb and postgres" >&2
  exit 1
fi
STATE=${PGTEMP_STATE:-$(dirname "$0")/../.pgtemp}
run() { if [ "$(id -u)" = 0 ]; then su postgres -s /bin/bash -c "$*"; else bash -c "$*"; fi; }
case "${1:-start}" in
start)
  PORT=${2:-$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')}
  DIR=$(mktemp -d /tmp/rangka-pg-XXXXXX)
  [ "$(id -u)" = 0 ] && chown postgres "$DIR"
  run "$PGBIN/initdb -D $DIR/data -U rangka --auth=trust -E UTF8 --locale=C.UTF-8 >$DIR/initdb.log 2>&1" \
    || { echo "pgtemp: initdb failed ($PGBIN):" >&2; cat "$DIR/initdb.log" >&2; exit 1; }
  run "$PGBIN/pg_ctl -D $DIR/data -l $DIR/pg.log -o '-p $PORT -k $DIR -c listen_addresses=127.0.0.1 -c fsync=off' -w start >/dev/null" \
    || { echo "pgtemp: postgres did not start:" >&2; cat "$DIR/pg.log" >&2; exit 1; }
  run "$PGBIN/createdb -h 127.0.0.1 -p $PORT -U rangka rangka"
  echo "$DIR $PORT" > "$STATE"
  echo "postgres://rangka@127.0.0.1:$PORT/rangka?sslmode=disable"
  ;;
stop)
  [ -f "$STATE" ] || exit 0
  read -r DIR PORT < "$STATE"
  run "$PGBIN/pg_ctl -D $DIR/data -m fast -w stop >/dev/null" || true
  rm -rf "$DIR" "$STATE"
  ;;
esac
