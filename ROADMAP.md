# StackKits Roadmap

This release mirror carries the installable StackKits source and the public
operator documentation needed to build, install, inspect, and verify BaseKit.

## Current Focus

- BaseKit is the verified beta one-click path.
- Modern Homelab and HA Kit remain alpha/scaffolding definitions until their
  rollout matrices graduate.
- The current BaseKit proof covers the local fallback path plus auth/setup
  guards. Complete Cubi/Coolify-managed L3 rollout remains a known blocker.

## Release Priorities

1. Keep BaseKit installable from release archives without a private checkout.
2. Keep public installer endpoints returning executable shell, not website HTML.
3. Keep generated local links browser-native and portless under
   `*.home.localhost`.
4. Keep default/protected services closed to anonymous `2xx`; public L3
   exposure is allowed only when explicitly configured in access policy.

Detailed internal planning lives in the private development repository.
