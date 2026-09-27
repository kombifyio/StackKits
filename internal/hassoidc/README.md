# Packaged Home Assistant OIDC

The release archive is unmodified upstream
[hass-oidc-auth v1.2.1](https://github.com/christiaangoossens/hass-oidc-auth/releases/tag/v1.2.1),
commit `0699a6774340e1440e770690c8af922bd933a1e7` (MIT, `LICENSE.upstream`).
Its SHA-256 is `e5badaaacaa63cfd6fe733924a05e76d75058836190398598fb24de57cd47ccd`.

`artifact.go` applies the separately governed `stackkit-owner-binding-v1` patch
and checks the exact upstream replacement anchors. `stackkit_binding.py` has its
own SHA-256 in Go and CUE. The patch adds an authenticated owner binding endpoint,
blocks OIDC before that binding exists, and converges existing non-owner OIDC role
grants at login. It leaves local credentials and owner status unchanged, and never
uses mutable username/email matching. Changes are StackKits' responsibility, not
an upstream feature claim.

Extra dependencies are private vendored Python packages extracted from the exact
PyPI wheels, retaining their licenses/notices alongside this source:

| Package | Wheel SHA-256 | License |
| --- | --- | --- |
| aiofiles 25.1.0 | `abe311e527c862958650f9438e859c1fa7568a141b22abcd015e120e86a85695` | Apache-2.0 |
| joserfc 1.7.0 | `17e5d7a5a35e65442b05efc435a3d5d46696ffa2c8a2ed0eea6f63fc268e3224` | BSD-3-Clause |

The extension's imports target these private packages. No global package or
`sys.path` is replaced. The immutable HA 2026.7.2 image supplies Jinja2 3.1.6 and
cryptography 48.0.1; startup checks those versions. The extension manifest's
requirements are emptied to prevent HA's online dependency installer. Upstream
joserfc 1.7.0 requires cryptography >=45.0.1, satisfied by the pinned image.

Primary sources:

- [Pinned extension configuration](https://github.com/christiaangoossens/hass-oidc-auth/blob/v1.2.1/docs/configuration.md)
- [Pinned extension package metadata](https://github.com/christiaangoossens/hass-oidc-auth/blob/v1.2.1/pyproject.toml)
- [HA image dependency installation](https://github.com/home-assistant/core/blob/2026.7.2/Dockerfile)
- [HA core dependency pins](https://github.com/home-assistant/core/blob/2026.7.2/pyproject.toml)
- [aiofiles metadata](https://pypi.org/pypi/aiofiles/25.1.0/json)
- [joserfc metadata](https://pypi.org/pypi/joserfc/1.7.0/json)

The package boundary test executes the actual patched owner view and role hook
against HA-shaped authentication/storage objects; it also compiles all packaged
Python sources. This is deterministic local evidence, not a real HA runtime or
browser login qualification.
