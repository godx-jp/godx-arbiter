# Contributing

Thanks for digging in. This file covers the development loop, the
project's style conventions, and the few non-obvious rules.

## Prerequisites

- Go **1.23+** (`anthropic-sdk-go` requires it; `go.mod` enforces this)
- `make`
- For docs: Python 3.x + `pip install mkdocs-material`
- For lint: `golangci-lint` (`brew install golangci-lint` /
  `go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0`)

## Build, test, lint

```bash
make build       # binary → bin/arbiter
make test        # go test -race -count=1 ./...
make vet         # go vet
make lint        # golangci-lint, falls back to go vet
make smoke       # end-to-end: pipe a synthetic hook payload through the binary
make cross-compile   # linux/darwin/windows × amd64/arm64 → dist/
```

CI runs `vet`, `lint`, `test`, `smoke`, and the cross-compile matrix on
every push.

## Repository layout

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the canonical
package map. In one paragraph: `cmd/arbiter` is the CLI;
`internal/{hookio, projectfind, config, policy}` is fast-path;
`internal/{agent, tools, skills, notify}` is slow-path; `internal/proxy`
is Mode B (with sub-packages for routing, classification, translation,
budget); `internal/{adapter, mcp, eventlog, usage, auth}` are the
remaining cross-cutting layers.

## Code style

- **Comments only when the WHY is non-obvious.** Identifiers should
  document themselves; comments explain hidden constraints, subtle
  invariants, and surprising behaviors. Don't narrate what the code
  does.
- **Don't add error handling for impossible cases.** Trust internal
  callers and framework guarantees. Validate at system boundaries
  (user input, network, untrusted JSON) only.
- **No backwards-compatibility hacks.** No `// removed` comments,
  no rebrand-shims, no preserved public APIs that nothing internal
  uses. If something is unused, delete it.
- **Match Go idioms.** `gofmt` and `goimports` are mandatory.
  `golangci-lint run` is a hard CI gate.
- **Tests live next to the code they exercise**, in
  `<package>_test.go`. Integration tests for the binary itself live
  in `cmd/arbiter/integration_test.go` (uses `package main_test`).

## Adding a new hook subcommand

1. Add a top-level `case` in `cmd/arbiter/main.go`'s `switch`.
2. Implement `runFoo(args []string)` in a new
   `cmd/arbiter/<name>.go`. Keep the file small — heavy logic moves
   into `internal/<package>`.
3. Add an entry to `printUsage`.
4. Update [docs/CLI.md](docs/CLI.md) — every subcommand needs a
   reference section.
5. Add an integration test in
   `cmd/arbiter/integration_test.go`.

## Adding a new decision-support tool

1. Implement the `Tool` interface in
   `internal/tools/<name>.go`:
   ```go
   type Tool interface {
       Name() string
       Description() string
       InputSchema() map[string]any
       Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
   }
   ```
2. Register it in `internal/tools/registry.go:DefaultRegistry()`.
3. Update [docs/MCP_TOOLS.md](docs/MCP_TOOLS.md) — it's the user-
   facing catalog *and* the contract for external MCP consumers.
4. Add focused unit tests in `internal/tools/registry_test.go`.

The tool is now available to both the slow-path agent (via
`tools.DefaultRegistry`) and external Claude sessions (via
`arbiter mcp`) with no further wiring.

## Adding a new notify channel

1. Implement the `Channel` interface in `internal/notify/<name>.go`.
2. Register it in `internal/notify.DefaultRegistry()`.
3. Add fixture-driven tests if the channel hits the network
   (use `httptest.NewServer`).
4. Document the env vars / configuration knobs in
   [docs/CONFIG.md](docs/CONFIG.md).

## Adding a new CLI adapter

1. Implement `internal/adapter.Adapter` in
   `internal/adapter/<name>.go`.
2. Register in `adapter.NewRegistry`.
3. Update the capability matrix in
   [docs/MULTI_CLI.md](docs/MULTI_CLI.md).

## Documentation

- User-facing docs live in `docs/`. They're rendered by `mkdocs build`
  via `.github/workflows/docs.yml` to GitHub Pages on push to `main`.
- Code-level docs use Go's standard package + symbol comments (one
  short paragraph for the package, doc comments on every exported
  symbol).
- The `index.md` in docs/ is the docs site landing page; the
  `README.md` at the repo root is for crawlers + first-time visitors.

## Commits

- One logical change per commit. Avoid mixing refactors with feature
  work.
- Subject line: `<scope>(<area>): <imperative>` is preferred but not
  enforced.
- Body explains the *why*. The diff already shows the what.
- Include a `Co-Authored-By:` line if pair-programming or AI-assisted.

## Releases

Tags `vX.Y.Z` trigger `.github/workflows/release.yml`:

1. Cross-compile the linux/darwin/windows matrix (5 binaries).
2. Generate `.sha256` checksums per binary.
3. Upload all artifacts to the GitHub Release.
4. Sync `npm/package.json` version.
5. `npm publish --access public` (requires `NPM_TOKEN` secret).

Manual checks before tagging:

```bash
make test                # go test -race
make smoke               # binary works on this host
git diff main...HEAD     # nothing surprising
```

## Reporting bugs

Open an issue in
https://github.com/godx-team/godx-arbiter/issues with:

1. `arbiter version` output
2. `arbiter doctor --json` output (redact API keys; the flag already
   does this)
3. Reproduction steps
4. Expected vs actual decision (paste from `arbiter explain --last`)

## Security issues

Please **do not** open a public issue for security bugs. See
[SECURITY.md](SECURITY.md) for the disclosure process.
