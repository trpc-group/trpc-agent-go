# AGENTS.md

## Cursor Cloud specific instructions

### Project overview

tRPC-Agent-Go is a Go multi-module monorepo (library/framework) for building AI agent systems. It is **not** a standalone application — there is no single `main.go` to run. The root module path is `trpc.group/trpc-go/trpc-agent-go`.

### Go version

The root `go.mod` requires Go 1.21. The environment has Go 1.22+ pre-installed, which is compatible. Some sub-modules (e.g. under `test/`) require Go 1.24+; `go mod download` handles this automatically via toolchain directives.

### Common commands

| Task | Command | Notes |
|------|---------|-------|
| Build | `go build ./...` | Root module only |
| Unit tests | `go test ./...` | Root module; all tests use mocks, no API keys needed |
| E2E tests | `cd test && go test ./...` | Separate module in `test/` |
| Lint | `golangci-lint run --timeout=10m` | Config in `.golangci.yml` |
| gofmt check | `gofmt -r 'interface{} -> any' -l .` | CI enforces `any` over `interface{}` |
| goimports check | `goimports -l .` | |
| All sub-module tests (CI-style) | `bash .github/scripts/run-go-tests.sh` | Runs tests across ~80 modules excluding examples/docs/test |
| Check example builds | `bash .github/scripts/check-examples.sh` | |

### Non-obvious caveats

- **GOPATH/bin must be on PATH** for `golangci-lint` and `goimports` to work. The update script handles installation, and `~/.bashrc` exports the path. If a tool is missing, run: `export PATH="$PATH:$(go env GOPATH)/bin"`.
- **No external API keys needed for tests.** The entire test suite uses mocks. API keys (e.g. `OPENAI_API_KEY`) are only needed to run the examples under `examples/`.
- **Multi-module monorepo:** There are ~80 `go.mod` files. Running `go test ./...` from the repo root only tests the root module. To test all modules, use the CI script `.github/scripts/run-go-tests.sh`.
- **SQLite CGO dependency:** The root module depends on `github.com/mattn/go-sqlite3`, which requires CGO. Ensure `CGO_ENABLED=1` (the default) and a C compiler is available.
- **License headers required on all `.go` files.** CI checks that every Go file has the Tencent Apache 2.0 header. See `CONTRIBUTING.md` for the template.
