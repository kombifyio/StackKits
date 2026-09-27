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

The pinned upstream LICENSE files carry these copyright notices:

```text
OpenTofu:
Copyright (c) The OpenTofu Authors
Copyright (c) 2014 HashiCorp, Inc.

OpenTofu hashicorp/local provider:
Copyright (c) 2017 HashiCorp, Inc.
```

The component versions match `scripts/release/fetch-terramate.sh`,
`scripts/release/fetch-opentofu.sh`, and
`scripts/release/fetch-opentofu-providers.sh`. Source remains available at the
pinned upstream tags above. Terramate's upstream third-party notice follows
verbatim.

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
