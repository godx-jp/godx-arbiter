#!/usr/bin/env node
// Tiny shim — npm puts this on PATH. It exec's the platform binary
// downloaded by install.js. Keeping it node-runtime so we work on Linux,
// macOS, and Windows without per-platform shell scripts.

const path = require("node:path");
const { spawn } = require("node:child_process");

const ext = process.platform === "win32" ? ".exe" : "";
const bin = path.join(__dirname, "arbiter" + ext);

const child = spawn(bin, process.argv.slice(2), {
  stdio: "inherit",
  windowsHide: true,
});
child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
  } else {
    process.exit(code ?? 0);
  }
});
child.on("error", (err) => {
  console.error(`[godx-arbiter] failed to spawn binary at ${bin}: ${err.message}`);
  process.exit(1);
});
