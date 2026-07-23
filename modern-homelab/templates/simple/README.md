# Modern Homelab simple template

> **Status: placeholder / contract-only.** This directory intentionally contains
> no OpenTofu or Compose deployment template. Modern-specific generation and
> execution have not been implemented.

Do not run `tofu init`, `tofu plan`, or `tofu apply` here. Hand-authored rollout
files would bypass the Architecture v2 `ResolvedPlan`, its exact Home/Cloud
placement, and the federation security contracts.

A future simple renderer must generate artifacts only after the plan has bound:

- at least one Home Site and one Cloud Site;
- Home-local Control, identity-signing, enrollment, and data authority;
- an exact Home-initiated connectivity overlay without default or broad private
  routes;
- service-by-service publication, TLS, authentication, origin identity, health,
  and rate-limit policy;
- a signed capability-scoped outbound-control channel;
- fail-closed Cloud/link partition behavior and reconciliation.

Until those renderers and executors have their own evidence, use
[`../../default-spec.yaml`](../../default-spec.yaml) only as a preview of the v2
intent shape and run the contract checks documented in
[`../../README.md`](../../README.md). It is not a deployment input with current
runtime support.
