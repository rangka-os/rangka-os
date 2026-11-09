# 0036 Catalogue in the platform: modules and connectors

Status: proposed (8 Nov 2026) · Owner of the change: rangka-os (schema, validators, kernel, web app, Console) ·
Affects: rangka-apps and rangka-connectors (optional catalogue keys), rangka-ai (read-only tool), rangka-cloud (reads
the same API, never stores state) · Builds on: 0001, 0002, 0004, 0015, 0017, 0028, 0032, 0033

## Context

- An owner who is not technical sees modules (rangka-apps `modules/`) and connectors (rangka-connectors) only on
  GitHub. Inside their own platform they cannot see what exists, what it adds, or how to switch it on.
- The kernel already reads every module and every connector in its directories (`meta.Registry`), but reports only
  what is installed:
  - `/meta` `modules[]` lists installed units only.
  - `/meta` `connectors` (`InstalledConnectors`, `api/meta.go`) lists a connector only when a flow triggers on it or
    calls it, or when `system.rk` `notify` names it. There is no way to switch a connector on before a flow uses it.
- The Console shows "App di paket" (plan apps, installed or not) and a "Konektor" screen with installed connectors
  only. The end-user web app has no settings page at all.
- The pieces for acting already exist or are proposed:
  - 0032: adding or removing a module is a `config.modules` change set, judged by the difference in installed modules.
  - 0028: plans limit which apps and modules a site may install (`app_not_in_plan`, `module_not_in_plan`).
  - 0017: connector secrets are bound per org inside the tenant's kernel, written through a write-only API, and never
    pass through change sets.
  - 0033: company connectors, a test endpoint, and a second confirmation for money-touching writes.
  - 0004: versioned packages and an index, with the official set bundled in the binary.
- What is missing is the place that ties them together: a list of what is available, with plain words, and buttons
  that start those flows.

## Decision

### 1. What the catalogue is
- **Version 1 lists everything the kernel can install without a download:**
  - modules in the modules directory and apps in the apps directory;
  - connectors in the connectors directory;
  - the site's company connectors (`<site>/connectors/x_*`, 0033).
- The official set ships in the binary (0004 §5), so the catalogue works offline and on desktop.
- **Packages from the 0004 index** that are not on disk come later (§9). The API shape already allows them.
- **Same in every deployment.** The catalogue is computed by the tenant's kernel, the same way for self-hosted,
  desktop and Rangka Cloud.

### 2. Catalogue keys (schema 0.5, all optional)
- `module.json` and `app.json` gain:
  - `summary` (label, id/en): one sentence in plain words ("Catat pelanggan dan pemasok di satu tempat").
  - `category`: one of `sales`, `purchasing`, `stock`, `finance`, `tax`, `people`, `operations`, `messaging`,
    `payments`, `marketplace`, `other`.
  - `icon`: a Remix icon name from the design system.
- `connector.json` gains the same three keys. It already has `label`, `vendor` and `docs`, and gets `hosts` (0017 §5)
  and `money` (0033 §2).
- **Validator warning `catalogue_summary_missing`:** a module, app or connector in the shared directories with no
  `summary`. Company connectors are exempt. It is a warning, not an error, so existing files stay valid.
- **Before release:** until these keys are in a released schema, rangka-apps and rangka-connectors put them under
  `experimental:` (0000 §4).
- **Labels in the UI:** the web app shows `title`/`label` and `summary`, never the technical name.
  - Without a `summary` it shows the model labels the module adds ("Menambah: Pelanggan, Pemasok").

### 3. API: read
- **`GET /api/v1/catalogue`** returns `{modules: [...], apps: [...], connectors: [...]}` for the current org.
- **Module or app entry:**
  - `name`, `kind` (`module` | `app`), `title`, `summary`, `category`, `icon`, `version`;
  - `installed` (bool) and `installed_by` (0032 §4);
  - `requires` and `required_by` (installed units that need it);
  - `models`: label and name of each model that is not `is_child`;
  - `in_plan` (bool) and `plan` (the current plan's name, 0028);
  - `source`: `bundled` | `directory` (later `index`, §9).
- **Connector entry:**
  - `name`, `label`, `summary`, `category`, `icon`, `vendor`, `docs`, `version`;
  - `company` (bool, an `x_` connector);
  - `money` (computed as in 0033 §4), `hosts`, and `provides` (ports, 0002 §B);
  - `ports_selected`: the ports this connector is picked for in `system.rk` `ports:`;
  - `used_by`: flows that trigger on it or call it;
  - `auth`: per field, name, label and `secret`, plus `set` for this org (the same answer as
    `GET /api/v1/connectors/{c}/auth`, 0017 §3). No value is ever included.
  - `state`, one of:
    - `available`: not installed;
    - `needs_auth`: installed, a required field is unset for this org;
    - `untested`: a company connector, 0033 §3;
    - `ready`: installed and authenticated;
    - `error`: errors in the last 24 h, from `/system/status`.
  - `errors_24h` and `last_event_at`.
- **Who may read:** members for whom `/meta.changesets.propose` is true, the same rule that shows the Ubah toggle
  (0015).
  - That flag is false for delegated `ai: true` tokens (`a.Human`, `api/preview.go`). Such a token may still read when
    the person it acts for may propose.
  - Everyone else gets `403 forbidden`. The answer holds no secret values and no file contents.
- **MCP:** a read-only tool `catalogue_list` with the same answer. A token with `ai: true` may call it.

### 4. Install and remove a module or app
- **`POST /api/v1/catalogue/propose`** with `{add: [names], remove: [names], connect: [names]}` builds one change set and
  returns it, already validated and previewed.
  - The kernel writes the `config.rk` edit itself: `config.modules` for modules (0032 §1), `config.apps` for apps.
    The client never computes closures.
  - Title: `Pasang <title>` or `Lepas <title>`. `source` is `catalogue`.
  - Risk and preview follow 0032 §3:
    - adding is medium, and the preview lists the whole closure;
    - removing is medium, or high when the removed models hold rows;
    - a unit still required by another stays installed, and the preview says so.
  - Approval is the ordinary change-set flow: approve only from previewed, high is owner only. A person who may
    approve can approve and apply from the same card, as in Ubah (0015).
- **Out of plan:** an entry with `in_plan: false` cannot be proposed (`422 module_not_in_plan` / `app_not_in_plan`).
  The UI shows "Tidak termasuk paket <plan>" and, on Rangka Cloud, a link to the plan page (§8).
- **Refusals:** the request answers `422 catalogue_nothing_to_do` when `add` names an installed unit or `remove` names
  one that is not installed.

### 5. Connect a connector
Connecting has up to four steps. The screen shows them in order, each with its own state.

1. **Install (change set).**
   - New key `config.connectors: [names]` (schema 0.5 `config.json`) switches a connector on before any flow uses it.
   - The installed set becomes `InstalledConnectors` (flows, `notify`) plus `config.connectors`.
   - `connect: [names]` in §4's propose request writes `config.connectors`.
   - Risk: medium, or high when the connector is money-touching (0033 §4).
   - The change set lists missing secret fields under `requires:` (0017 §4). It can be applied, and the connector is
     `needs_auth` until the next step.
   - One site has one installed set. Per-org `orgs.<id>.config.connectors` is refused (`org_config_key`, as 0032 §1).
2. **Pick it for a port (change set), only when it `provides` a port that has no vendor yet.**
   - It is a `system.rk` `ports:` edit (0002 §B), so it is high and owner only (`risk.go`: system.rk).
   - The screen offers it after install ("Pakai Midtrans untuk QRIS?"). Raka never proposes it (§7).
3. **Credentials (no change set).**
   - The form calls `PUT /api/v1/connectors/{c}/auth/{field}` for the current org (0017 §3).
   - Only the owner and roles granted `connector_auth` see the form. Others see "Perlu diatur oleh pemilik".
   - With the `env`, `file` or `vault` driver the form is read only and says the server administrator sets the value
     (`409 secrets_read_only_driver`). It still shows whether each field is set.
   - Each org of a multi-org site sets its own values. An org without them sees `needs_auth` for that connector.
4. **Test (optional for catalogue connectors).**
   - "Uji sambungan" runs `POST /api/v1/connectors/{c}/test` (0033 §3), which runs one `GET` action once. A connector
     with no `GET` action has no test button.
   - For company connectors the 0033 rules apply unchanged: test before wiring, and confirm writes when money-touching.
   - For catalogue connectors the test does not gate flows in this contract (0033 Open).

**Disconnect.**
- "Putuskan" proposes removing the connector from `config.connectors` (medium). If a flow still uses it, it stays
  installed and the preview names the flow.
- "Hapus kunci" calls `DELETE /api/v1/connectors/{c}/auth/{field}` for each field. It needs `connector_auth`.

### 6. Screens
**Web app: "Modul dan sambungan" (`#/pengaturan/katalog`).**
- **Who sees it:** members for whom `/meta.changesets.propose` is true. It sits in the sidebar group "Lainnya" next
  to Persetujuan and Impor data. Sales roles do not see the entry, and the route answers "Tidak ada akses".
- **Two tabs:**
  - Modul: modules and apps together, grouped by `category`.
  - Sambungan: connectors grouped by `category`.
- **Search box and filters:** Semua · Terpasang · Belum dipasang.
- **Cards:** icon, label, one-line summary and a status word. Status is always a word, never a colour alone:
  - Terpasang, Belum dipasang, Tidak termasuk paket;
  - Perlu kunci, Belum diuji, Siap, Gangguan.
- **Detail page:**
  - what it adds (model labels);
  - what it needs (`requires`, with labels) and who uses it (`required_by`, `used_by`);
  - for connectors, the steps of §5 and the hosts it talks to ("Mengirim data ke graph.facebook.com").
- **Buttons:** Pasang, Lepas, Sambungkan, Atur kunci, Uji sambungan, Putuskan.
  - Pasang, Lepas, Sambungkan and Putuskan open the change-set card of Ubah (0015): preview, risk word, and approve
    when the person may.
- **Text:** Bahasa Indonesia first, sentence case, "kamu". Technical names appear only in a small muted line on the
  detail page.

**Console (technical view).**
- A new screen "Katalog" under "Lapis 5 · Apps" for modules and apps, showing:
  - versions and ranges, the closure, `installed_by`, file paths, and the `.rk` source (read only).
- The "Konektor" screen (Lapis 3) also lists connectors that are not installed, with `provides`, `ports_selected`,
  `hosts`, `money`, the auth fields with `set`/`hint`/`version` (0017 §3), and the same actions.
- **One set of rules:** the Console and the web app call the same endpoints.

### 7. Rangka AI (Raka)
- Raka may read the catalogue (`catalogue_list`, or `GET /api/v1/catalogue` with its delegated token) to suggest
  modules or connectors and explain them in plain words.
- Raka may propose the change sets of §4 and §5 step 1, through the MCP propose tool like any other change set.
  - `POST /api/v1/catalogue/propose` is refused to it (`403 ai_forbidden`, 0020 §4). It writes the same `config.rk`
    edit, and the kernel judges it the same way, because 0032 §3 rates the difference in installed modules.
  - The 0032 guardrails apply: adding is medium, and removing is marked high before drafting.
  - Money-touching connectors are high.
- Raka never proposes `system.rk` `ports:` (escalated, 0033 §5), never asks for a secret, and never calls the secrets,
  test or confirm endpoints (0017 §4, 0033 §3).
  - It links the owner to "Atur kunci" on the connector's page instead.

### 8. Where the state lives: the tenant's kernel, not Rangka Cloud
- **In the tenant's kernel:** the catalogue, the installed set (`config.rk`), connector states and secrets all live in
  each tenant's own kernel and database. Every screen in §6 talks to that kernel only.
- **Self-hosted** owners get exactly the same screens and rules as Rangka Cloud tenants.
- **Rangka Cloud** (closed source) does only this:
  - **Onboarding:** it reads `GET /api/v1/catalogue` through the kernel API and proposes the initial change set
    (0032 §7).
  - **Provisioning:**
    - It may write secrets with a `secrets:write` token, only when the owner types them into a Cloud onboarding form.
    - The value goes straight to the tenant's kernel (0017 §3).
    - The Cloud never stores a secret value, never logs it, and never reads it back (no endpoint returns one).
  - **Keys:** it supplies the key-encryption key through a KMS (0017 §2) and never sees data keys or values.
  - **Plans and billing:** the "Tidak termasuk paket" link (§4) opens the Cloud's plan page. A plan change reaches the
    kernel as `features.rk` (0028).
- **Not in Rangka Cloud:** a catalogue database, a copy of the installed set, or connector credentials.

### 9. Later: packages from the index (0004)
- Entries not on disk come from the package index with `source: "index"`, `available_version`, and the archive's
  checksum and signature.
- Installing one downloads and verifies the archive into `.rangka/packages/` before the change set is proposed. The
  change set records the checksum, and apply refuses a package whose checksum differs.
- An installed unit with a newer `available_version` shows "Versi baru tersedia". Upgrading follows 0004 §3
  (`rangka upgrade`) and 0032 §8 ranges.
- This needs 0004 implemented first and is not part of this contract's build order.

## Contract changes

**rangka-os**
- Schema 0.5:
  - `module.json`, `app.json` and `connector.json`: `summary`, `category`, `icon`;
  - `config.json`: `connectors` (array of connector names).
- Validators, Python and Go with identical reports (`make compare-python`):
  - `catalogue_summary_missing` (warning);
  - `unknown_connector` (existing error code) also for a `config.connectors` name with no connector;
  - `org_config_key` also covers `connectors`.
  - Testdata `kernel/testdata/catalogue-keys` and `broken-catalogue-keys`.
- Kernel:
  - `api.InstalledConnectors` adds `config.connectors`;
  - `GET /api/v1/catalogue`, `POST /api/v1/catalogue/propose` (§3, §4, §5), MCP tool `catalogue_list`;
  - `changeset.Assess` rates a `config.connectors` difference as in §5 step 1;
  - the `requires:` list of 0017 §4 for installs.
- Web app: route `#/pengaturan/katalog` and its sidebar entry (§6). Feature detection: `GET /api/v1/catalogue`
  answering 404 hides the entry.
- Console: screen "Katalog" and the extended "Konektor" screen (§6).
- Docs site: a concepts page "Katalog" and the API reference.

**rangka-apps and rangka-connectors** (owner of those repos, 0000 §1)
- Add `summary`, `category` and `icon` to each module, app and connector, under `experimental:` until the schema is
  released.

**rangka-ai**
- `catalogue_list` in the tool set. The guardrails of §7: `config.connectors` adds are medium, or high when
  money-touching, and `ports:` stays escalated.

**rangka-cloud** (not this repo)
- The onboarding reads the catalogue from the tenant kernel instead of its own copy of rangka-apps.
- The plan page link target (§4).

## Build order
1. Schema keys, validator codes, and `config.connectors` in `InstalledConnectors`.
2. `GET /api/v1/catalogue` and the MCP tool. The Console "Katalog" screen and the extended "Konektor" screen (read
   only).
3. `POST /api/v1/catalogue/propose` for modules and apps (0032 must be implemented), and the web app page with Pasang
   and Lepas.
4. Connect: `connect:` in propose, `requires:`, and the credentials form once 0017's `db` driver exists. Until then
   the form is read only under the `env` driver.
5. Test button (with 0033 §3) and port selection (§5 step 2).

## Gate
In the Kopi Senja demo, signed in as the owner, with nothing typed outside the web app:
- "Modul dan sambungan" lists `invoicing` as Belum dipasang. Pasang shows a medium change set whose preview lists its
  closure. Approving and applying it adds the module's menu group without a restart.
- WhatsApp Business shows Belum dipasang, then Perlu kunci after Sambungkan is applied. After Atur kunci it shows Siap.
- Throughout, no API answer, log, audit entry or change set holds the token's value.
- Signed in as `sales@contoh.test`, the entry is absent and `GET /api/v1/catalogue` answers 403.
- A module outside the plan shows "Tidak termasuk paket", and propose answers `module_not_in_plan`.

## Open
- Whether port selection should move from `system.rk` (high, layer 2) to `config.rk` `ports:` (layer 4). Picking a
  payment provider is a business choice, but it decides where money goes.
- Whether `admin` should see the credentials form by default (0017 Open, same question).
- Whether catalogue connectors with money should also need the test and the confirmation of 0033 §3–§4.
- Ratings, screenshots or "used by N businesses" on cards. They need data from Rangka Cloud, which this contract keeps
  out of the tenant.
- Whether the category list should be a schema enum (strict) or free text with a recommended list.
