# Contributing to kombify StackKits

Thank you for your interest in contributing to kombify StackKits!

We welcome contributions of all kinds: bug reports, feature requests, new stacks, schema improvements, and documentation.

---

## Quick Start

1. **Fork the repository** on GitHub
2. **Clone your fork** locally
3. **Create a branch** for your changes: `git checkout -b feature/my-feature`
4. **Make your changes** and validate them
5. **Commit and push** your changes
6. **Open a Pull Request**

---

## Development Setup

**Prerequisites:** Go 1.24+, CUE CLI, golangci-lint

```bash
# Install dependencies
go mod download

# Build
go build -o build/stackkit ./cmd/stackkit

# Run tests
go test -v -race -short ./pkg/... ./internal/...

# Lint
golangci-lint run ./...

# Validate CUE schemas
cue vet ./schemas/...
```

---

## Code Conventions

### Go Code

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Format: `go fmt ./...`
- Vet: `go vet ./...`
- Error wrapping: `fmt.Errorf("context: %w", err)`

### CUE Schemas

- **Always validate** before committing: `cue vet ./schemas/...`
- **Backwards compatibility**: Never remove fields or change types
- New fields must be optional (`field?: type`) or have defaults (`field: type | *"default"`)
- Format: `cue fmt ./schemas/...`

### Testing

- Unit tests alongside source files (`_test.go`)
- Validate schemas against existing stacks before submitting

---

## Adding New Stacks

1. Define schema in `schemas/services/` (if new service type)
2. Validate: `cue vet ./schemas/...`
3. Create stack directory in `stacks/`
4. Create stack configuration implementing the schema
5. Validate stack: `cue vet ./schemas/... ./stacks/<stack-name>/`
6. Add README.md to stack directory

---

## Commit Messages

We follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>
```

**Types:** `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

---

## Pull Request Process

1. Run `cue vet ./schemas/...` (must pass)
2. Run `go test -v -race ./...`
3. Run `golangci-lint run`
4. Provide a clear description of your changes
5. Link related issues

---

## Brand Guidelines

- Always use "kombify" with a lowercase k
- Tool names: "kombify-TechStack", "kombify Sim", "kombify StackKits", "kombify Sphere"

---

## License

By contributing, you agree that your contributions will be licensed under the project's dual license (Apache 2.0 / GPLv3). See [LICENSE](LICENSE) for details.
