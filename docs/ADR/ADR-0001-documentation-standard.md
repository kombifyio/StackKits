# ADR-0001: Documentation Standard

- **Status:** Accepted
- **Date:** 2026-01-22

## Context

The StackKits repository contained mixed documentation styles, outdated files, and a lack of clear separation between "current state" and "target vision." This led to confusion about what features were actually implemented versus planned.

## Decision

We will adopt a strict documentation framework to ensure clarity and maintainability.

1.  **Core Docs live in `docs/`:**
    - `README.md`: Concise repo entry point.
    - `STATUS.md`: Honest, verifiable current state.
    - `ROADMAP.md`: Beads-generated roadmap read-view.
    - `docs/ARCHITECTURE.md`: Current implementation overview.
    - `docs/CLI.md`: CLI reference.
    - `docs/API.md`: Human API summary; OpenAPI is the contract source.
    - `docs/CONFIGURATION.md`: Configuration reference.
    - `docs/TESTING.md`: Test gate matrix.

2.  **No in-repo archive folder:**
    - Prefer git history (tags/branches) for retrieval.
    - If long-term storage is needed, use a separate external archive repo.

3.  **ADR Process:**
    - Architectural decisions will be recorded in `ADR/` using this template.
    - Format: `ADR-XXXX-title.md`.

## Consequences

- **Benefit:** instant clarity for new contributors on what is real vs. planned.
- **Benefit:** Cleaning up `docs/` makes the repo more professional.
- **Maintenance:** Requires discipline to update Beads, `STATUS.md`, and the Tier-3 docs as features are completed.
