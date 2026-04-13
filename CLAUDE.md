# CLAUDE.md

## SPEC

Always refer to @docs/Handout.md for instructions

## Project Layout

```
cmd/          # main applications (one subdir per binary)
internal/     # private app & library code (compiler-enforced)
pkg/          # public library code safe for external import
api/          # OpenAPI/Swagger specs, protobuf definitions
configs/      # config file templates
scripts/      # build, install, analysis scripts
build/        # packaging & CI configs
deployments/  # docker-compose, helm, terraform
test/         # integration tests & test data
```

**Rules:**
- `cmd/minik8s/main.go` should be thin — import and invoke from `internal/` or `pkg/`
- Prefer `internal/` over `pkg/` unless external reuse is intentional
- Never use `/src` — this is not Java

## Code Style

- Run `golangci-lint fmt` before every commit (non-negotiable)
- Use `golangci-lint run` for linting
- Follow [Effective Go](https://golang.org/doc/effective_go.html) naming conventions
  - Packages: short, lowercase, no underscores (`userstore`, not `user_store`)
  - Interfaces: single-method interfaces named by method + `-er` (`Reader`, `Stringer`)
  - Errors: `var ErrNotFound = errors.New(...)` for sentinel errors

## Go Modules

- Always use Go Modules (`go.mod` required)
- Module path format: `github.com/<org>/<repo>`
- Do not commit `/vendor` for libraries; it's acceptable for deployable apps

## Error Handling

- Always handle errors explicitly — never `_` an error silently
- Wrap errors with context: `fmt.Errorf("loading config: %w", err)`
- Return errors up the stack; avoid `log.Fatal` outside of `main`

## Testing

- Unit tests live alongside source: `foo_test.go` next to `foo.go`
- Integration/e2e tests go in `/test`
- Use `testdata/` subdirectories for test fixtures (Go ignores them during build)
- Table-driven tests are preferred

## Commands

```bash
go build ./...       # build all
go test ./...        # run all tests
go vet ./...         # static analysis
golangci-lint fmt    # format + fix imports
golangci-lint run    # lint (includes staticcheck, gofmt, and more)
```
