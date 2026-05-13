# StackKits

StackKits is an open-source infrastructure blueprint system for self-hosted
homelab and small-server deployments. The `stackkit` CLI turns a declarative
`stack-spec.yaml` into validated Docker/OpenTofu deployment output.

## Install

```sh
curl -sSL stackkit.cc/base | sh
```

For CLI-only installation:

```sh
curl -sSL stackkit.cc/install | sh
```

## Documentation

- [CLI reference](docs/CLI.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Concepts](docs/CONCEPTS.md)
- [Stack spec reference](docs/stack-spec-reference.md)
- [Testing](docs/TESTING.md)

## Source Of Truth

CUE files are the technical source of truth for schemas, defaults,
constraints, module contracts, and kit composition. Generated OpenTofu,
Compose, tfvars, state, and rollout snapshots are build output.

## License

See [LICENSE](LICENSE).
