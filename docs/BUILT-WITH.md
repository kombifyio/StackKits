# How StackKits is built

StackKits is an open homelab standard: CUE contracts, a standalone `stackkit`
CLI, and a Docker Compose lifecycle that runs on a Linux host you control.
This page records how the project is developed, including the AI-assisted
engineering workflow behind it, so that the provenance of the code is as
inspectable as the code itself.

## What the product depends on

- **No AI at runtime.** The shipped binaries (`stackkit`, `stackkit-server`,
  `stackkit-mcp`) call no language-model API. Standard Mode is account-free
  and works without any OpenAI, kombify, or other hosted service.
- **Agent-native, provider-neutral.** Every release ships `llms.txt`, OpenAPI,
  JSON schemas, prompt Markdown, and the `stackkit-mcp` connector so that any
  coding agent or MCP client can drive the same governed lifecycle. Which
  model a user connects is the user's choice.
- **Upstream applications keep their features.** StackKits integrates curated
  open-source software (Immich, Cloudreve, Vaultwarden, Jellyfin, Home
  Assistant, PocketID, TinyAuth, step-ca, Traefik, and more) and does not
  reimplement their functionality.

## How the code is written

The maintainers develop StackKits with OpenAI Codex as the primary coding
agent, under a documented delegation policy:

- substantial implementation slices run on **GPT-6 Astra**;
- bounded, well-specified delegations run on GPT-5.6 Sol;
- planning, review, and integration stay with the maintainers.

Every change lands through a pull request on the private development
repository, passes the affected test gate (`mise run check`: Go, CUE, website
and public-boundary checks), and is reviewed and merged by a maintainer before
it is exported to this public mirror through an explicit allowlist.

Scale of the agent-authored contribution, measured on 2026-09-14 from the
development repository's merged pull requests:

| Measure | Value |
| --- | --- |
| Merged pull requests since the repository started (January 2026) | 997 |
| Merged from `codex/*` agent branches | 732 |
| Agent-branch merges in July / August / September 2026 (to date) | 364 / 169 / 189 |
| Agent-branch merges since 2026-09-03 | 163 |

Representative agent-implemented slices from the current release line
(each merged after maintainer review and the affected gate):

- the Files (Cloudreve) owner bootstrap, backup, staged restore and native
  activation path, including the byte-identical recovery proof;
- Kopia application-volume snapshots, Compose environment handling, and
  container recreate labels for the backup engine;
- the internal PKI to Traefik binding and certificate duration policy;
- device enrollment and the LAN client-trust handoff;
- the release publication chain (attested archives, release index, public
  mirror export) and its live installer smoke.

## What we do not claim

- No percentage of "AI-written code": the counts above are pull-request
  counts, not line attribution.
- No claim that every historical change used a specific model; the delegation
  policy above describes the current workflow.
- No runtime dependency on any model provider, now or as a roadmap item.

## Evidence discipline

Runtime claims follow the same rule as code provenance: a kit or use case is
called `supported`, `preview`, or `alpha` only with a cited run. Missing
evidence is `pending`, never `passed`. See `README.md` for the current kit
statuses and `docs/RELEASE.md` for the release evidence contract.
