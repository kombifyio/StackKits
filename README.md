# StackKits

StackKits is an open-source infrastructure blueprint system for self-hosted
homelab and small-server deployments. **Every deployment is executed and
tracked by OpenTofu; managed deployments are orchestrated by Terramate**
(ADR-0045). Standalone Docker Compose remains the complete primary application
experience: containers run as Compose workloads inside that OpenTofu-tracked
stack. Standard Mode runs independently, without a Kombify account, Techstack,
Coolify, or Komodo. Basement Kit defaults to the `opentofu` execution target;
Cloud Kit and Modern Homelab currently default to `compose`, with
`opentofu`/`terramate` available, switching after each kit's own end-to-end
run.

The CLI is an optional user-facing interface to the same governed lifecycle,
not a separate full edition. Komodo and Coolify are explicit opt-in
integrations. Development proceeds from complete standalone Compose to Komodo,
then Coolify; Dokploy remains draft. Existing explicit platform selections are
preserved. See [the standalone decision](docs/ADR/ADR-0042-standalone-default-and-optional-platforms.md)
for the application-default contract and the separate runtime-evidence
requirements.

## Install

Basement Kit — local / homelab:

```sh
curl -sSL https://base.stackkit.cc | sh
```

Cloud Kit — cloud VM / BYO-VPS:

```sh
curl -sSL https://cloud.stackkit.cc | sh
```

Modern Homelab — combined Home + Cloud topology (alpha definition archive, not an install target yet):

```sh
curl -sSL https://install.stackkit.cc | sh
stackkit init modern-homelab --non-interactive --name my-modern-homelab
```

The Modern archive and catalog entry prove self-contained native-v2 authoring
and validation. They do not claim that every federation runtime owner is
graduated.

For the CLI plus the public kit catalog (two install paths plus the Modern
alpha definition), use the same `install.stackkit.cc` installer and select the
desired kit with `stackkit init`.

| Kit | Target | Status |
| --- | --- | --- |
| Basement Kit | an existing Linux host at home | supported one-command path |
| Cloud Kit | an existing VPS with your own domain | preview |
| Modern Homelab | Home + Cloud, joined by an explicit federation bridge | alpha definition archive |

A published release is source and distribution evidence, not runtime
acceptance. Each release ships `release-evidence.json`; kit and use-case
runtime evidence is summarized on <https://stackkit.cc> and a status only
widens when a cited run exists.

## Website, docs and community

- Product site and installers: <https://stackkit.cc>
- Documentation: <https://docs.kombify.io/stackkits>
- The StackKit Open Spec (architecture snapshot, lifecycle verbs, placement
  taxonomy, verification evidence): [docs/OPEN-SPEC.md](docs/OPEN-SPEC.md)
- Support: see [SUPPORT.md](SUPPORT.md). Questions and ideas go to GitHub
  Discussions on this repository, bugs to the issue templates, vulnerabilities
  to GitHub Security Advisories (see `SECURITY.md`). This repository is a
  generated release mirror; contributions are ported upstream by maintainers
  (see `CONTRIBUTING.md`)
- How the project is built, including the AI-assisted development
  provenance: [docs/BUILT-WITH.md](docs/BUILT-WITH.md)

## Works with agents

StackKits is agent-native without depending on any AI service. The CLI embeds
the `stackkit-mcp` connector and the prompt Markdown; `llms.txt`, OpenAPI and
the JSON schemas are published on <https://stackkit.cc> for every release:

```sh
stackkit agent mcp-config --client codex   # or claude / generic
stackkit agent prompt --list
```

## Documentation

- [CLI reference](docs/CLI.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Concepts](docs/CONCEPTS.md)
- [Stack spec reference](docs/stack-spec-reference.md)

## Source Of Truth

CUE files are the technical source of truth for schemas, defaults,
constraints, module contracts, and kit composition. Generated OpenTofu,
Compose, tfvars, state, and rollout snapshots are build output.

The [Architecture v2 contract proof](architecture/v2/fixtures/contract-fixtures.manifest.json)
reproducibly binds a two-node Basement topology, named runtime daemons,
provider/consumer interfaces, runtime networks, and an approved direct-socket
exception through the compiler and renderer contract. It uses a separate
contract-only catalog and is explicitly ineligible for product graduation.
Validate the committed hashes and catalog boundary with
`node scripts/release/validate-architecture-contract-fixture.mjs --repo-root .`.

## License

Apache-2.0 OR GPL-3.0-or-later, at your option. See [LICENSING.md](LICENSING.md)
and the complete [Apache-2.0](LICENSE-APACHE) and
[GPL-3.0](LICENSE-GPL-3.0-or-later) texts.
The [WebMCP package](webmcp/LICENSE) is Apache-2.0-only.
