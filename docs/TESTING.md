# Testing — kombify StackKits

> Last verified: 2026-05-12

Use the smallest gate that proves the changed surface, then broaden when a shared contract changed.

## Gate Matrix

| Surface | Command | Purpose |
| --- | --- | --- |
| Go packages | `go test ./...` | Whole-repo Go behavior. |
| Go race/unit loop | `go test -v -race -short ./pkg/... ./internal/...` | Fast local package gate. |
| Coverage | `go test -coverprofile=coverage/coverage.out -covermode=atomic ./...` | Coverage floor and drift. |
| Static analysis | `go vet ./...`, `golangci-lint run ./...` | Compiler-adjacent and configured lint checks. |
| CUE schemas | `cue vet ./base/... ./base-kit/... ./modern-homelab/... ./ha-kit/...` | Kit and base schema validation. |
| Module CUE | `cue vet -c=false ./modules/...` | Module contract validation with incomplete values allowed. |
| CUE binding | `make test-cue-binding` | Module contracts plus Go binding/composition/generate packages. |
| Website | `mise run test:website` | Static website structure, links, scripts, and build output. |
| Release-note surface | `node --test scripts/release/changelog.test.mjs` | Parser coverage for website changelog JSON and public release notes. |
| Public release-note export | `node --test scripts/release/changelog.test.mjs && npm --prefix website run build` | Public website release-note JSON and built changelog section. |
| API/OpenAPI | `go test ./internal/api ./api/openapi/...` | API handler and OpenAPI embed behavior. |
| Post-apply verification | `stackkit verify --json` | Read-only host validation. |
| HTTP verification | `stackkit verify --http --json` | Generated service URL reachability. |
| Secret scan | `gitleaks detect --no-git --source . --config .gitleaks.toml` | Current-tree secret detection when `gitleaks` is available. |
| Vulnerability scan | `govulncheck ./...` | Reachable Go vulnerability detection. |
| Docker build | `docker build .` | Server image build verification. |
| BaseKit fresh Ubuntu VM | `mise run test:vm:local` | SK-S1 fresh target path: prepare, init, generate, apply, verify Hub and default services. |
| Release archive toolchain | `.github/workflows/release.yml` archive validation | Every public archive must contain both `stackkit` and the packaged `tofu` binary. |

## Local-First Rule

Debug release candidates locally before relying on remote CI. For docs-only changes, link checks are sufficient unless `website/`, install snippets, CLI examples, or test docs changed.

Local gates MUST NOT rely on a host-installed OpenTofu binary. OpenTofu is a StackKit release-package component, not a developer or user prerequisite. Any test that proves install, prepare, plan, apply, or verify behavior must either use the packaged StackKit OpenTofu binary or start from a fresh target where `tofu` is absent before StackKit installs its packaged copy. The BaseKit fresh-VM gate explicitly fails if the target already has `tofu` on `PATH`.

For code, CUE, config, deployment, or user-facing behavior changes:

```bash
go test ./...
cue vet ./base/... ./base-kit/... ./modern-homelab/... ./ha-kit/...
make test-cue-binding
```

## Release Candidate Preflight

Before the first official OSS release, and before every tag that should reach `kombifyio/stackKits`, run the SpeechKit-style public-surface gate from the private repo:

```powershell
node --test scripts/release/changelog.test.mjs && npm --prefix website run build
```

The preflight verifies:

- focused Go release packages,
- kit CUE contracts,
- `CHANGELOG.md` release-note parsing,
- `website/build/changelog.json` and the `Latest Release Notes` website section,
- sanitized allowlist export from `scripts/public/export-manifest.txt`,
- Go tests in the exported public tree,
- release and live-test workflow syntax through `actionlint`.

Use `-SkipCue`, `-SkipGoTests`, `-SkipWebsite`, or `-SkipActionlint` only for narrow local debugging. A release-candidate receipt should use the full command.

## Canonical Scenarios

[STACKKIT_TEST_SCENARIOS.md](STACKKIT_TEST_SCENARIOS.md) defines the canonical scenario set for topology, domain mode, identity bootstrap, platform adapter selection, and application placement.

Every new module should map to at least one canonical scenario before it becomes part of the release default.

## Production-Style Tests

Production-like tests live under `../tests/production/` and are documented in private production-test runbook.

Important targets:

- `TestProductionReadinessLocalHomeLocalhost`
- `TestProductionReadinessKombifyMeSubdomains`
- `TestSimCloudSSORedirectConfigured`

When Docker Hub rate-limits anonymous pulls, seed the Ubuntu VM target with either `STACKKIT_FRESH_VM_DOCKER_CONFIG` or `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON`.

## First BaseKit Live Test Sequence

Use this sequence for the first BaseKit live validation after the preflight passes:

1. Start from a clean release candidate in the private repo; only intended release changes should be present.
2. Run `private BaseKit live preflight` and keep the output as the local release-candidate evidence.
3. Ensure `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` is configured in GitHub Actions secrets, or provide `STACKKIT_FRESH_VM_DOCKER_CONFIG` locally.
4. Dispatch `.github/workflows/production-tests.yml` with `run_basekit_live=true`, `run_live_sim=false`, and `run_installer=false`.
5. Verify the `stackkit-SK-S1-homelab` artifact contains `status=success`, the Hub URL, browser URL, default service URLs, target metadata, and logs hint.
6. Only after SK-S1 passes, run the public-history reset/tag path described in private release runbook.

## Post-Deployment Verification

`stackkit verify` is intentionally read-only. Use it after `stackkit apply`, in VM tests, and in managed deployment jobs.

Recommended patterns:

```bash
stackkit apply --auto-approve --verify
stackkit apply --auto-approve --verify-http --verify-strict
stackkit verify --host <host> --remote-dir /opt/stackkit --json
stackkit verify --json
```

Warnings do not fail by default unless `--strict` is used.

## Local Caveats

- On Windows, shell runners under `tests/**/*.sh` need Bash/WSL or Linux CI.
- `go test -race` requires CGO; if local CGO is disabled, run non-race tests locally and rely on Linux CI for the race gate.
- Live kombify, Auth0, Cloudflare, Simulate, and proxy tests require secrets injected from the approved secret store. Do not hard-code them in docs or fixtures.

## Writing Tests

- Prefer table-driven Go tests for deterministic logic.
- Keep CUE fixtures close to the contract being validated.
- Add `testdata/` fixtures for golden generation behavior.
- Use VM/production-style tests only when local unit or CUE tests cannot prove the behavior.
