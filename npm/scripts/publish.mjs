#!/usr/bin/env node
// Publishes npm/out (from stage.mjs): platform packages first, then @rangka-os/cli, then @rangka-os/create.
//   node npm/scripts/publish.mjs [--dry-run] [--provenance]
// Pre-release versions (0.5.0-alpha.1) go to the "next" dist-tag, others to "latest".
import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const out = resolve(dirname(fileURLToPath(import.meta.url)), "..", "out");
const dirs = readdirSync(out).sort((a, b) => (b.startsWith("cli-") - a.startsWith("cli-")) || (a === "create") - (b === "create"));
const flags = process.argv.slice(2).filter((f) => ["--dry-run", "--provenance"].includes(f));
for (const d of dirs) {
  const { name, version } = JSON.parse(readFileSync(join(out, d, "package.json"), "utf8"));
  const tag = version.includes("-") ? "next" : "latest";
  console.log(`publish ${name}@${version} (${tag})`);
  execFileSync("npm", ["publish", "--access", "public", "--tag", tag, ...flags], { cwd: join(out, d), stdio: "inherit" });
}
