# Testing — kombify StackKits

> Last verified: 2026-05-17

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
| CUE binding | `mise run test:cue-binding` | Module contracts plus Go binding/composition/generate packages. |
| Website | `mise run test:website` | Cross-platform static website structure, installer scripts, Pages routing metadata, links, and build output. |
| Agent docs and MCP | `mise run test:website`; `go test ./cmd/stackkit-mcp ./cmd/stackkit/commands ./internal/api` | Public `llms.txt`, prompt Markdown, schema/OpenAPI mirrors, CLI agent helpers, MCP tool gating, and node-local management endpoints. |
| Release-note surface | `node --test scripts/release/changelog.test.mjs` | Parser coverage for website changelog JSON and public release notes. |
| Release evidence surface | `node --test scripts/release/release-evidence.test.mjs scripts/release/verify-release-attestations.test.mjs scripts/release/check-l3-paas-contract.test.mjs` | Machine-readable Enterprise release evidence JSON rendering, artifact hashing, attestation verification subject selection, and default StackKit-owned L3 PaaS-intent checks. |
| Default StackKit-owned L3 PaaS intent | `node scripts/release/check-l3-paas-contract.mjs --repo-root . --generated base-kit/templates/simple/main.tf` | Validates that generated BaseKit product-bundled L3 artifacts are PaaS-intended and do not start StackKit-owned L3 apps with direct Docker Compose. |
| Public release-note export | `node --test scripts/release/changelog.test.mjs && npm --prefix website run build` | Public website release-note JSON and built changelog section. |
| API/OpenAPI | `go test ./internal/api ./api/openapi/...` | API handler and OpenAPI embed behavior. |
| Post-apply verification | `stackkit verify --json` | Read-only host validation. |
| HTTP verification | `stackkit verify --http --json` | Generated service URL reachability. |
| Secret scan | `gitleaks detect --no-git --source . --config .gitleaks.toml` | Current-tree secret detection when `gitleaks` is available. |
| Vulnerability scan | `govulncheck ./...` | Reachable Go vulnerability detection. |
| Docker build | `docker build .` | Server image build verification. |
| BaseKit fresh Ubuntu VM | `mise run test:vm:local` | SK-S1 fresh target path: prepare, init, generate, apply, verify Hub and default services. |
| Release archive toolchain | `bash scripts/release/validate-release-archives.sh dist` | Public archives must contain `stackkit`, `stackkit-server`, `stackkit-mcp`, packaged `tofu`, root `cue.mod`, shared `base/`, kit definitions, and required `modules/`; the BaseKit and full CLI/catalog archives must run `init` and `generate` from extracted release content. |

## Local-First Rule

Debug release candidates locally before relying on remote CI. For docs-only changes, link checks are sufficient unless `website/`, install snippets, CLI examples, or test docs changed.

No test, workflow job, generated readiness wait, or manual release gate may wait longer than 15 minutes. Extending a timeout to hide slow or stuck behavior is not a fix; the gate must fail fast with diagnostics, split into smaller phases, or be redesigned until the blocking phase is visible and bounded. The timeout-budget policy is enforced by `node scripts/release/check-timeout-budget.mjs --repo-root .`.

Local gates MUST NOT rely on a host-installed OpenTofu binary. OpenTofu is a StackKit release-package component, not a developer or user prerequisite. Any test that proves install, prepare, plan, apply, or verify behavior must either use the packaged StackKit OpenTofu binary or start from a fresh target where `tofu` is absent before StackKit installs its packaged copy. The BaseKit fresh-VM gate explicitly fails if the target already has `tofu` on `PATH`.

Release archive gates MUST NOT rely on the repo checkout for CUE imports or module contracts. Build the snapshot with GoReleaser, extract the archive, copy only the released files into a fresh home directory, and prove that `stackkit init` plus `stackkit generate` creates non-empty identity runtime values such as `admin_email` and `tinyauth_users`.

## Local Demo Rollouts

Local demos MUST be real StackKit rollouts. The dev orchestrator may start two BaseKit paths:

- **Base Kit Installer**: runs the public one-line installer (`https://base.stackkit.cc`) with `STACKKIT_ADMIN_EMAIL`/`KOMBIFY_USER_EMAIL` supplied from `STACKKIT_DEMO_ADMIN_EMAIL`.
- **Base Kit CLI**: runs `stackkit init base-kit`, `stackkit generate`, and `stackkit apply` directly through the repo's CLI container.

The installer path exercises the CLI implicitly because the installer installs and invokes `stackkit prepare`, `stackkit init`, `stackkit generate`, and `stackkit apply`. Keep the direct CLI path as a separate gate so installer breakage and CLI breakage are distinguishable.

The orchestrator targets `STACKKIT_DEMO_DOCKER_HOST` (default `tcp://vm:2375`) and does not start a different VM as a side effect of pressing Start. For the persistent VM profile, set `STACKKIT_DEMO_DOCKER_HOST=tcp://vm-persistent:2375` and `STACKKIT_DEMO_VM_CONTAINER=stackkits-vm-persistent`.

Do not add hand-authored demo `docker-compose.yml` rollout paths to the orchestrator. The only local user-facing Hub link for BaseKit is `http://base.home.localhost`; local demo links MUST NOT contain ports, hosts-file instructions, manual DNS mapping, browser proxy setup, trust-store setup, or invented per-kit Hub hostnames such as `modern.home.localhost` or `ha.home.localhost`.

Do not add route shims to demos or tests. When the selected PaaS includes Traefik, the PaaS Traefik is the only valid StackKit routing path: Coolify's Traefik for Coolify, `dokploy-traefik` for Dokploy. `paas: komodo` is the current explicit exception and must prove the single StackKit-owned Traefik path. A second StackKit Traefik, Nginx bridge, host-side proxy, or mapped-port-only browser workaround invalidates local reachability evidence.

For code, CUE, config, deployment, or user-facing behavior changes:

```bash
go test ./...
cue vet ./base/... ./base-kit/... ./modern-homelab/... ./ha-kit/...
mise run test:cue-binding
```

## Release Candidate Preflight

Before every tag that should reach `kombifyio/stackKits`, run the SpeechKit-style public-surface gate from the private development repo:

```powershell
node --test scripts/release/changelog.test.mjs && npm --prefix website run build
```

The preflight verifies:

- focused Go release packages,
- kit CUE contracts,
- `CHANGELOG.md` release-note parsing,
- installer shell routes, `website/dist/changelog.json`, and the `Latest Release Notes` website section,
- sanitized allowlist export from `scripts/public/export-manifest.txt`,
- Go tests in the exported public tree,
- release and live-test workflow syntax through `actionlint`.
- release archive installability via `scripts/release/validate-release-archives.sh` in the public workflow.
- default StackKit-owned L3 PaaS-intent validation via `scripts/release/check-l3-paas-contract.mjs` and the `defaultL3PaaSDelivery` release evidence check.
- GitHub Artifact Attestations for public release files and the GHCR server image, plus CI verification before evidence marks attestation status as passed.
- SBOM generation plus `release-evidence.json` publication for Enterprise review.

Use `-SkipCue`, `-SkipGoTests`, `-SkipWebsite`, or `-SkipActionlint` only for narrow local debugging. A release-candidate receipt should use the full command.

When installer URLs or website routing change, verify the live one-liner endpoints after deployment. `https://install.stackkit.cc` and `https://base.stackkit.cc` are canonical BaseKit one-line installer paths and must return executable shell, not an HTML fallback page. `https://modern.stackkit.cc` and `https://ha.stackkit.cc` may return shell preview entrypoints, but they must warn that the kits are alpha/scaffolding. Raw GitHub URLs are acceptable fallback evidence, but they do not prove the public short endpoint.

## Canonical Scenarios

private StackKit scenario catalog defines the canonical scenario set for topology, domain mode, identity bootstrap, platform adapter selection, and application placement.

Every new module should map to at least one canonical scenario before it becomes part of the release default.

## Production-Style Tests

Production-like tests live under `../tests/production/` and are documented in private production-test runbook.

Important targets:

- `TestProductionReadinessLocalHomeLocalhost`
- `TestProductionReadinessKombifyMeSubdomains`
- `TestSimCloudSSORedirectConfigured`

When Docker Hub rate-limits anonymous pulls, seed the Ubuntu VM target with either `STACKKIT_FRESH_VM_DOCKER_CONFIG` or `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON`.

The fresh Ubuntu gate is no longer a pure liveness smoke. For BaseKit local
defaults it must also prove:

- fixed host ports are free before Docker resources are created,
- `base.home.localhost` answers anonymous first-setup requests with the warning `Diese Seite ist aktuell ungeschützt.` and the `Base Hub schützen` action,
- protected/default services other than the Base Hub do not answer anonymous requests with `2xx`,
- `reverse_proxy_backend` matches the actual traffic path; if it is `coolify` or `dokploy`, service probes must traverse that PaaS Traefik and no separate StackKit Traefik/Nginx/host proxy may satisfy the route; if it is `stackkit` for explicit Komodo, probes must traverse the single StackKit-owned Traefik,
- `stackkit-server` can read `deploy/.platform-apps-manifest.json`,
- the Photos/Immich on-demand setup drop can execute from the node-local API,
- expensive phases are logged so slow local runs show progress.

Explicit `public-unauthenticated` L3 services are allowed, but they are a separate access-policy case and must not be inferred from the BaseKit default path.

Known release blocker: the Coolify-managed application-layer contract is
tracked in Beads as `kombify-StackKits-85x`. A production-ready SK-S1 pass must
show StackKit-owned/default L3 apps as PaaS-managed with external app IDs/status
evidence; direct-compose starts for those product-bundled apps are invalid
release evidence. User-installed apps outside StackKit manifests are allowed
but state-unmanaged by StackKit.

For Coolify SK-S1 evidence, the VM must additionally show that Coolify is
API-ready and router-ready from the one-click bootstrap: root user/team exists,
the API is enabled, `.stackkit/platform.json` contains endpoint/token plus
project/environment/server/destination context, Coolify's Traefik/proxy is the
active route for generated service URLs, and StackKit-owned L3 apps have
Coolify service IDs/status. A healthy Coolify container with Vault/Photos
running as direct Compose containers, or routes served by a separate
StackKit-owned Traefik/Nginx shim, is a failed managed-app rollout.

For Dokploy evidence, apply the same rule with `dokploy-traefik`:
generated StackKit routes must attach to Dokploy's router. A separate
StackKit-owned Traefik is valid only for a future adapter whose accepted PaaS
contract explicitly has no integrated router.

For Komodo evidence, the VM must show a UI-free bootstrap: Komodo Core,
Periphery, and DB are healthy; the generated initial admin can log in;
registration is closed; `.stackkit/platform.json` contains endpoint,
`apiKey`, `apiSecret`, and server context; StackKit-owned/default L3 apps are
created as Komodo Stack resources with external IDs/status; and generated
routes are served through the declared StackKit-owned Traefik path.

## First BaseKit Live Test Sequence

Use this sequence for the first BaseKit live validation after the preflight passes:

1. Start from a clean release candidate in the private repo; only intended release changes should be present.
2. Run `private BaseKit live preflight` and keep the output as the local release-candidate evidence.
3. Ensure `STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON` is configured in GitHub Actions secrets, or provide `STACKKIT_FRESH_VM_DOCKER_CONFIG` locally.
4. Dispatch `.github/workflows/production-tests.yml` with `run_basekit_live=true`, `run_live_sim=false`, and `run_installer=false`.
5. Verify the `stackkit-SK-S1-homelab` artifact contains `status=success`, the Hub URL, browser URL, default service URLs, target metadata, and logs hint.
6. Only after SK-S1 passes, run the tag publish path described in private release runbook.

Customer-owned SvelteKit app rollouts are not StackKit release gates. StackKit validates the PaaS, routing baseline, generated customer-app handoff manifests, and status evidence; the selected PaaS/Admin surface owns customer-app deployment and lifecycle. Product-bundled L3 use cases remain StackKit-owned and PaaS-intended by default; user-installed apps outside that path are state-unmanaged.

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
