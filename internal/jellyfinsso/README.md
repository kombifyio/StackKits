# Jellyfin SSO distribution

The unmodified `sso-authentication_5.1.1.zip` is the maintained K0lin plugin,
not the archived 9p4 distribution. SHA-256:
`db126b4763bda3a3754fa2103232fc03ef1bf929cfeda35f2e9af39c875233b1`.

Upstream release and exact corresponding source:

- https://github.com/K0lin/jellyfin-plugin-sso/releases/tag/v5.1.1
- https://github.com/K0lin/jellyfin-plugin-sso/tree/82a095fa51ae3f6659a1375eafa66957ed33c859

Copyright belongs to the upstream contributors; the plugin is distributed under
GPL-3.0 (`LICENSE.upstream`). The archive includes Duende.IdentityModel 8.1.0 and
Duende.IdentityModel.OidcClient 7.1.0, copyright Duende Software, under Apache-2.0
(the repository's `LICENSE-APACHE`). Their assembly bytes were checked against
these exact NuGet packages. Corresponding sources are in
https://github.com/DuendeSoftware/foss at:

- IdentityModel: `47a77f2dbda08f418ec95af04fc75529768dc1e2`
- OidcClient: `8e981e459e9c45d325a51e11712d36a31b7ccb12`

The CLI embeds and verifies this archive without a runtime download. Only its
three assemblies are mounted read-only. Jellyfin owns `meta.json` and mutable
SSO account links; generated metadata disables automatic updates. The catalog
pin, closed renderer contract and embedded artifact must change together.
