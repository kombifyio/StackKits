# Testing — kombify StackKits

## Test types

| Type | Command | Description |
|------|---------|-------------|
| Go tests | `go test ./...` | Unit and integration-style Go packages |
| Go static analysis | `go vet ./...` and `golangci-lint run ./...` | Compiler-adjacent checks and configured lint rules |
| CUE validation | `cue vet ./base/...`, `cue vet ./base-kit/...`, `cue vet ./modern-homelab/...`, `cue vet ./ha-kit/...`, `cue vet -c=false ./modules/...` | Schema and module contract validation |
| CUE binding | `make test-cue-binding` | Module contracts plus Go binding/composition/generate packages |
| Website validation | `mise run test:website` | Verifies the static `website/` landing page, CLI reference, and required assets |
| Secret scan | `gitleaks detect --no-git --source . --config .gitleaks.toml` | Current-tree secret detection |
| Vulnerability scan | `govulncheck ./...` | Go reachable vulnerability detection |
| Admin validation | `cd kombify-admin && npm ci && npm audit --audit-level=low && npx prisma generate && npx tsc --noEmit` | Admin dependency and TypeScript schema gate |
| OpenTofu generation smoke | `stackkit generate` then `tofu init -backend=false` and `tofu validate` in `deploy/` | Generated IaC syntax and provider validation |
| Docker build | `docker build .` | Image build verification |
| VM smoke | `docker compose --profile cli up -d --build vm cli` then `go test -v -timeout=20m -tags=vm ./tests/vm/...` | CLI apply path against the local Docker VM, including the login gateway and `/health` smoke |

## Running tests

```bash
# All tests
go test ./...

# CUE schema validation
cue vet ./base/...
cue vet ./base-kit/...
cue vet ./modern-homelab/...
cue vet ./ha-kit/...
cue vet -c=false ./modules/...

# CUE module binding
make test-cue-binding

# Specific package
go test ./pkg/...
```

## Production regressions

Production-like test targets are wired explicitly and documented in:

- `tests/production/README.md`

Do not rediscover hostnames or secret names ad hoc. Use the fixed target names:

- Fresh Ubuntu VM target for the local Base Kit production-readiness regression
- `simulate.kombify.io` for the cloud/simulator auth regression
- `TestProductionReadinessLocalHomeLocalhost` for `base.home.localhost` + Step-CA
- `TestProductionReadinessKombifyMeSubdomains` for public kombify.me subdomains

`TestProductionReadinessLocalHomeLocalhost` creates its own blank Ubuntu target
with Docker-in-Docker, deploys the current checkout from scratch, and verifies
the standard services through their `*.home.localhost` HTTPS URLs. It must not
reuse or mutate a standing VM.

The canonical CI entrypoint is:

- `.github/workflows/production-tests.yml`

## CI requirements

All PRs must pass:
- `gofmt` — code formatting
- `go vet` — static analysis
- `cue vet` — CUE schema validation
- `make test-cue-binding` — CUE contract binding
- `mise run test:website` — static website structure and link sanity
- `go test ./...` and Linux CI race tests
- `govulncheck`, `gitleaks`, and `gosec`
- Admin `npm audit`, typecheck, and build gate
- Module and full composition integration jobs

## Writing tests

- Test CUE schemas with valid and invalid input fixtures
- Test Go logic with table-driven tests
- Add test fixtures in `testdata/` directories
