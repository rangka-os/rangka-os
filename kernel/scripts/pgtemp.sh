#!/usr/bin/env bash
# Start a throwaway Postgres cluster in a temp dir and print its URL.
#   scripts/pgtemp.sh start [port]   -> prints DATABASE_URL, writes the dir to .pgtemp
#   scripts/pgtemp.sh stop           -> stops it and removes the dir
set -euo pipefail
PGBIN=${PGBIN:-$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | sort -V | tail -1)}
STATE=${PGTEMP_STATE:-$(dirname "$0")/../.pgtemp}
run() { if [ "$(id -u)" = 0 ]; then su postgres -s /bin/bash -c "$*"; else bash -c "$*"; fi; }
case "${1:-start}" in
start)
  PORT=${2:-$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')}
  DIR=$(mktemp -d /tmp/rangka-pg-XXXXXX)
  [ "$(id -u)" = 0 ] && chown postgres "$DIR"
  run "$PGBIN/initdb -D $DIR/data -U rangka --auth=trust -E UTF8 --locale=C.UTF-8 >$DIR/initdb.log 2>&1"
  run "$PGBIN/pg_ctl -D $DIR/data -l $DIR/pg.log -o '-p $PORT -k $DIR -c listen_addresses=127.0.0.1 -c fsync=off' -w start >/dev/null"
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
