# juragan.ai pilots: runbook

Two Rangka OS pilots (`jasa`, `fnb`) on one VM: Postgres 16, the two kernels, Caddy (automatic HTTPS), and a
nightly encrypted backup to off-box storage. Readers: Dede (does every cloud, DNS and credential step by hand)
and the on-call engineer.

Steps marked **Dede** create cloud resources, touch DNS, money or credentials. Everything else is a command the
scripts in this folder (or `local/e2e.sh`) run. Placeholders are in `<angle brackets>`. Never paste secrets into
chat, tickets or git.

Files in this folder: `compose.yaml`, `.env.example`, `Caddyfile`, `postgres/initdb/10-pilots.sh`,
`backup/{Dockerfile,lib.sh,backup.sh,restore.sh}`, `restore-drill.sh`, `systemd/`, `local/` (end-to-end test).

## 1. Provider

AWS Asia Pacific (Jakarta), region `ap-southeast-3`:

| Piece | Setting |
| --- | --- |
| VM | EC2, 2 vCPU / 4 GB (for example `t3.medium`; `t4g.medium` if Graviton is offered in the region: the release image is multi-arch amd64/arm64), 40 GB gp3, Ubuntu 24.04 LTS |
| Address | Elastic IP attached to the VM |
| Security group | 22 from Dede's IP only, 80 and 443 from anywhere |
| Bucket | S3 in `ap-southeast-3`, Block Public Access on, versioning on, Object Lock enabled **at creation** (governance mode, default retention 30 days), lifecycle rule expiring current and noncurrent versions after 35 days |
| Backup key | IAM user `juragan-backup`, policy below |

Prices: check the AWS pricing page for the region before creating anything.

Why: customer data stays in an Indonesian region; the bucket keeps immutable off-box copies (the backup key
cannot delete, and Object Lock stops even a leaked key from erasing history); one account, one bill.

Alternative: Biznet Gio (NEO Lite VM + NEO Object Storage, Jakarta). Same stack. Set
`RCLONE_CONFIG_OFFBOX_PROVIDER=Other` and `RCLONE_CONFIG_OFFBOX_ENDPOINT=<service URL>`. Verify that its object
storage supports Object Lock before relying on it; without it, immutability is lost.

IAM policy for `juragan-backup` (replace `<bucket>`):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    { "Effect": "Allow", "Action": ["s3:PutObject", "s3:GetObject"], "Resource": "arn:aws:s3:::<bucket>/*" },
    { "Effect": "Allow", "Action": ["s3:ListBucket"], "Resource": "arn:aws:s3:::<bucket>" }
  ]
}
```

The backup key cannot delete. Retention is the bucket's job (Object Lock 30 days, lifecycle expiry at 35 days);
`backup.sh` never deletes remote objects. Optional: a second IAM user for the drill machine with only
`s3:GetObject` and `s3:ListBucket` (same ARNs).

## 2. What Dede does by hand

Each step has the check that proves it.

1. **AWS account and billing** in the console. Check: you can open the EC2 console in `ap-southeast-3`.
2. **Launch the VM** (table above) with a key pair, and the security group (22 from your IP, 80/443 world).
   Check: `ssh ubuntu@<elastic-ip>` works; from another network, port 22 is refused.
3. **Elastic IP:** allocate and associate it. Check: the console shows it attached; `ssh` still works.
4. **DNS:** at the juragan.ai registrar, A records `jasa.juragan.ai` and `fnb.juragan.ai` to the Elastic IP
   (the pilot hostnames are placeholders and may change; keep `JASA_HOST`/`FNB_HOST` in `.env` in step). Check:
   ```bash
   dig +short jasa.juragan.ai
   dig +short fnb.juragan.ai
   ```
   Both print the Elastic IP. Caddy gets certificates by itself once DNS resolves and 80/443 are open.
5. **Bucket and IAM key:** create the bucket as in section 1 (enable Object Lock at bucket creation: simplest), the
   `juragan-backup` user with the policy, and one access key. Keep the key for step 8. Check: bucket properties
   show Versioning on, Object Lock on (governance, 30 days), Block Public Access on.
6. **age key pair**, on your laptop, not the VM:
   ```bash
   age-keygen -o juragan-backup.key
   ```
   It prints `Public key: age1...`. Put `juragan-backup.key` into the password manager and keep one offline
   copy. The private key never goes on the VM. Check: `grep -c '^AGE-SECRET-KEY-' juragan-backup.key`
   prints `1`. The file also has a `# public key: age1...` line: that `age1...` value goes into
   `BACKUP_AGE_RECIPIENT` in `.env`.
7. **Image pull:** the release image `ghcr.io/rangka-os/rangka:<tag>` is public, no login needed. Check on the
   VM after Docker is installed: `docker pull ghcr.io/rangka-os/rangka:<tag>`.
8. **`.env`** (on the VM, section 3): copy `.env.example` and fill it in. Generate each database password (hex, so it is
   URL-safe: the passwords are embedded in `DATABASE_URL`, and characters such as `/`, `@` or `:` would break it):
   ```bash
   openssl rand -hex 24
   ```
   Keys to set: `RANGKA_TAG` (a pinned release, never `latest`), `PILOTS_DIR`, `JASA_HOST`, `FNB_HOST`,
   `CADDY_TLS` (an ops email for ACME), `POSTGRES_PASSWORD`, `JASA_DB_PASSWORD`, `FNB_DB_PASSWORD`,
   `JASA_ORG`, `FNB_ORG` (organisation ids, default `jasa` / `fnb`), `BACKUP_DIR=/opt/juragan/backups`,
   `BACKUP_BUCKET`, `BACKUP_AGE_RECIPIENT`, and `RCLONE_CONFIG_OFFBOX_PROVIDER=AWS`,
   `RCLONE_CONFIG_OFFBOX_REGION=ap-southeast-3`, `RCLONE_CONFIG_OFFBOX_ACCESS_KEY_ID`,
   `RCLONE_CONFIG_OFFBOX_SECRET_ACCESS_KEY` (leave `RCLONE_CONFIG_OFFBOX_ENDPOINT` blank for AWS). Compose
   refuses to start when a required key is empty and names it. Store a copy of the passwords in the password
   manager.
9. **Optional but recommended: healthchecks.io** check with a daily period and a grace time of a few hours.
   Put its ping URL in `BACKUP_PING_URL`. It is pinged only after a fully successful run, so a missed or failed
   night raises an alert. Check: after the first successful backup the check turns green.

## 3. Install on the VM

1. **Docker Engine and the compose plugin:** install from Docker's apt repository following
   <https://docs.docker.com/engine/install/ubuntu/>. Check:
   ```bash
   docker --version
   docker compose version
   ```
2. **Clone this repo to `/opt/juragan/rangka-os`** (the systemd units expect `/opt/juragan/rangka-os/deploy/juragan`).
   Pilot data, backups and the pilots checkout live next to the clone, never inside it:
   `/opt/juragan/pilots`, `/opt/juragan/pilots-src`, `/opt/juragan/backups`. If the clone
   needs credentials, **Dede** adds a read-only deploy key to the VM.
   ```bash
   sudo mkdir -p /opt/juragan
   sudo git clone <rangka-os repo URL> /opt/juragan/rangka-os
   cd /opt/juragan/rangka-os/deploy/juragan
   ```
3. **Pilot folders** from the private repo `rangka-os/juragan-pilots` (stream S9), one folder per pilot with
   `apps/`, `site/`, `fixtures/`. Clone it to `/opt/juragan/pilots-src` (**Dede** adds a read-only deploy key
   for that repo too), copy each folder to `/opt/juragan/pilots/<pilot>`, then create the writable folders and hand them to the kernel (distroless nonroot, uid 65532):
   ```bash
   sudo git clone <juragan-pilots repo URL> /opt/juragan/pilots-src
   sudo mkdir -p /opt/juragan/pilots
   for p in jasa fnb; do
     sudo cp -a /opt/juragan/pilots-src/$p /opt/juragan/pilots/$p
     sudo mkdir -p /opt/juragan/pilots/$p/data/files /opt/juragan/pilots/$p/backups
   done
   sudo chown -R 65532:65532 /opt/juragan/pilots
   sudo mkdir -p /opt/juragan/backups && sudo chmod 700 /opt/juragan/backups
   ```
   `backups/` receives the kernel's pre-migrate backups (see section 5); the nightly archive does not include it.
4. **`.env`:** `cp .env.example .env`, fill it in (section 2, step 8), then `chmod 600 .env`.
   Check: `docker compose config -q` prints nothing (any missing secret is named in the error).
5. **Start:**
   ```bash
   docker compose up -d
   docker compose ps
   ```
   `postgres` is healthy, `jasa`, `fnb` and `caddy` are up. The first start creates one role and one database per
   pilot (`postgres/initdb/10-pilots.sh`): the `jasa` role cannot connect to `fnb` and the reverse. The kernels
   migrate on start.
6. **Seed the owner once per pilot.** **Dede** types the password himself, so it never lands in shell history or
   a file. The two values are passed to this one command only and are not written to `.env`:
   ```bash
   read -r -p 'owner email: ' RANGKA_OWNER_EMAIL
   read -r -s -p 'owner password: ' RANGKA_OWNER_PASSWORD; echo
   export RANGKA_OWNER_EMAIL RANGKA_OWNER_PASSWORD
   docker compose run --rm -e RANGKA_OWNER_EMAIL -e RANGKA_OWNER_PASSWORD jasa seed
   docker compose run --rm -e RANGKA_OWNER_EMAIL -e RANGKA_OWNER_PASSWORD fnb seed
   unset RANGKA_OWNER_EMAIL RANGKA_OWNER_PASSWORD
   ```
   Leave `RANGKA_OWNER_EMAIL` and `RANGKA_OWNER_PASSWORD` blank in `.env`.
7. **Verify HTTPS** (DNS must already resolve; the first certificate takes a short while):
   ```bash
   curl -sS -o /dev/null -w '%{http_code}\n' https://jasa.juragan.ai/.well-known/openid-configuration
   curl -sS -o /dev/null -w '%{http_code}\n' https://fnb.juragan.ai/.well-known/openid-configuration
   docker compose logs caddy | tail -20
   ```
   Both print `200`. If not, see "HTTPS does not come up" in section 5.
8. **Install the nightly backup timer** (02:00 Asia/Jakarta, `Persistent=true`):
   ```bash
   sudo cp systemd/juragan-backup.service systemd/juragan-backup.timer /etc/systemd/system/
   sudo systemd-analyze verify /etc/systemd/system/juragan-backup.service /etc/systemd/system/juragan-backup.timer
   sudo systemctl daemon-reload
   sudo systemctl enable --now juragan-backup.timer
   systemctl list-timers juragan-backup.timer
   ```
   `systemd-analyze verify` needs a Linux host with systemd, so it was not run on the macOS dev machine: run it
   here. Test one run now (builds the backup image on first use):
   ```bash
   sudo systemctl start juragan-backup.service
   journalctl -u juragan-backup.service --no-pager | tail -20
   ```
   Expected lines per pilot: `backup: <pilot>: wrote /backups/<pilot>/<pilot>-<stamp>.tar.age (...)`,
   `backup: <pilot>: uploaded to offbox:<bucket>/<pilot>/<name>`, then `backup: done`. Check the bucket shows
   `jasa/` and `fnb/` with `.tar.age` objects, and that the healthchecks.io check turned green.

   If the bucket has Object Lock, every PutObject must carry a Content-MD5 header; rclone sends it on single-part
   uploads, and this first backup landing in the bucket proves it works.

## 4. Pilot bundle requirements (S9)

Each pilot's `site/system.rk` must have:

- `auth.issuer: https://<host>` (the pilot's public host). Session cookies get the `Secure` flag only with an
  https issuer.
- `files.path: ./data/files` (becomes `/srv/data/files`, inside the backed-up `data/` folder).
- `db.url: '${DATABASE_URL}'` (quoted; compose injects the real URL, nothing secret in the bundle).
- `db.isolation` as the bundle needs.

`RANGKA_ENV=production` and `RANGKA_BACKUP_DIR=/srv/backups` are already set in `compose.yaml`: once the S1
kernel lands, migrations take a pre-migrate backup into `<pilot>/backups/` (local disk only, not in the nightly
archive).

## 5. Daily operations

Status and logs:

```bash
cd /opt/juragan/rangka-os/deploy/juragan
docker compose ps
docker compose logs --tail 100 jasa
docker compose logs -f fnb
docker compose logs caddy | tail -50
systemctl list-timers juragan-backup.timer
journalctl -u juragan-backup.service --no-pager | tail -50
```

**Backup behaviour.** A failed upload exits non-zero, keeps the local archive, marks the unit failed and sends no
ping. Local archives are pruned after `BACKUP_KEEP_LOCAL` days (default 7), and pruning runs even after a failed
upload. During a long off-box outage, copy `BACKUP_DIR` archives off the VM before they age out. Run a backup by
hand any time: `docker compose run --rm backup all` (or `jasa` / `fnb`).

**Upgrade:**

1. Take a backup first and confirm it uploaded:
   ```bash
   docker compose run --rm backup all
   ```
2. Note the current `RANGKA_TAG`, set the new pinned tag in `.env`.
3. Pull, rebuild the backup image (it is built from the release image), restart:
   ```bash
   docker compose pull
   docker compose build backup
   docker compose up -d
   docker compose ps
   ```
4. Check `docker compose logs --tail 50 jasa fnb` and the HTTPS check from section 3, step 7.
5. The nightly dump uses `pg_dump` (`BACKUP_METHOD=pg_dump`, the default), never by itself anything else. After
   the first release that has `rangka backup` / `rangka restore` (S1, contract 0005 section 2), switch to it only
   deliberately: set `BACKUP_METHOD=rangka` (`auto` picks it when the binary has it; `rangka` fails when it does
   not), run one backup by hand (`docker compose run --rm backup all`), then a restore drill (section 7) that
   passes on that archive. Only then leave it on. Revert by removing the line.

**Rollback:** set the previous tag in `.env`, `docker compose up -d`. If the new release's migration changed data
destructively, the old binary is not enough: restore from the pre-upgrade archive (section 6).

**HTTPS does not come up:** `dig +short <host>` must print the Elastic IP; ports 80 and 443 must be open in the
security group; `CADDY_TLS` must be a valid email; read `docker compose logs caddy`.

## 6. Restore in production

Restore only goes into a **fresh** database and **empty** `apps`, `site`, `data`, `connectors` folders:
`restore.sh` refuses anything else (`database <pilot> is not empty ...` or `<folder> is not empty ...`) and has
no override. It verifies the archive checksums before touching any database. Do the pilots one at a time. The
procedure is modelled on `restore-drill.sh`.

You need the age private key on this machine for the duration of the restore only (from the password manager,
for example `/root/juragan-backup.key`, mode 600). **Delete it afterwards.**

0. **Stop the nightly timer** so no backup runs during the restore (start it again in the last step):
   ```bash
   sudo systemctl stop juragan-backup.timer
   ```
1. **Get the archive** into `BACKUP_DIR` (set to `/opt/juragan/backups` in section 2; mounted at `/backups` in the backup container).
   The local archive of the night you want is already in `BACKUP_DIR/<pilot>/`. For an off-box one, copy it in
   with the same rclone settings the backup uses:
   ```bash
   sudo mkdir -p /opt/juragan/backups/in
   docker compose run --rm -T --no-deps --entrypoint rclone backup \
     copyto "offbox:<bucket>/<pilot>/<archive name>" "/backups/in/<archive name>" --s3-no-check-bucket
   ```
2. **Stop the pilot** (DESTRUCTIVE from here: downtime for that pilot):
   ```bash
   docker compose stop <pilot>
   ```
3. **Set the current database aside** (a rename keeps the old data; it needs no open connections, which the
   stop above guarantees), then create an empty one owned by the pilot role, as `10-pilots.sh` does:
   ```bash
   docker compose exec -T postgres psql -U postgres -v ON_ERROR_STOP=1 <<'SQL'
   ALTER DATABASE <pilot> RENAME TO <pilot>_old_<yyyymmdd>;
   CREATE DATABASE <pilot> OWNER <pilot>;
   REVOKE CONNECT ON DATABASE <pilot> FROM PUBLIC;
   SQL
   ```
   (`<pilot>_old_<yyyymmdd>` is a literal new name you choose. Drop it with `DROP DATABASE` only after the
   restored pilot is confirmed good.)
4. **Move the pilot folders aside** (so they are empty; `backups/` stays in place):
   ```bash
   cd "$(grep '^PILOTS_DIR=' .env | cut -d= -f2-)"/<pilot>
   sudo mkdir ../<pilot>-old-<yyyymmdd>
   # a pilot may have no connectors/ folder: `|| true` only skips a missing folder; the next step's
   # "not empty" refusal in restore.sh still catches anything left behind
   sudo mv apps site data connectors ../<pilot>-old-<yyyymmdd>/ 2>/dev/null || true
   cd /opt/juragan/rangka-os/deploy/juragan
   ```
   If a folder does not exist, that is fine.
5. **Run restore.sh** in the backup image, with the pilot folder mounted read-write at `/restore/<pilot>` and
   the age key at `/run/secrets/age-identity`:
   ```bash
   docker compose run --rm -T --no-deps \
     -v <path to age key>:/run/secrets/age-identity:ro \
     -v "$(grep '^PILOTS_DIR=' .env | cut -d= -f2-)/<pilot>:/restore/<pilot>" \
     --entrypoint /usr/local/bin/restore.sh backup "/backups/<pilot>/<archive name>" <pilot>
   ```
   (`/backups/in/<archive name>` if you copied it in step 1.) Expected last line:
   `restore ok: <N> tables, <M> rows match`. Earlier lines: `restore: checksums ok`,
   `restore: unpacked apps, site and files into /restore/<pilot>`. Any `MISSING`, `MISMATCH` or
   `restored data does not match the manifest` means do not start the pilot: keep the old database and folders,
   and escalate.

   If `restore.sh` fails after `pg_restore` (a folder or table-comparison error, a killed run), the database and
   the folders are no longer empty and it refuses a rerun. Empty them again, then run step 5 once more: stop the
   pilot (it should be stopped), drop and recreate the database as in step 3 (the failed attempt is worthless, so
   `DROP DATABASE <pilot>;` first, then the same `CREATE DATABASE` and `REVOKE` statements), and move the partly
   filled folders aside as in step 4 (use a new `<pilot>-old-<yyyymmdd>b` name).
6. **Fix ownership and start:**
   ```bash
   P="$(grep '^PILOTS_DIR=' .env | cut -d= -f2-)/<pilot>"
   sudo mkdir -p "$P/backups"
   sudo chown -R 65532:65532 "$P"
   docker compose up -d <pilot>
   ```
7. **Check:** `curl -sS -o /dev/null -w '%{http_code}\n' https://<host>/.well-known/openid-configuration` prints
   `200`, `docker compose logs --tail 50 <pilot>` is clean, and a login works.
8. Remove the age key: `shred -u <path to age key>` (or remove it from the machine). Remove the copied-in
   archive too (`sudo rm -f /opt/juragan/backups/in/<archive name>`; it is encrypted, but there is no reason to keep it).
   Start the nightly timer again and confirm it is scheduled:
   ```bash
   sudo systemctl start juragan-backup.timer
   systemctl list-timers juragan-backup.timer
   ```

## 7. Restore drill on a second machine

Proves that the newest **off-box** backup restores, decrypts and boots. Go-live gate: a passed drill within the
24 hours before go-live; then weekly. Run it on a different machine from production (a laptop or a small VM)
that has:

- Docker with the compose plugin and a checkout of this repo (`deploy/juragan/`).
- A drill env file with only the off-box settings, **not** the production DB passwords (the drill makes its own):
  `RANGKA_TAG`, `BACKUP_BUCKET`, `JASA_ORG` / `FNB_ORG` (only if production uses other ids than `jasa` / `fnb`), `RCLONE_CONFIG_OFFBOX_TYPE=s3`, `RCLONE_CONFIG_OFFBOX_PROVIDER=AWS`,
  `RCLONE_CONFIG_OFFBOX_REGION=ap-southeast-3`, `RCLONE_CONFIG_OFFBOX_ACCESS_KEY_ID`,
  `RCLONE_CONFIG_OFFBOX_SECRET_ACCESS_KEY`. Use the optional read-only key here. Mode 600. On Biznet Gio set
  `RCLONE_CONFIG_OFFBOX_PROVIDER=Other` and `RCLONE_CONFIG_OFFBOX_ENDPOINT=<service URL>` instead.
- The age private key file from the password manager. **Delete it from the machine after the drill.**

```bash
cd deploy/juragan
./restore-drill.sh --pilot all --env <drill env file> --identity <age key file>
```

Options: `--pilot jasa|fnb|all`, `--archive <file>` (needs one pilot), `--log <file>`, `--max-age <hours>`
(default 48), `--keep`. Per pilot the drill prints the archive's age (from the UTC stamp in its name) and fails
when it is older than `--max-age`: that means the nightly backup has stopped. It also warns when the archive was
made with another `RANGKA_TAG` or image than the drill env file names. Per pilot the drill fetches the newest archive, starts a throwaway compose project (`juragan-drill-<epoch>`, own Postgres,
random passwords, no Caddy), runs `restore.sh`, requires `migrate --dry-run` to plan 0 statements, boots the
kernel and expects `GET /.well-known/openid-configuration` = 200. The project and its temp folder are removed on
exit.

Pass condition: one line per pilot, also appended to `drill.log` in `deploy/juragan/`:

```text
DRILL PASSED jasa jasa-<stamp>.tar.age <UTC time>
DRILL PASSED fnb fnb-<stamp>.tar.age <UTC time>
```

and exit code 0. Along the way you see `restore: checksums ok` and `restore ok: <N> tables, <M> rows match`.
A failure prints `DRILL FAILED <pilot>` and exits non-zero.

| Message | Meaning |
| --- | --- |
| `no archive for <pilot> in offbox:<bucket>/<pilot>/` | No backup yet for that pilot, **or a wrong `BACKUP_BUCKET`**: check the name first |
| `cannot list offbox:... (rclone exit N)` | Bad keys, region or network |
| `checksum verification failed` / `decrypt failed` | Damaged archive or wrong age key; no database was touched |
| `database <pilot> is not empty` | Only with a reused database; the drill always starts fresh |
| `migrate --dry-run would apply N statement(s)` | `RANGKA_TAG` in the drill env differs from the production tag |

## 8. Local test (no cloud)

`deploy/juragan/local/e2e.sh` builds a local image, runs the whole stack with a self-signed CA on
`*.localhost` ports 8081/8443, and runs three sections: the stack (database isolation, HTTPS, seed and login),
backup (archive, off-box upload, failed upload), and drill (restore, corrupted or truncated archives, refusal of
a non-empty database). The output counts the checks (about 71). Needs Docker.

```bash
deploy/juragan/local/e2e.sh          # tears down on exit
KEEP=1 deploy/juragan/local/e2e.sh   # leaves the stack running
```

## 9. Go-live checklist

Mapped to the program's go-live gate (per pilot). All of it before Dede signs off.

- [ ] HTTPS works on both hosts (`curl` of `/.well-known/openid-configuration` returns 200 on `https://jasa.juragan.ai` and `https://fnb.juragan.ai`).
- [ ] A backup of each pilot is in the bucket (`jasa/`, `fnb/` hold `.tar.age` objects from the last night, timer log clean).
- [ ] Restore drill passed on another machine within the last 24 hours (`DRILL PASSED` lines for both pilots in `drill.log`); the drill's age key file deleted afterwards.
- [ ] Secrets only in `/opt/juragan/rangka-os/deploy/juragan/.env`, mode 600 (`stat -c %a .env` prints `600`); not in git, not in a bundle; age private key not on the VM.
- [ ] `juragan-backup.timer` enabled (`systemctl list-timers juragan-backup.timer` shows the next run).
- [ ] healthchecks.io ping alert set for `BACKUP_PING_URL` and verified green.
- [ ] Bucket: Object Lock (governance, 30 days), versioning, 35-day expiry rule, Block Public Access all confirmed.
- [ ] Bundle (S9): `auth.issuer` is `https://<host>`, `files.path: ./data/files`, `db.url: '${DATABASE_URL}'`.
- [ ] Owner seeded; `RANGKA_OWNER_*` blank in `.env`.
- [ ] Gate items owned elsewhere: the spec's day-one list works with the pilot's real opening data, the permission matrix test passes (cross-org and cross-outlet requests return 403), the Review Focus tests pass (`local/e2e.sh`).
- [ ] Dede signs off.
