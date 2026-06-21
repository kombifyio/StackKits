# Contributing

StackKits is built around CUE contracts and Go code.

## Local Gates

Run the narrowest relevant checks for your change:

```sh
go test ./...
cue vet ./base/... ./base-kit/...
npm --prefix website install
npm --prefix website run build
```

When changing generated rollout output, update the CUE or Go source and
regenerate instead of patching generated files directly.

## Public Release Surface

The public repository is generated from an explicit allowlist in the private
upstream. Do not add internal infrastructure details, private service URLs, or
secrets to public docs, tests, workflows, or examples.
