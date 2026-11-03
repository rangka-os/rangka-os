#!/usr/bin/env node
// Builds the npm packages for one release into npm/out/:
//   cli-<os>-<arch>  one per platform, holding the native binary (os/cpu fields make npm pick one)
//   cli              @rangka-os/cli: the launcher, with the platform packages as optionalDependencies
//   create           @rangka-os/create: the project starter, with modules/apps/connectors/demo site copied in
//
//   node npm/scripts/stage.mjs --version 0.5.0 --dist dist            # after goreleaser (dist/artifacts.json)
//   node npm/scripts/stage.mjs --version 0.5.0 --bin-dir /tmp/bins    # rangka-<goos>-<goarch>[.exe] files
// Platforms without a binary are skipped with a warning (npm/scripts/check-staged.sh stages only the local one).
// Sibling repos (rangka-apps, rangka-connectors) default to ../rangka-apps and ../rangka-connectors
// (--apps-repo, --connectors-repo).
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync, chmodSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const npmDir = resolve(here, "..");
const repo = resolve(npmDir, "..");
const arg = (k, d) => { const i = process.argv.indexOf(`--${k}`); return i > 0 ? process.argv[i + 1] : d; };
const version = (arg("version") || "").replace(/^v/, "");
if (!/^\d+\.\d+\.\d+(-[\w.]+)?$/.test(version)) { console.error("--version x.y.z required"); process.exit(2); }
const out = resolve(arg("out", join(npmDir, "out")));
const appsRepo = resolve(arg("apps-repo", join(repo, "..", "rangka-apps")));
const connRepo = resolve(arg("connectors-repo", join(repo, "..", "rangka-connectors")));

const OS = { darwin: "darwin", linux: "linux", windows: "win32" };
const CPU = { amd64: "x64", arm64: "arm64" };
const PLATFORMS = ["darwin-arm64", "darwin-x64", "linux-x64", "linux-arm64", "win32-x64", "win32-arm64"];

// 1. find the binaries
const bins = {};
const dist = arg("dist");
if (dist) {
  for (const a of JSON.parse(readFileSync(join(dist, "artifacts.json"), "utf8"))) {
    if (a.type === "Binary" && a.name.startsWith("rangka") && OS[a.goos] && CPU[a.goarch]) bins[`${OS[a.goos]}-${CPU[a.goarch]}`] = resolve(a.path);
  }
} else {
  const d = resolve(arg("bin-dir", "dist/bin"));
  for (const f of readdirSync(d)) {
    const m = f.match(/^rangka-(\w+)-(\w+?)(\.exe)?$/);
    if (m && OS[m[1]] && CPU[m[2]]) bins[`${OS[m[1]]}-${CPU[m[2]]}`] = join(d, f);
  }
}
if (!Object.keys(bins).length) { console.error("no binaries found"); process.exit(1); }

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
const base = JSON.parse(readFileSync(join(npmDir, "cli", "package.json"), "utf8"));
const common = { license: base.license, homepage: base.homepage, bugs: base.bugs };

// 2. platform packages
const optional = {};
for (const p of PLATFORMS) {
  if (!bins[p]) { console.warn(`  skip ${p}: no binary`); continue; }
  const [os, cpu] = p.split("-");
  const name = `@rangka-os/cli-${p}`;
  const dir = join(out, `cli-${p}`);
  mkdirSync(join(dir, "bin"), { recursive: true });
  const exe = os === "win32" ? "rangka.exe" : "rangka";
  cpSync(bins[p], join(dir, "bin", exe));
  chmodSync(join(dir, "bin", exe), 0o755);
  writeFileSync(join(dir, "package.json"), JSON.stringify({
    name, version, description: `The rangka binary for ${p} (installed by @rangka-os/cli).`, ...common,
    repository: { ...base.repository, directory: "npm" }, os: [os], cpu: [cpu], files: ["bin"], preferUnplugged: true,
  }, null, 2) + "\n");
  writeFileSync(join(dir, "README.md"), `# ${name}\n\nThe native \`rangka\` binary for ${p}. Install [@rangka-os/cli](https://www.npmjs.com/package/@rangka-os/cli) instead; npm picks this package for you.\n`);
  optional[name] = version;
  console.log(`  ${name}@${version}  ${(statSync(bins[p]).size / 1e6).toFixed(1)} MB`);
}

// 3. the launcher
cpSync(join(npmDir, "cli"), join(out, "cli"), { recursive: true });
writeFileSync(join(out, "cli", "package.json"), JSON.stringify({ ...base, version, optionalDependencies: optional }, null, 2) + "\n");
console.log(`  @rangka-os/cli@${version}`);

// 4. the starter, with the modules, apps, connectors and demo site of this release (contract 0001 layout:
// rangka-apps/modules/<m>/module.rk, apps/<a>/app.rk, examples/sites/<site>, examples/fixtures/<site>).
// DEMOS maps a create template to its example site; its fixtures share the site's name.
const DEMOS = { demo: "kopi-senja" };
const need = (p) => { if (!existsSync(p)) { console.error(`stage: ${p} not found (rangka-apps layout changed?)`); process.exit(1); } return p; };
const cdir = join(out, "create");
cpSync(join(npmDir, "create"), cdir, { recursive: true, filter: (s) => !s.includes(`${join("create", "template")}`) });
const T = join(cdir, "template");
rmSync(join(cdir, "files"), { recursive: true, force: true });
rmSync(join(cdir, "blank"), { recursive: true, force: true });
cpSync(join(npmDir, "create", "files"), join(T, "base"), { recursive: true });
for (const d of ["modules", "apps"]) cpSync(need(join(appsRepo, d)), join(T, "base", d), { recursive: true });
let connectors = 0;
for (const c of readdirSync(need(connRepo))) {
  if (existsSync(join(connRepo, c, "connector.rk"))) { cpSync(join(connRepo, c), join(T, "base", "connectors", c), { recursive: true }); connectors++; }
}
if (!connectors) { console.error(`stage: no <name>/connector.rk in ${connRepo}`); process.exit(1); }
for (const [tpl, site] of Object.entries(DEMOS)) {
  cpSync(need(join(appsRepo, "examples", "sites", site)), join(T, tpl, "site"), { recursive: true });
  cpSync(need(join(appsRepo, "examples", "fixtures", site)), join(T, tpl, "fixtures"), { recursive: true });
}
cpSync(join(npmDir, "create", "blank"), join(T, "blank"), { recursive: true });
const cpkg = JSON.parse(readFileSync(join(npmDir, "create", "package.json"), "utf8"));
writeFileSync(join(cdir, "package.json"), JSON.stringify({ ...cpkg, version }, null, 2) + "\n");
console.log(`  @rangka-os/create@${version}`);
console.log(`staged into ${out}`);
