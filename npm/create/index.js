#!/usr/bin/env node
// npm create @rangka-os@latest [dir] [-- --name "Toko Maju" --template demo|blank --modules contacts,invoicing --email you@x.id --yes]
// Writes a Rangka OS project: modules, apps, connectors, a site (config, theme, layout), fixtures, .env,
// docker-compose.yml (Postgres) and package.json scripts that run the kernel from @rangka-os/cli.
// --modules composes the site from modules (contract 0032): config.modules with the copied modules' ranges and no
// apps. The validator adds what each module requires; this script never computes that.
"use strict";
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const readline = require("node:readline/promises");

const pkg = require("./package.json");
const TPL = path.join(__dirname, "template");

function parseArgs(argv) {
  const o = { _: [] };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--yes" || a === "-y") o.yes = true;
    else if (a === "--no-install") o.noInstall = true;
    else if (a === "--help" || a === "-h") o.help = true;
    else if (a.startsWith("--")) {
      const [k, v] = a.slice(2).split("=");
      o[k] = v !== undefined ? v : argv[++i];
    } else o._.push(a);
  }
  return o;
}

const slugify = (s) => s.toLowerCase().normalize("NFKD").replace(/[^\w\s-]/g, "").trim().replace(/[\s_]+/g, "-").replace(/-+/g, "-").replace(/^-|-$/g, "") || "rangka";
const titleOf = (slug) => slug.split("-").filter(Boolean).map((p) => p[0].toUpperCase() + p.slice(1)).join(" ");

function copyDir(src, dst, vars) {
  fs.mkdirSync(dst, { recursive: true });
  for (const e of fs.readdirSync(src, { withFileTypes: true })) {
    const name = { _gitignore: ".gitignore", _vscode: ".vscode", "env.example": ".env" }[e.name] || e.name;
    const s = path.join(src, e.name), d = path.join(dst, name.replace(/__SLUG__/g, vars.SLUG_UNDERSCORE));
    if (e.isDirectory()) copyDir(s, d, vars);
    else {
      let t = fs.readFileSync(s);
      if (/\.(rk|md|json|ya?ml|example|env|gitignore)$|^_gitignore$|^\.env/.test(e.name)) {
        t = Buffer.from(t.toString("utf8").replace(/\{\{(\w+)\}\}/g, (m, k) => (k in vars ? vars[k] : m)));
      }
      fs.writeFileSync(d, t);
    }
  }
}

/** "^major.minor" of modules/<name>/module.rk's version, or null when the module is not in the project. */
function moduleRange(dir, name) {
  const p = path.join(dir, "modules", name, "module.rk");
  if (!/^[a-z][a-z0-9_]*$/.test(name) || !fs.existsSync(p)) return null;
  const m = /^version:\s*['"]?(\d+)\.(\d+)/m.exec(fs.readFileSync(p, "utf8"));
  return m ? `^${m[1]}.${m[2]}` : null;
}

/** Turns the blank site into a composed one: config.modules instead of apps, no app layout or app numbering, plans
 *  without apps (a plan with neither apps nor modules allows every module). The kernel draws one menu group per
 *  module while the layout has no nav.side. */
function composeSite(dir, names) {
  const ranges = names.map((n) => [n, moduleRange(dir, n)]);
  const missing = ranges.filter(([, r]) => !r).map(([n]) => n);
  if (missing.length) {
    const have = fs.readdirSync(path.join(dir, "modules")).filter((n) => fs.existsSync(path.join(dir, "modules", n, "module.rk"))).sort();
    throw new Error(`not a module in this release: ${missing.join(", ")} (modules: ${have.join(", ")}; an app goes under --template)`);
  }
  const site = path.join(dir, "site");
  const cfgPath = path.join(site, "config.rk");
  let cfg = fs.readFileSync(cfgPath, "utf8");
  cfg = cfg.replace(/^# (.*): company settings\..*$/m, "# $1: company settings. Modules listed here are installed with what they require (contract 0032).")
    .replace(/^numbering:\n(?:[ ].*\n)*/m, "")
    .replace(/^layout: .*\n/m, "")
    .replace(/^apps: .*$/m, `modules: {${ranges.map(([n, r]) => `${n}: ${r}`).join(", ")}}`);
  fs.writeFileSync(cfgPath, cfg);
  const featPath = path.join(site, "features.rk");
  fs.writeFileSync(featPath, fs.readFileSync(featPath, "utf8").replace(/^ +apps: .*\n/gm, ""));
  fs.rmSync(path.join(site, "layouts"), { recursive: true, force: true });
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  if (args.help) {
    console.log(`Usage: npm create @rangka-os@latest [dir] -- [--name "Toko Maju"] [--template demo|blank] [--modules a,b] [--email you@x.id] [--yes]

  demo       the Kopi Senja demo company: the CRM app with sample customers, leads, deals and invoices
  blank      the same CRM app with your company name and no sample data
  --modules  no app: the modules you name (and what they require), e.g. --modules contacts,invoicing`);
    return;
  }
  const tty = process.stdin.isTTY && !args.yes;
  const rl = tty ? readline.createInterface({ input: process.stdin, output: process.stdout }) : null;
  const ask = async (q, def) => (rl ? (await rl.question(`${q} (${def}): `)).trim() || def : def);

  console.log(`\n  Rangka OS · create a project (v${pkg.version}, preview)\n`);
  const dirArg = args._[0] || (await ask("Folder proyek / project folder", "my-shop"));
  const dir = path.resolve(dirArg);
  if (fs.existsSync(dir) && fs.readdirSync(dir).length) {
    console.error(`  ${dirArg} is not empty; pick another folder.`);
    process.exit(1);
  }
  const name = args.name || (await ask("Nama usaha / business name", titleOf(slugify(path.basename(dir)))));
  const modules = typeof args.modules === "string" ? args.modules.split(",").map((m) => m.trim()).filter(Boolean) : [];
  let template = modules.length ? "blank" : args.template || (await ask("Template: demo (Kopi Senja, sample data) or blank", "demo"));
  if (!["demo", "blank"].includes(template)) template = "demo";
  const slug = slugify(path.basename(dir));
  const email = args.email || (await ask("Email pemilik / owner email", `owner@${slug}.local`));
  rl && rl.close();

  const vars = {
    NAME: name, SLUG: slug, SLUG_UNDERSCORE: slug.replace(/-/g, "_"), OWNER_EMAIL: email,
    OWNER_PASSWORD: crypto.randomBytes(9).toString("base64url"), CLI_VERSION: pkg.version, TEMPLATE: template,
  };
  copyDir(path.join(TPL, "base"), dir, vars);
  copyDir(path.join(TPL, template), dir, vars);
  if (modules.length) composeSite(dir, modules);

  const cmd = (s) => `\x1b[1m${s}\x1b[0m`;
  console.log(`
  Created ${path.relative(process.cwd(), dir) || "."} (${modules.length ? "modules " + modules.join(", ") : template}) for ${name}.

  Next:
    cd ${dirArg}
    ${cmd("npm install")}
    ${cmd("npm run db")}        start Postgres in Docker (or set DATABASE_URL in .env)
    ${cmd("npm run setup")}     validate, create tables, add ${template === "demo" ? "the demo data" : "your outlet"} and the owner
    ${cmd("npm run dev")}       http://localhost:8080/console/

  Owner sign-in: ${email} / ${vars.OWNER_PASSWORD} (saved in .env)
  Docs: https://rangka-os.github.io/rangka-os/
`);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
