# StackKits SvelteKit Smoke App

This fixture defines the canonical app contract used by SK-S2A/SK-S3A rollout tests.

Expected published image:

```text
ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0
```

Runtime contract:

- listens on `PORT`, default `3000`
- serves `/health` with HTTP 200 JSON
- renders the app name from `PUBLIC_APP_NAME`

Build locally:

```bash
docker build -t ghcr.io/kombifyio/stackkits-smoke-sveltekit:0.1.0 examples/sveltekit-smoke
```
