# Notices for bundled third-party software

StackKits distributes the following unmodified executables alongside its own
code. Their MPL-2.0 license does not change the StackKits license choice in
[LICENSING.md](LICENSING.md). The complete MPL-2.0 text is in
[LICENSE-MPL-2.0](LICENSE-MPL-2.0).

| Component | Bundled version | License | Source and upstream notice |
| --- | --- | --- | --- |
| Terramate CLI | 0.17.1 | MPL-2.0 | [Source](https://github.com/terramate-io/terramate/tree/v0.17.1), [LICENSE](https://github.com/terramate-io/terramate/blob/v0.17.1/LICENSE), [ThirdPartyNotice.txt](https://github.com/terramate-io/terramate/blob/v0.17.1/ThirdPartyNotice.txt) (reproduced below) |
| OpenTofu CLI | 1.12.6 | MPL-2.0 | [Source](https://github.com/opentofu/opentofu/tree/v1.12.6), [LICENSE](https://github.com/opentofu/opentofu/blob/v1.12.6/LICENSE) |
| OpenTofu `hashicorp/local` provider | 2.5.3 | MPL-2.0 | [Source](https://github.com/opentofu/terraform-provider-local/tree/v2.5.3), [LICENSE](https://github.com/opentofu/terraform-provider-local/blob/v2.5.3/LICENSE); upstream LICENSE is also present beside each bundled provider binary |
| OpenTofu `kreuzwerker/docker` provider | 4.6.0 | MPL-2.0 | [Source](https://github.com/kreuzwerker/terraform-provider-docker/tree/v4.6.0), [LICENSE](https://github.com/kreuzwerker/terraform-provider-docker/blob/v4.6.0/LICENSE); upstream LICENSE is also present beside each bundled provider binary. Used only by roots of modules explicitly opted in to the ADR-0045 Stage 2 pilot |
| OpenTofu `sebastianfs82/komodo` provider | 0.12.0 | MPL-2.0 | [Source](https://github.com/sebastianfs82/terraform-provider-komodo/tree/v0.12.0), [LICENSE](https://github.com/sebastianfs82/terraform-provider-komodo/blob/v0.12.0/LICENSE); upstream LICENSE is also present beside each bundled provider binary |

The pinned upstream LICENSE files carry these copyright notices:

```text
OpenTofu:
Copyright (c) The OpenTofu Authors
Copyright (c) 2014 HashiCorp, Inc.

OpenTofu hashicorp/local provider:
Copyright (c) 2017 HashiCorp, Inc.

OpenTofu sebastianfs82/komodo provider:
Copyright (c) 2021 HashiCorp, Inc.
```

The component versions match `scripts/release/fetch-terramate.sh`,
`scripts/release/fetch-opentofu.sh`, and
`scripts/release/fetch-opentofu-providers.sh`. The release archive check
requires one provider row per entry in `internal/tofu/provider_manifest.json`.
Source remains available at the pinned upstream tags above. Terramate's
upstream third-party notice follows verbatim.

## Optional upstream platform applications

The application images below are fetched for explicitly selected integrations;
they are not executables bundled with the CLI. Version pins remain in the
owning contracts and do not establish runtime support. Existing legacy
installations retain their selected pin until an admitted transition.

| Application | Current declared pin | License | Source and upstream license |
| --- | --- | --- | --- |
| Komodo Core and Periphery | 2.3.0 together (legacy bootstrap templates and ADR-0003) | GPL-3.0 | [Source](https://github.com/moghtech/komodo/tree/v2.3.0), [LICENSE](https://github.com/moghtech/komodo/blob/v2.3.0/LICENSE) |
| Komodo Core and Periphery | 2.3.3 together (native Basement bootstrap; exact container digests in the CUE catalog) | GPL-3.0 | [Source](https://github.com/moghtech/komodo/tree/v2.3.3), [LICENSE](https://github.com/moghtech/komodo/blob/v2.3.3/LICENSE) |
| MongoDB Community Server | 7 (node-local Komodo control dependency; exact container digest in the CUE catalog) | SSPL-1.0 | [Source](https://github.com/mongodb/mongo/tree/r7.0.0), [LICENSE](https://github.com/mongodb/mongo/blob/r7.0.0/LICENSE-Community.txt); the upstream container retains its own notices |
| Coolify in the legacy Full core | 4.1.2 (Architecture v2 core catalog) | Apache-2.0 | [Source](https://github.com/coollabsio/coolify/tree/v4.1.2), [LICENSE](https://github.com/coollabsio/coolify/blob/v4.1.2/LICENSE) |

Marketplace targets digest-pinned Coolify 4.4.x with `AUTOUPDATE=false`
(ADR-0049). That target is not the legacy 4.1.2 pin. Its implementation must
record the exact release, digest and matching upstream notice before release;
the 4.4.6 development probe is not a shipped Marketplace pin.

## Go browser capacity runtime

The WebMCP build includes Go's unmodified `lib/wasm/wasm_exec.js` and the
compiled standard runtime with the shared StackKits capacity engine. The Go
version follows `go.mod`; the build copies the JavaScript from that exact
toolchain's `GOROOT` and includes its unmodified BSD-3-Clause `LICENSE` as
`assets/GO-LICENSE`. `assets/manifest.json` records the Go version, engine source
digest and hashes of the WASM module, JavaScript runtime and license. Published
npm consumers receive these static artifacts and need no Go source checkout.

Upstream: [Go source](https://github.com/golang/go),
[license](https://github.com/golang/go/blob/master/LICENSE).

## Terramate v0.17.1 upstream ThirdPartyNotice.txt

```text
NOTICES

This repository incorporates material as listed below or described in the code.

---------------------------------------------------------
./tg v0.55.0 - MIT
https://github.com/gruntwork-io/terragrunt

The MIT License (MIT)
Copyright (c) 2016 Gruntwork, LLC

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

## Apache Guacamole Remote application

Apache Guacamole client and guacd are selected upstream container images,
version 1.6.0 under Apache-2.0. The immutable image refs and index digests are
owned by `foundation/architecture_v2_guacamole.cue`; a pin is not runtime support.
The initial profile uses upstream static XML authentication, without a database.

Client: [source](https://github.com/apache/guacamole-client/tree/1.6.0),
[license](https://github.com/apache/guacamole-client/blob/1.6.0/LICENSE),
[notice](https://github.com/apache/guacamole-client/blob/1.6.0/NOTICE).
Daemon: [source](https://github.com/apache/guacamole-server/tree/1.6.0),
[license](https://github.com/apache/guacamole-server/blob/1.6.0/LICENSE),
[notice](https://github.com/apache/guacamole-server/blob/1.6.0/NOTICE).

## AdGuard Home Network application

AdGuard Home v0.107.79 is an upstream GPL-3.0 application selected as the
Network DNS filter, not vendored application code. The immutable multi-platform
image is `docker.io/adguard/adguardhome:v0.107.79@sha256:aba9e3bf0613be3ba3755e1fc311b126e2c24bec25e18b6483894a88283074f0`.
Source and license: https://github.com/AdguardTeam/AdGuardHome/tree/v0.107.79
and https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.79/LICENSE.txt.
Upstream configuration reference: https://adguard-dns.io/kb/adguard-home/configuration/.

## OpenCloud Files application

The optional Files alternative uses OpenCloud server8.1.0, Apache-2.0, from https://github.com/opencloud-eu/opencloud/releases/tag/v8.1.0. The canonical image is docker.io/opencloudeu/opencloud-rolling:8.1.0 at sha256:8fc64ca861739cc62095cd558814f2eba6a90acf5998d884e439b33a47875da8. The renderer derives image and release values from the CUE catalog. Upstream retains its file, sharing, WebDAV and collaboration features and their licenses. This integration grants no provider lifecycle or SQL-server ownership.

## ntfy live notification relay

The optional explicitly selected ntfy-live workload uses the unmodified upstream
ntfy v2.30.0 container image, pinned to multi-platform digest
`sha256:8563a39da2f5a47360618b2ab9200239c76b37f4145f6d627d9ab4368abd23f0`.
Source: [ntfy v2.30.0](https://github.com/binwiederhier/ntfy/tree/v2.30.0).
The upstream [LICENSE](https://github.com/binwiederhier/ntfy/blob/v2.30.0/LICENSE)
is Apache-2.0, copyright2021 Philipp C. Heckel. No ntfy feature is reimplemented
by StackKits; the upstream image retains its bundled dependency notices.
