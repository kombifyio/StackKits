# StackKits Roadmap

This release mirror carries the installable StackKits source and the public
operator documentation needed to build, install, inspect, and verify the
Basement and Cloud kits.

## Current Focus

- Two public OSS kit surfaces share the `base/` foundation library:
  - Basement Kit (`basement-kit`, local `context`, installed via
    `base.stackkit.cc`) — the stable v0.5 one-click path.
  - Cloud Kit (`cloud-kit`, cloud `context`, installed via
    `cloud.stackkit.cc`) — ships as scaffolding, graduating in v0.5.1.
- Product-bundled L3 applications are PaaS-intended by default. Complete
  Coolify-managed application-layer evidence remains a known blocker.

## Release Priorities

1. Keep both kits installable from release archives without a private checkout.
2. Keep public installer endpoints returning executable shell, not website HTML.
3. Keep generated local links browser-native and portless under
   `*.home.localhost`.
4. Keep default/protected services closed to anonymous `2xx`; public L3
   exposure is allowed only when explicitly configured in access policy.

Detailed internal planning lives in the private development repository.
