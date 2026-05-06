# godx-arbiter (npm wrapper)

This is the npm wrapper for [godx-arbiter](https://github.com/godx-team/godx-arbiter)
— a Go binary distributed via GitHub Releases.

`npm install -g godx-arbiter` runs `install.js`, which:

1. Detects platform + arch (`linux-x64`, `darwin-arm64`, `win32-x64`, ...)
2. Downloads the matching binary from `github.com/godx-team/godx-arbiter/releases/download/v<version>/<file>`.
3. Verifies the `.sha256` companion checksum.
4. Places the binary at `node_modules/godx-arbiter/bin/arbiter[.exe]`.
5. The shim at `bin/arbiter.js` (declared in `bin` of `package.json`) execs that binary.

To override the download URL (private mirror / staging release), set
`GODX_ARBITER_DOWNLOAD_URL` before `npm install`.

To target a specific version: `npm install -g godx-arbiter@<version>`
(the npm version drives both the package and the binary tag).

After install:

```bash
arbiter --version
arbiter init
arbiter doctor
```

See the main repo for documentation:
https://github.com/godx-team/godx-arbiter
