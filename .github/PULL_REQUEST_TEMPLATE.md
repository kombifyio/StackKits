## What changed

<!-- One paragraph. Conventional Commit title, e.g. `fix(cli): ...` -->

## Why

<!-- The user-visible problem or the contract this aligns with. -->

## How it was verified

<!-- Commands and results. Pre-1.0 the affected gate is the synchronous check:
     `mise run check` (Go/CUE/website slice + public boundary).
     Runtime claims need a cited run; missing evidence stays pending. -->

- [ ] `mise run check` passes
- [ ] No secrets, private hostnames, credentials or internal URLs in the diff
- [ ] Generated files were regenerated from their CUE/Go source, not hand-edited
