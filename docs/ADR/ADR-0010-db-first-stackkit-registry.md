# ADR-0010: DB-First StackKit Registry

Status: Accepted

StackKits keeps CUE as the technical contract source of truth while registry
systems may mirror catalog, version, compatibility, and deployment state for
operator workflows. Public releases contain the CUE contracts and CLI behavior;
managed registry implementation details are outside the OSS release surface.
