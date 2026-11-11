# StackKit CLI (`stackkit`)

This folder contains the source for the `stackkit` CLI.

## Documentation

- CLI reference: ../../docs/CLI.md

## Build

```bash
# from repo root
make build
./build/stackkit version
```

## Quick start

```bash
# from an empty working directory
stackkit init basement-kit --catalog-defaults --owner-source=local --owner-email you@example.com
stackkit prepare
stackkit generate
stackkit plan
stackkit apply
```
