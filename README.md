# StackKits

StackKits is an open-source infrastructure blueprint system for self-hosted
homelab and small-server deployments. The `stackkit` CLI turns a declarative
`stack-spec.yaml` into validated Docker/OpenTofu deployment output.

## Install

Basement Kit — local / homelab (stable):

```sh
curl -sSL https://base.stackkit.cc | sh
```

Cloud Kit — cloud VM / BYO-VPS:

```sh
curl -sSL https://cloud.stackkit.cc | sh
```

Modern Homelab — combined Home + Cloud topology (Preview):

```sh
curl -sSL https://install.stackkit.cc | sh
stackkit init modern-homelab --non-interactive --name my-modern-homelab
```

The Modern archive and catalog entry prove self-contained native-v2 authoring
and validation. They do not claim that every federation runtime owner is
graduated.

For the CLI plus the complete public three-kit catalog, use the same
`install.stackkit.cc` installer and select the desired kit with `stackkit init`.

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

See [LICENSE](LICENSE).
