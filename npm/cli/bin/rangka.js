#!/usr/bin/env node
// Runs the native `rangka` binary from the platform package npm installed next to this one
// (@rangka-os/cli-<os>-<arch>, an optional dependency). RANGKA_BINARY overrides the path.
"use strict";
const { spawnSync } = require("node:child_process");
const fs = require("node:fs");

const key = `${process.platform}-${process.arch}`;
const exe = process.platform === "win32" ? "rangka.exe" : "rangka";
let bin = process.env.RANGKA_BINARY;
if (!bin) {
  try {
    bin = require.resolve(`@rangka-os/cli-${key}/bin/${exe}`);
  } catch {
    console.error(
      `rangka: no binary for ${key}. Supported: darwin-arm64, darwin-x64, linux-x64, linux-arm64, win32-x64, win32-arm64.\n` +
        `If your platform is listed, reinstall without --no-optional / --omit=optional.\n` +
        `Otherwise download a release from https://github.com/rangka-os/rangka-os/releases and set RANGKA_BINARY.`
    );
    process.exit(1);
  }
}
try {
  fs.accessSync(bin, fs.constants.X_OK);
} catch {
  try { fs.chmodSync(bin, 0o755); } catch { /* read-only install: spawn will report */ }
}
const r = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (r.error) {
  console.error(`rangka: ${r.error.message}`);
  process.exit(1);
}
if (r.signal) process.kill(process.pid, r.signal);
process.exit(r.status ?? 1);
