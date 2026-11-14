# StackKits Roadmap

This release mirror carries the installable StackKits source and the public
operator documentation needed to build, install, inspect, and verify the
Basement, Cloud, and Modern Homelab kits.

## Current Focus

- Three public OSS kit surfaces share the `foundation/` library:
  - Basement Kit (`basement-kit`, local `context`, installed via
    `base.stackkit.cc`).
  - Cloud Kit (`cloud-kit`, cloud `context`, installed via
    `cloud.stackkit.cc`).
  - Modern Homelab (`modern-homelab`) — public native-v2 definition
    combining an explicit Home Site and Cloud Site. Archive presence proves
    authoring/validation only; incomplete runtime owners remain fail-closed.
- Standalone Docker Compose is the primary application runtime. Komodo and
  Coolify are explicit integrations with separately stated evidence.

<!-- kit-support:begin -->
| Kit | Target | Status |
| --- | --- | --- |
| Basement Kit | an existing Linux host at home | alpha at v0.57.13; no public-package pass recorded |
| Cloud Kit | an existing VPS with your own domain | alpha at v0.57.13; no public-package pass recorded |
| Modern Homelab | Home + Cloud, joined by an explicit federation bridge | alpha definition archive at v0.57.13; no public-package pass recorded |
<!-- kit-support:end -->

## Release Priorities

1. Keep all three kits installable from release archives without a private checkout.
2. Keep public installer endpoints returning executable shell, not website HTML.
3. Keep generated local links browser-native and portless under
   enrolled `*.home`.
4. Keep default/protected services closed to anonymous `2xx`; public L3
   exposure is allowed only when explicitly configured in access policy.

Detailed internal planning lives in the private development repository.
