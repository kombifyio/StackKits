# Canonical kit templates (source of truth)

This directory is the **single canonical source** for the per-kit OpenTofu /
Terramate template trees. The committed trees `basement-kit/templates/` and
`cloud-kit/templates/` are **generated artifacts** derived from here — do not
edit them by hand.

## How it works

The files here are byte-identical to every kit except for three literal
sentinels, which the generator substitutes per kit:

| Sentinel | `basement-kit` | `cloud-kit` |
|---|---|---|
| `__KIT_SLUG__` | `basement-kit` | `cloud-kit` |
| `__KIT_DISPLAY__` | `Basement Kit` | `Cloud Kit` |
| `__KIT_DISPLAY_UPPER__` | `BASEMENT KIT` | `CLOUD KIT` |

Sentinels are plain literals, not Go template directives, so the runtime
`{{ ... }}` directives and HCL `${ ... }` interpolations in the templates pass
through untouched — they are rendered later by `internal/template` at
`stackkit generate` time. ASCII banner lines whose width changes with the kit
name are re-padded so the box border stays aligned.

`README.md` (this file) is the only file the generator skips; everything else is
materialized into each kit.

## Regenerating

After editing any file here, regenerate the per-kit trees and commit the result:

```bash
go generate ./...            # or: mise run gen:kit-templates
```

The generator (`cmd/gen-kit-templates`, wired via `//go:generate` in
`internal/kittemplates`) also runs in the goreleaser `before` hooks. A freshness
test in `internal/kittemplates` fails if any per-kit tree drifts from what this
source would produce.
