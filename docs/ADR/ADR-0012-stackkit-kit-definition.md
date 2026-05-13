# ADR-0012: StackKit Kit Definition

Status: Accepted

StackKit definitions are version-controlled CUE/YAML contracts. Kit metadata,
module composition, defaults, and constraints are reviewed in source and
validated before release. Generated rollout artifacts are outputs and should not
be patched by hand.
