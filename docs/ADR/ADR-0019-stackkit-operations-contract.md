# ADR-0019: StackKit Operations Contract with Optional Pulumi Executor

**Status:** Accepted
**Date:** 2026-05-09

## Context

StackKits must deliver a one-click homelab default without requiring users to
choose or understand multiple infrastructure engines. OpenTofu already owns the
durable deployment resources, Terramate orchestrates multi-stack OpenTofu
workflows, and StackKits already includes Go runtime code for API bootstrap
tasks such as PocketID owner setup and TinyAuth integration.

Pulumi is useful for some API-managed resources because it can provide preview,
refresh, drift detection, and stateful resource lifecycle. It is also stateful:
adding it to the default StackKit path would create a second state owner beside
OpenTofu. That would make the open StackKit standard harder to reason about and
would duplicate identity bootstrap logic already implemented in Go.

## Decision

StackKits owns the operations contract. TechStack may orchestrate operation
execution, approvals, tenant context, and audit, but it must consume signed
StackKit operation bundles instead of inventing tool-specific lifecycle logic.

The contract is expressed in `base/operations.cue` and mirrored by
`internal/operations` Go validation:

- `phase`: `post_apply`, `reconcile`, `upgrade`, or `pre_destroy`
- `executor`: `go` or `pulumi`
- `stateful`: whether the operation owns persistent state
- `owner`: `opentofu`, `stackkit-runtime`, `pulumi`, or `external`
- `inputs`, `outputs`, `secret_refs`, `approval_policy`, and optional
  `state_scope`

Pulumi is allowed only as an optional stateful operations executor. A Pulumi
operation must declare `owner: pulumi`, must have `stateful: true`, and must set
`state_scope`. The Pulumi command provider is not allowed in StackKit operation
specs because it would hide shell scripts behind Pulumi state rather than
creating a typed API resource contract.

## Boundaries

- OpenTofu remains the owner for durable homelab infrastructure: hosts, Docker
  networks, containers, volumes, routing, and local DNS/service resources.
- StackKit Go runtime remains the owner for local bootstrap operations such as
  PocketID owner/user/group setup, TinyAuth OIDC client setup, break-glass
  bundle generation, access summaries, health checks, and one-shot verification.
- Pulumi is reserved for external API domains where preview/refresh/drift are
  materially valuable, such as Cloudflare Access, Auth0 clients, or Doppler
  project configuration.
- TechStack must expose only an allowlisted `pulumi_operation` command type to
  agents. Raw `pulumi`, `pulumi_command`, shell, or generic execute commands
  remain outside the agent command contract.

## Consequences

- The Base Kit one-click path remains OpenTofu plus StackKit Go operations, with
  no Pulumi dependency for the default installer.
- Optional Pulumi proof-of-concepts should target external API resources rather
  than PocketID owner bootstrap.
- Every managed object must appear with one owner in the ownership manifest;
  duplicates across OpenTofu, Pulumi, and StackKit runtime are contract errors.
- TechStack can combine OpenTofu plans and Pulumi previews in the UI later, but
  those are separate approval sections backed by separate state owners.

## Verification

- `go test ./internal/operations`
- `cue vet ./base/...`
- TechStack targeted test for the `pulumi_operation` allowlist boundary.
