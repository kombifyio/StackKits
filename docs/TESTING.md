# Testing — kombify StackKits

## Test types

| Type | Command | Description |
|------|---------|-------------|
| Unit tests | `make test` | All Go tests |
| CUE validation | `cue vet ./...` | Schema validation |
| Docker build | `docker build .` | Build verification |

## Running tests

```bash
# All tests
make test

# CUE schema validation
cue vet ./...

# Specific package
go test ./pkg/...
```

## Production regressions

Production-like test targets are wired explicitly and documented in:

- `tests/production/README.md`

Do not rediscover hostnames or secret names ad hoc. Use the fixed target names:

- `stackkitsbase` for the local Base Kit reinstall regression
- `simulate.kombify.space` for the cloud/simulator auth regression

The canonical CI entrypoint is:

- `.github/workflows/production-tests.yml`

## CI requirements

All PRs must pass:
- `gofmt` — code formatting
- `go vet` — static analysis
- `cue vet` — CUE schema validation
- Unit tests
- Docker build

## Writing tests

- Test CUE schemas with valid and invalid input fixtures
- Test Go logic with table-driven tests
- Add test fixtures in `testdata/` directories
