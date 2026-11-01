# 0004: App packages and upgrades

Status: accepted in principle (1 Nov 2026) · Owner: rangka-os · Affects: rangka-apps, rangka-cloud, npm create

## Context
`npm create @rangka-os` copies apps into every project folder, so each installation becomes a fork. When CRM 0.2 ships, existing installations have no way to upgrade.

## Decision
1. **Modules and apps are versioned packages.**
   - `rangka-apps` CI builds each module and app at a tag into a signed archive (`crm-0.2.0.rkpkg`: files, manifest, checksum) and publishes it to an index (GitHub Releases first, `rangka-registry` later).
2. **Installations reference, they don't copy.**
   - The project folder holds `site/`, `custom/` and a lock file `rangka.lock` (`app: crm@0.2.0`, resolved modules, checksums).
   - Packages are cached in `.rangka/packages/`, and the kernel loads them read-only.
3. **Commands:**
   - `rangka install crm`
   - `rangka upgrade [crm]`: shows the plan (module versions, migrations, risk), then applies it as one change set with owner approval
   - `rangka rollback`
   - `rangka add-module` (for app authors)
4. **Company changes never get overwritten.** They stay in `custom/` overlays. On upgrade the kernel re-validates overlays against the new models, and a conflict blocks the upgrade with a clear message.
5. **Offline and desktop:** official packages for a release are also bundled inside the binary, so a desktop install works without internet.
6. **Rangka Cloud** uses the same commands per tenant, so Cloud and self-host never drift.
