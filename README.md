# StackKits

StackKits is an open-source infrastructure blueprint system for self-hosted
homelab and small-server deployments. The `stackkit` CLI turns a declarative
`stack-spec.yaml` into validated Docker/OpenTofu deployment output.

## Install

```sh
curl -sSL https://base.stackkit.cc | sh
```

For CLI plus the public kit catalog:

```sh
curl -sSL https://install.stackkit.cc | sh
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

## License

See [LICENSE](LICENSE).
