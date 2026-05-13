# StackKit Canonical Test Scenarios

> Status: Planning baseline
> Scope: Future unit, integration, production, and live E2E tests for StackKit rollout behavior.
> Based on: [StackKit Golden Rules](STACKKIT_GOLDEN_RULES.md), [StackKit Development Decision Guide](STACKKIT_DEVELOPMENT_DECISION_GUIDE.md), and [ADR-0006 Service URL Matrix](ADR/ADR-0006-service-url-matrix.md).

This document defines the small canonical scenario set used to test StackKits across topology, domain mode, identity bootstrap, platform adapter selection, and application module placement.

The goal is not to test every possible permutation directly. The goal is to keep 3 to 5 high-value scenarios that cover the decision surface well enough that new modules can be assigned to one or more scenarios and verified consistently.

## 1. Canonical Custom Domain

`kombify.pro` is the canonical custom-domain test zone for StackKits.

Tests MUST NOT use the bare apex `kombify.pro` as an application target. Live tests MUST derive an isolated per-run subzone:

```text
sk-<run-id>.kombify.pro
*.sk-<run-id>.kombify.pro
```

Examples:

```text
base.sk-20260509-1430.kombify.pro
id.sk-20260509-1430.kombify.pro
photos.sk-20260509-1430.kombify.pro
```

This keeps the test surface isolated, supports parallel runs, and avoids overwriting future product, demo, or documentation records on `kombify.pro`.

## 2. DNS And Registrar Contract

StackKits should distinguish domain ownership from DNS automation.

| Concern | Meaning | StackKits behavior |
| --- | --- | --- |
| Registrar | Where the domain was bought. | Only matters if nameservers must be delegated. |
| DNS provider | Where records are managed. | This is what StackKits can automate. |
| Zone access | API token or manual record editing. | Determines whether DNS can be fully automated. |
| Server public IP | Target address for public records. | Required for custom-domain public access. |

For automated custom-domain tests, `kombify.pro` SHOULD be managed through Cloudflare DNS. The test runner should provide a scoped Cloudflare token with permission to edit only the `kombify.pro` zone.

Recommended environment contract:

```bash
STACKKIT_DNS_PROVIDER=cloudflare
STACKKIT_DNS_ZONE=kombify.pro
STACKKIT_DNS_TOKEN=<scoped-cloudflare-zone-token>
STACKKIT_CUSTOM_DOMAIN_BASE=kombify.pro
```

For a custom-domain rollout, StackKits must support two DNS paths.

### Automated DNS Path

Use this when DNS provider credentials are present.

Required behavior:

1. Detect or accept the target server public IP.
2. Create or update the per-run base record.
3. Create or update the per-run wildcard record.
4. Configure DNS-01 or provider-compatible ACME flow when needed.
5. Clean up test records after the test when the test is marked ephemeral.

Required records for a single-primary-node public rollout:

```text
A     sk-<run-id>.kombify.pro      <server-ipv4>
A     *.sk-<run-id>.kombify.pro    <server-ipv4>
AAAA  sk-<run-id>.kombify.pro      <server-ipv6>   optional
AAAA  *.sk-<run-id>.kombify.pro    <server-ipv6>   optional
```

### Manual DNS Path

Use this when the user owns a domain but no supported DNS API token is available.

Required behavior:

1. StackKits must print the exact records the user needs to create.
2. StackKits must include the detected target IP.
3. StackKits must explain whether a wildcard record is required.
4. StackKits must wait, poll, or provide a `stackkit doctor dns` command before proceeding to public TLS.
5. Non-interactive full public rollout must fail if DNS is not already correct and cannot be automated.

Minimum manual instruction shape:

```text
Create these DNS records at your DNS provider:

Type: A
Name: sk-<run-id>
Value: <server-ipv4>

Type: A
Name: *.sk-<run-id>
Value: <server-ipv4>

Then run:
stackkit doctor dns --domain sk-<run-id>.kombify.pro
```

Registrar-specific setup is documentation, not core deployment logic. If a registrar does not host DNS itself, the user must either delegate nameservers to Cloudflare or create equivalent records at their active DNS provider.

## 3. First-User And Email Contract

StackKits must distinguish a technical admin login from a real owner identity.

| Situation | Expected behavior |
| --- | --- |
| Local-only rollout, no email | Generate a technical admin email such as `admin@stack.home`. Do not invent a real owner person. |
| kombify Cloud/TechStack context, no CLI email | Use `KOMBIFY_USER_EMAIL` as the owner/admin email when present. |
| Custom public domain with explicit email | Preserve the provided email exactly. |
| Custom public domain, full config, no email, interactive | Prompt for owner/admin email. |
| Custom public domain, full config, no email, non-interactive | Fail validation with a clear missing-email error. |
| Owner bootstrap enabled, no owner email | Fail validation in non-interactive mode; prompt in interactive mode. |
| Owner bootstrap enabled, no owner username | Fail validation in non-interactive mode; prompt in interactive mode. |

Synthetic emails are acceptable for local technical accounts. They are not acceptable as a substitute for a real owner identity in a full public or managed configuration.

## 4. Scenario Matrix

These seven scenarios are the canonical planning set. SK-S2A and SK-S3A extend the public BaseKit paths with a user SvelteKit app rollout.

| ID | Name | Kit | Topology | Domain mode | Email mode | Primary coverage |
| --- | --- | --- | --- | --- | --- | --- |
| SK-S1 | Local Ready | Base Kit | One local node | Local DNS `stack.home` | No email | Local default, local DNS, synthetic admin email |
| SK-S2 | Cloud OneClick kombify.me | Base Kit | One cloud node | Managed `kombify.me` | Cloud user email | kombify.me registry, managed subdomains, Dokploy default |
| SK-S2A | Cloud OneClick kombify.me + SvelteKit App | Base Kit | One cloud node | Managed `kombify.me` | Cloud user email | Installer app handoff, Dokploy app adapter, `<prefix>-web.kombify.me` |
| SK-S3 | Cloud Custom Domain | Base Kit | One cloud node | `sk-<run>.kombify.pro` | Explicit email | Cloudflare DNS, public TLS, Coolify/custom-domain path |
| SK-S3A | Cloud Custom Domain + SvelteKit App | Base Kit | One cloud node | `sk-<run>.kombify.pro` | Explicit email | Installer app handoff, Coolify app adapter, `web.<domain>` |
| SK-S4 | Modern Hybrid | Modern Homelab | One cloud node + one local node | `kombify.pro` public zone + local zone | Explicit owner email | Public/local split, placement, backups |
| SK-S5 | Missing Mail Contract | Base Kit or Modern Homelab | Cloud/public | Custom domain or kombify.me | No email source | Negative validation for full config |

## 5. Scenario Definitions

### SK-S1: Local Ready

Purpose:

Validate the local-only Base Kit path with the least user intent.

Input shape:

```yaml
stackkit: base-kit
mode: simple
context: local
domain: stack.home
compute:
  tier: standard
```

Email input:

- `email` omitted
- `adminEmail` omitted
- owner bootstrap omitted unless explicitly tested

Expected resolution:

- domain: `stack.home`
- service hosts: `base.stack.home`, `id.stack.home`, `auth.stack.home`, `kuma.stack.home`, `whoami.stack.home`, `vault.stack.home`, `photos.stack.home`
- admin email: `admin@stack.home`
- public DNS: none
- public TLS: none
- local DNS: Kombify Point
- local TLS: StackKit-managed Step-CA
- application exposure: LAN-only behind access policy

Minimum verification:

- generated spec contains `domain: stack.home`
- generated tfvars contain non-empty email-shaped `admin_email`
- no `.localhost` admin email
- access summary uses HTTPS local service URLs
- TinyAuth/PocketID provider config is internally consistent
- default local services are reachable through local Traefik path

Existing anchors:

- `TestProductionReadinessLocalHomeLocalhost`
- `verifyLocalHomeAccessSummary`
- `TestNormalizeAdminEmail`

### SK-S2: Cloud OneClick kombify.me

Purpose:

Validate the managed subdomain path for a cloud server where kombify provides routing and the authenticated cloud user supplies the email identity.

Input shape:

```yaml
stackkit: base-kit
mode: simple
context: cloud
domain: kombify.me
subdomainPrefix: <generated-or-provided-prefix>
compute:
  tier: standard
```

Environment:

```bash
KOMBIFY_CONTEXT=cloud
KOMBIFY_USER_EMAIL=tester@kombify.pro
KOMBIFY_API_KEY=<kombify-me-api-key>
```

Expected resolution:

- domain: `kombify.me`
- service hosts: `<prefix>-base.kombify.me`, `<prefix>-id.kombify.me`, `<prefix>-auth.kombify.me`
- admin/owner email: `tester@kombify.pro`
- PaaS: Dokploy default unless explicitly overridden
- TLS: managed by kombify edge
- DNS: kombify.me registry, flat service naming

Minimum verification:

- `KOMBIFY_USER_EMAIL` is preferred over synthetic admin email
- access summary uses `https://<prefix>-service.kombify.me`
- kombify.me registry contains expected service entries
- registry entries are exposed and not failed
- auth and identity URLs match generated service URLs

Existing anchors:

- `TestProductionReadinessKombifyMeSubdomains`
- `verifyKombifyMeSubdomainAccessSummary`
- `verifyKombifyMeRegistrySubdomains`

### SK-S2A: Cloud OneClick kombify.me + SvelteKit App

Purpose:

Validate that the public kombify.me installer path can add and deploy a user SvelteKit app through the platform-app handoff.

Additional input:

```yaml
apps:
  web:
    kind: sveltekit
    image: ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0
    port: 3000
    route:
      auth: public
    health:
      path: /health
```

Expected resolution:

- PaaS: Dokploy
- app host: `<prefix>-web.kombify.me`
- kombify.me registry includes the `web` service
- `stackkit status --json` includes `platformApps[].name == "web"`
- public `/health` probe succeeds for the smoke app

### SK-S3: Cloud Custom Domain

Purpose:

Validate the own-domain public cloud path using `kombify.pro`.

Input shape:

```yaml
stackkit: base-kit
mode: simple
context: cloud
domain: sk-<run-id>.kombify.pro
email: owner@kombify.pro
adminEmail: owner@kombify.pro
tls:
  provider: cloudflare
  challenge: dns
compute:
  tier: standard
paas: coolify
```

Environment:

```bash
STACKKIT_DNS_PROVIDER=cloudflare
STACKKIT_DNS_ZONE=kombify.pro
STACKKIT_DNS_TOKEN=<scoped-cloudflare-zone-token>
STACKKIT_CUSTOM_DOMAIN_BASE=kombify.pro
```

Expected resolution:

- domain: `sk-<run-id>.kombify.pro`
- service hosts: `base.sk-<run-id>.kombify.pro`, `id.sk-<run-id>.kombify.pro`
- admin/owner email: `owner@kombify.pro`
- PaaS: Coolify for custom-domain cloud path, unless the test intentionally validates Dokploy custom-domain behavior
- TLS: ACME DNS-01 through Cloudflare
- DNS: wildcard/base records under the per-run subzone

Minimum verification:

- DNS record creation or manual DNS failure is explicit
- wildcard record points to target public IP
- ACME challenge path is configured
- public HTTPS probes succeed or fail with actionable DNS/TLS diagnostics
- generated access summary uses `https://service.sk-<run-id>.kombify.pro`
- no synthetic email is used when explicit email is present

Implementation notes:

- This scenario is the canonical place to test registrar/DNS guidance.
- The first implementation can be a dry-run resolver/generator test.
- Live DNS mutation should be gated by Cloudflare token env vars.
- Test cleanup should delete only records under the run-specific subzone.

### SK-S3A: Cloud Custom Domain + SvelteKit App

Purpose:

Validate that the custom-domain installer path can add and deploy a user SvelteKit app through Coolify.

Additional input:

```yaml
apps:
  web:
    kind: sveltekit
    image: ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0
    route:
      auth: public
    health:
      path: /health
```

Expected resolution:

- PaaS: Coolify
- app host: `web.sk-<run-id>.kombify.pro`
- `stackkit status --json` includes `platformApps[].name == "web"`
- public TLS and `/health` probes succeed for the smoke app

### SK-S4: Modern Hybrid

Purpose:

Validate a hybrid homelab with both remote flexibility and local-first security.

Input shape:

```yaml
stackkit: modern-homelab
mode: advanced
context: hybrid
domain: sk-<run-id>.kombify.pro
email: owner@kombify.pro
owner:
  source: local
  email: owner@kombify.pro
  username: owner
nodes:
  - name: cloud-main
    role: main
    context: cloud
  - name: local-main
    role: worker
    context: local
```

Expected resolution:

- cloud node owns public edge and remote entrypoints
- local node owns LAN-only and data-sensitive services where appropriate
- identity remains coherent across public and local services
- backups use a distinct failure domain when possible
- service placement is explainable
- local-only modules do not become public through the cloud node by accident

Minimum verification:

- placement report assigns public services to cloud side
- local-only services stay LAN-only or VPN-only
- backup target crosses local/cloud boundary when configured
- owner identity is explicit and not synthetic
- TechStack/Advanced Mode operations are declared but can be gated if not implemented

Implementation notes:

- This scenario can start as resolver-only and placement-only tests before live multi-node automation exists.
- Module authors should use this scenario for Smart Home, Files, Photos, Backup, and any module with data-locality rules.

### SK-S5: Missing Mail Contract

Purpose:

Validate that full public or managed configurations do not silently invent a real owner identity.

Input shape:

```yaml
stackkit: base-kit
mode: simple
context: cloud
domain: sk-<run-id>.kombify.pro
tls:
  provider: cloudflare
compute:
  tier: standard
```

Environment:

```bash
# Intentionally absent:
# KOMBIFY_USER_EMAIL
# STACKKIT_ADMIN_EMAIL
# owner.email
# adminEmail
# email
```

Expected behavior:

- interactive mode: prompt for owner/admin email before full configuration
- non-interactive mode: fail validation before generate/apply
- error explains which email input is required and why
- no generated public deployment should proceed with `admin@sk-<run-id>.kombify.pro` as a real owner identity

Minimum verification:

- non-interactive init/generate returns non-zero
- error message names the missing owner/admin email
- local-only synthetic admin email behavior remains unaffected

Implementation notes:

- This is a negative test and should be fast.
- It belongs in resolver/CLI tests first, then production readiness once the full config path is implemented.

## 6. Module Assignment Rules

Every new use-case module should declare at least one canonical scenario.

| Module class | Primary scenario | Additional scenario | What to prove |
| --- | --- | --- | --- |
| Identity, login gateway | SK-S1 | SK-S2, SK-S3 | Auth URLs, owner/bootstrap behavior, forward-auth |
| PaaS adapter | SK-S2 | SK-S3, SK-S4 | Adapter selection, app registration, routing |
| Photos | SK-S1 | SK-S4 | Storage, backup, local data posture |
| Files | SK-S4 | SK-S1 | Local data, sharing policy, backup |
| Smart Home | SK-S4 | SK-S1 | LAN/device access, never-public default |
| Password vault/secrets | SK-S1 | SK-S2, SK-S3, SK-S4 | Owner, auth, backup, recovery |
| Marketing website/public app | SK-S3 | SK-S4 | Public route, TLS, unauthenticated route only when explicit |
| Dev platform | SK-S3 | SK-S4 | Webhooks, domain, auth, remote access |
| Backup add-on | SK-S4 | SK-S1 | Failure-domain separation, restore contract |
| Monitoring/OTel | SK-S1 | SK-S4 | Local health, advanced telemetry |

Default modules must pass their assigned scenario before promotion to default status.

## 7. Test Layer Mapping

Each scenario should be implemented gradually across test layers.

| Layer | Purpose | Example checks |
| --- | --- | --- |
| Unit | Pure resolver/model behavior | email fallback, domain classification, PaaS selection |
| CUE | Contract validation | unsupported context rejected, required fields enforced |
| Golden generation | Output stability | access summary, tfvars, routes, platform adapter |
| Integration | CLI flow without live DNS | init/generate/plan with fake inputs |
| Production local | Fresh target | local VM, local DNS, reachable services, Homelab artifact |
| Production cloud | Live or simulated cloud target | kombify.me, custom DNS, public TLS, dashboard link artifact |
| Negative | Fail-fast safety | missing mail, invalid DNS, unsupported adapter |

Successful production scenarios write `artifacts/scenarios/<scenario-id>/homelab.json`
unless a scenario-specific output path is configured. The artifact contract is
`scenarioId`, `runId`, `status`, `hubUrl`, `browserUrl`, `services`, `target`,
and `logsHint`. Public scenarios use `browserUrl == hubUrl`; the local VM keeps
`hubUrl` as `https://base.stack.home` and exposes the mapped HTTPS browser URL
separately.

Initial implementation order:

1. SK-S1: implemented as the local fresh-VM production gate; keep extending it.
2. SK-S2: implemented as the kombify.me installer gate with email source assertions.
3. SK-S2A/SK-S3A: defined as gated-live SvelteKit app rollout scenarios; wire them into production dispatch after the smoke image is published.
4. SK-S5: implemented as fast negative CLI/resolver tests.
5. SK-S3: add more dry-run DNS/custom-domain tests, then harden the live Cloudflare-gated test.
6. SK-S4: add resolver/placement tests, then live multi-node tests later.

## 8. Open Implementation Tasks

These are planning items for future sessions.

1. Add deeper resolver tests for PaaS selection across local, kombify.me, and `kombify.pro`.
2. Add `stackkit doctor dns` planning or implementation for custom-domain manual DNS.
3. Harden Cloudflare-gated DNS dry-run/live tests for `sk-<run-id>.kombify.pro`.
4. Add a module metadata field that maps each module to canonical scenarios.
5. Add CI labels or build tags so live custom-domain tests run only when DNS credentials and cloud targets are present.
6. Add hybrid placement tests for Modern Homelab before requiring live two-node deployment.
7. Update production test docs when SK-S4 moves from planning to live multi-node implementation.
