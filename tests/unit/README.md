# Unit Tests

This directory contains unit tests for the StackKits core packages.

## Structure

```
unit/
├── config_test.go    # Tests for internal/config
├── docker_test.go    # Tests for internal/docker
└── README.md         # This file
```

## Running Tests

```bash
# From project root
make test

# Run only unit tests
go test ./tests/unit/...

# With coverage
go test -cover ./tests/unit/...

# Verbose output
go test -v ./tests/unit/...
```

## Writing New Tests

1. Create a test file named `{package}_test.go`
2. Use `testing` and `testify` packages
3. Follow table-driven test patterns
4. Include both happy path and error cases
5. Test edge cases and boundary conditions

## Coverage Goals

Target: **80% coverage** for all internal packages.

Current packages to test:
- [ ] `internal/config` - Configuration loading
- [ ] `internal/cue` - CUE validation
- [ ] `internal/docker` - Docker client
- [ ] `internal/ssh` - SSH client
- [ ] `internal/template` - Template rendering
- [ ] `internal/tofu` - OpenTofu executor
- [ ] `internal/terramate` - Terramate orchestration
- [ ] `internal/iac` - IaC executor
- [ ] `internal/validation` - Validation helpers
