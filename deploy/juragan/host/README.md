# juragan.ai tenant host: runbook

Many Juragan tenants on one VM, without hand work per tenant: one Postgres and one Caddy shared by all, and per
tenant one kernel container (its own pinned release), one database and one login role (NOSUPERUSER NOBYPASSRLS, no
CONNECT for anyone else), one Caddy site `<id>.juragan.ai` with its own certificate, and a nightly encrypted archive
that is restored into a throwaway Postgres every night (the drill). `./tenantctl` does all of it. The two pilots keep
running on `../compose.yaml` (RUNBOOK.md); they can move here later as ordinary tenants (section 8).

Readers: the operator (Dede, or whoever is on call) and the builder that turns onboarding answers into a tenant
(Juragan Cloud; locally the shim in the Juragan workspace, `scripts/tenant-host.sh`).

## 1. What is where

| Path | What |
| --- | --- |
| `compose.yaml` | shared services: `postgres`, `caddy`, and the `backup` tool image (profile `tools`) |
| `tenant.compose.yaml` | one tenant = compose project `juragan-t-<id>`, service `kernel`, alias `t-<id>` on the host network |
| `Caddyfile` | snippets `tenant` / `suspended`; imports `$HOST_DIR/caddy/*.caddy` (written by tenantctl) |
| `kernel/Dockerfile` | the release binary from GitHub Releases, verified against `checksums.txt`; target `pdf` (Chromium) or `slim` |
| `backup/Dockerfile` | `../backup/{backup,restore,lib}.sh` on postgres:16-alpine with rclone, age, jq |
| `host.env.example` | settings; copy to `.env` (git-ignored), `chmod 600` |
| `systemd/` | nightly backup + drill (02:00 Jakarta), health every 5 minutes |
| `test/e2e.sh` | local end-to-end test (Docker only) |

Under `HOST_DIR` (default `/opt/juragan/host`): `tenants/<id>/` (bundle + `data/` + `backups/`, mounted at `/srv`,
owned by uid 65532), `registry/<id>.env` (id, org, host, tag, plan, status, dates; no secrets, no personal data),
`secrets/<id>.env` (generated database password, mode 600), `caddy/<id>.caddy`, `backups/<id>/`, `drill.log`.

The GHCR image `ghcr.io/rangka-os/rangka` refused anonymous pulls on 6 Nov 2026; the release assets on GitHub are
public, so the kernel image is built from them (a minute the first time per tag).

## 2. One-time setup (operator; steps that cost money or touch DNS are marked **paid** / **DNS**)

1. **paid** VM: 2 vCPU / 4 GB / 60-80 GB SSD, Ubuntu 24.04, in Indonesia (customer data stays in the country). Ports
   22 from the operator only, 80 and 443 from anywhere. Docker Engine + compose plugin.
2. **paid** Object storage bucket (S3-compatible), versioning and, when the provider has it, Object Lock (30 days
   governance) and a lifecycle rule expiring versions after 35 days. A write-only key (`PutObject`, `GetObject`,
   `ListBucket`; no delete), as in RUNBOOK.md section 1.
3. **DNS** wildcard `*.juragan.ai` A record to the VM (one record for every tenant). Caddy asks Let's Encrypt for each
   tenant's own name when tenantctl adds its site; with a wildcard record no DNS step is needed per tenant.
4. age key pair on the operator's laptop (`age-keygen -o juragan-host.key`); the public `age1...` goes into
   `BACKUP_AGE_RECIPIENT`, the private key into the password manager. For the nightly drill (section 5) a copy lives
   in `/root/juragan-drill.key` (mode 600) on the VM; see the trade-off there.
5. Clone rangka-os to `/opt/juragan/rangka-os`, `cd deploy/juragan/host`, `cp host.env.example .env`, fill it in
   (`openssl rand -hex 24` for `POSTGRES_PASSWORD`), `chmod 600 .env`.
6. `./tenantctl host-up` (Postgres and Caddy). Check: `docker compose -p juragan-host ps` shows both healthy.
7. Timers:
   ```bash
   sudo cp systemd/juragan-host-*.service systemd/juragan-host-*.timer /etc/systemd/system/
   sudo systemd-analyze verify /etc/systemd/system/juragan-host-*.service /etc/systemd/system/juragan-host-*.timer
   sudo systemctl daemon-reload
   sudo systemctl enable --now juragan-host-backup.timer juragan-host-health.timer
   ```
8. **optional, free** healthchecks.io: one check for the backup (period 1 day, grace 3 h; `BACKUP_PING_URL`), one for
   health (period 5 min, grace 10 min; `HEALTH_PING_URL`). The free plan has 20 checks.

## 3. A new tenant

The builder writes a bundle (`apps/`, `modules/` of the pinned rangka-apps commit, `site/` from the onboarding
answers, `fixtures/<org>/`, `connectors/`), then:

```bash
RANGKA_OWNER_PASSWORD=... ./tenantctl create <id> --bundle <dir> --owner-email <email> --plan <plan> [--tag v0.5.0-alpha.9]
```

The password reaches the kernel through the environment only. Steps, each recorded in the registry if it fails:
copy the bundle, create role + database, build the image for the tag if missing, `rangka validate`, `rangka migrate`,
`rangka seed --org` for every org in `site/config.rk` (with `fixtures/<org>` when present), `rangka plan set <plan>`
(contract 0028; on a release without it the plan comes from `config.rk`), start the kernel, wait for 200, write the
Caddy site and reload. It prints the tenant URL. Ids are 3-30 lowercase letters and digits and are never reused, nor
are hosts. A failed create: read the error, then `./tenantctl delete <id> --confirm <id> --force`.

Locally, from onboarding answers in one command (Juragan workspace): `scripts/tenant-host.sh baru jasa-proyek`.

## 4. Daily operations

```bash
./tenantctl list
./tenantctl health            # one line per tenant + disk; exit 1 on any problem
./tenantctl status <id>
./tenantctl logs <id> [lines]
./tenantctl restart <id>      # after a site/ change (letterhead, features)
RANGKA_USER_PASSWORD=... ./tenantctl kernel <id> user add <email> --org <org> --role <role>
./tenantctl kernel <id> plan set tumbuh --by operator --note "upgrade"   # contract 0028 releases
```

Health fails when a kernel or its route does not answer 200, when the newest local archive is older than
`HEALTH_BACKUP_MAX_AGE_HOURS` (26), or when the disk is above `HEALTH_DISK_MAX_PCT` (85).

## 5. Backup and restore drill

`./tenantctl backup all` runs `../backup/backup.sh` for every active and suspended tenant (`BACKUP_PILOTS` = the
tenants): one REPEATABLE READ snapshot per tenant with row counts and checksums, `pg_dump`, the bundle and files,
encrypted with age, kept `BACKUP_KEEP_LOCAL` days locally and uploaded off-box with a key that cannot delete.

`./tenantctl drill all --identity <age key>` takes each tenant's newest off-box archive (at most `--max-age` 48 h old),
starts a throwaway Postgres on the host network, and restores with `restore.sh` (checksums before touching anything,
exact table-by-table compare with the manifest, ownership). Then the restored role must be NOSUPERUSER NOBYPASSRLS,
every `r_*` table must have row-level security enabled and forced, `migrate --dry-run` with the tenant's own release
must plan 0 statements, and that release must boot on it and answer 200. `DRILL PASSED <id> <archive> <time>` goes to
`drill.log`. Everything the drill started is removed, also on failure.

**Trade-off of the nightly drill on the VM.** It needs the age private key on the VM, which the pilot RUNBOOK avoids.
The VM already holds every tenant's live data in clear, so the extra exposure is the archive history (up to 35 days,
including tenants deleted within that window). Not acceptable: remove the second `ExecStart` from
`juragan-host-backup.service` and run `tenantctl drill` weekly by hand with the key from the password manager, then
delete the key file.

**Restore in production** (a tenant's data is damaged): `./tenantctl suspend <id>`, take note of the archive to use,
then follow RUNBOOK.md section 6 with `BACKUP_PILOTS=<id>` and `<ID>_DB_PASSWORD` from `secrets/<id>.env`: drop and
recreate the database and role (empty), empty the tenant folder, run `restore.sh` in the backup image, `resume`.

## 6. Upgrade a tenant

```bash
./tenantctl upgrade <id> --tag v0.5.0-alpha.10 [--bundle <dir with the new apps/ modules/>]
```

Backup first (must succeed), build the image, replace `apps/ modules/ connectors/ fixtures/` when `--bundle` is given
(never `site/` or `data/`), show the `migrate --dry-run` plan, restart (the kernel migrates on start and takes its own
pre-migrate backup into `backups/`), wait for 200. A kernel that does not come up is rolled back to the previous tag;
if the new release already migrated the database, restore the pre-upgrade archive. Upgrade tenants one at a time,
the internal ones first, as juragan-pilots does with its pins.

## 7. Suspend, resume, delete (UU PDP)

- `suspend <id>`: the kernel stops, the route answers 503 with a short page, data stays. For unpaid invoices or a
  deletion request.
- `resume <id>`: back to active.
- `delete <id> --confirm <id>`: only a suspended tenant. Final backup first, then the kernel project, the database
  and role, the route, the files and the secret go; the registry keeps a tombstone (id, host, dates). Archives remain
  until retention (local `BACKUP_KEEP_LOCAL` days, off-box 35 days by the bucket lifecycle), which the terms must say.
  Before deleting, give the owner the agreed export window (CSV export per list, or an archive).

## 8. Capacity and moving the pilots

Measured locally (6 Nov, `slim`, idle): kernel 10-14 MiB, Postgres 60-95 MiB, Caddy 15 MiB. A PDF render adds
100-200 MB for up to 30 s (`RANGKA_PDF_CONCURRENCY=1`, `KERNEL_MEM_LIMIT=768m`). Budget about 150 MB per active
tenant under load: a 4 GB VM holds about 15-20 small tenants; above 70 % memory or 85 % disk, open a second host
(same files, another `HOST_DIR`, its own DNS names or a second wildcard).

The pilots can become tenants `jasa` and `fnb`... except those names are reserved here so that they never collide
with the pilot deploy on the same Postgres. Moving them is a separate, planned step (new ids, restore from their
archives into the new databases, DNS switch).
