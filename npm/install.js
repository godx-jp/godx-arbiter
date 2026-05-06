#!/usr/bin/env node
// godx-arbiter — postinstall: download the right prebuilt binary for
// this platform from GitHub Releases, verify the SHA-256 checksum, and
// place it next to bin/arbiter.js so the npm-shipped shim can exec it.
//
// Pattern modeled after esbuild / @biomejs/biome / turbo: the npm
// package is a tiny wrapper; the binary is fetched per-install. Keeps
// the registry footprint small and lets us cross-compile without
// shipping every platform's binary in every install.

const fs = require("node:fs");
const fsp = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const https = require("node:https");
const crypto = require("node:crypto");
const zlib = require("node:zlib");
const { pipeline } = require("node:stream/promises");

const PKG = require("./package.json");
const REPO = "godx-team/godx-arbiter";
const VERSION = process.env.GODX_ARBITER_VERSION || PKG.version;

const PLATFORMS = {
  "linux-x64": { goos: "linux", goarch: "amd64", ext: "" },
  "linux-arm64": { goos: "linux", goarch: "arm64", ext: "" },
  "darwin-x64": { goos: "darwin", goarch: "amd64", ext: "" },
  "darwin-arm64": { goos: "darwin", goarch: "arm64", ext: "" },
  "win32-x64": { goos: "windows", goarch: "amd64", ext: ".exe" },
};

function platformKey() {
  const platform = process.platform;
  const arch = process.arch;
  return `${platform}-${arch}`;
}

async function main() {
  const key = platformKey();
  const target = PLATFORMS[key];
  if (!target) {
    console.error(`[godx-arbiter] unsupported platform: ${key}`);
    process.exit(1);
  }

  const filename = `arbiter-${target.goos}-${target.goarch}${target.ext}`;
  const url =
    process.env.GODX_ARBITER_DOWNLOAD_URL ||
    `https://github.com/${REPO}/releases/download/v${VERSION}/${filename}`;

  const binDir = path.join(__dirname, "bin");
  const binPath = path.join(binDir, "arbiter" + target.ext);
  await fsp.mkdir(binDir, { recursive: true });

  console.log(`[godx-arbiter] downloading ${url}`);
  const tmp = binPath + ".part";
  await downloadTo(url, tmp);

  const checksumURL = url + ".sha256";
  let expected = "";
  try {
    expected = (await downloadString(checksumURL)).trim().split(/\s+/)[0];
  } catch (e) {
    console.warn(
      `[godx-arbiter] checksum file not available (${e.message}) — proceeding without verification`
    );
  }
  if (expected) {
    const got = await sha256(tmp);
    if (got !== expected) {
      await fsp.unlink(tmp);
      console.error(
        `[godx-arbiter] checksum mismatch: got ${got}, want ${expected}`
      );
      process.exit(1);
    }
    console.log("[godx-arbiter] checksum ok");
  }

  await fsp.rename(tmp, binPath);
  await fsp.chmod(binPath, 0o755);
  console.log(`[godx-arbiter] installed → ${binPath}`);
}

function downloadTo(url, dest) {
  return new Promise((resolve, reject) => {
    https
      .get(url, { headers: { "User-Agent": "godx-arbiter-installer" } }, async (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          try {
            await downloadTo(res.headers.location, dest);
            resolve();
          } catch (e) {
            reject(e);
          }
          return;
        }
        if (res.statusCode !== 200) {
          reject(new Error(`HTTP ${res.statusCode} for ${url}`));
          return;
        }
        try {
          const out = fs.createWriteStream(dest);
          let stream = res;
          if (url.endsWith(".gz")) stream = res.pipe(zlib.createGunzip());
          await pipeline(stream, out);
          resolve();
        } catch (e) {
          reject(e);
        }
      })
      .on("error", reject);
  });
}

function downloadString(url) {
  return new Promise((resolve, reject) => {
    https
      .get(url, { headers: { "User-Agent": "godx-arbiter-installer" } }, async (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          try {
            resolve(await downloadString(res.headers.location));
          } catch (e) {
            reject(e);
          }
          return;
        }
        if (res.statusCode !== 200) {
          reject(new Error(`HTTP ${res.statusCode} for ${url}`));
          return;
        }
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
      })
      .on("error", reject);
  });
}

async function sha256(file) {
  const hash = crypto.createHash("sha256");
  await pipeline(fs.createReadStream(file), hash);
  return hash.digest("hex");
}

main().catch((e) => {
  console.error(`[godx-arbiter] install failed: ${e.message}`);
  process.exit(1);
});
