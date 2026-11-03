#!/usr/bin/env bash
# First start only: one role and one database per pilot; nobody else may connect to them.
# The role is a plain owner: no SUPERUSER, no BYPASSRLS (row-level security, contract 0010, must bind it), no CREATEROLE.
set -euo pipefail
: "${JASA_DB_PASSWORD:?}" "${FNB_DB_PASSWORD:?}"

for p in jasa fnb; do
  # the password reaches psql through its environment (\getenv), never argv
  PILOT_PASSWORD="$(printenv "${p^^}_DB_PASSWORD")" psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres <<SQL
\getenv pw PILOT_PASSWORD
CREATE ROLE $p LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB PASSWORD :'pw';
CREATE DATABASE $p OWNER $p;
REVOKE CONNECT ON DATABASE $p FROM PUBLIC;
SQL
done
