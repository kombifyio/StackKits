# Production Test Targets

This directory contains the live regression tests for the two production-like
targets that matter for StackKits:

1. `stackkitsbase` for the local Base Kit path
2. `simulate.kombify.space` for the cloud/simulator path

These targets are intentionally named and wired explicitly. Future work should
extend these tests, not rediscover hosts or secret names.

## Fixed Target Mapping

### `stackkitsbase`

`stackkitsbase` is the dedicated local Base Kit regression host.

Canonical source of truth today:

- Doppler project: `kombify`
- Doppler config: `prd`
- Existing secrets:
  - `STACKKIT_TESTDEVICE_IP`
  - `STACKKIT_TESTDEVICE_USER`
  - `STACKKIT_TESTDEVICE_PASSWORD`

GitHub Actions must map those values into these stable secret names:

- `STACKKITSBASE_HOST`
- `STACKKITSBASE_PORT` (optional, defaults to `22`)
- `STACKKITSBASE_USER`
- `STACKKITSBASE_PASSWORD`
- `STACKKITSBASE_HOME` (optional, defaults to `/home/<user>`)
- `STACKKITSBASE_PROJECT_DIR` (optional, defaults to `<home>/my-homelab`)
- `STACKKITSBASE_ADMIN_EMAIL` (optional, defaults to `ci@kombify.io`)

Runner requirement:

- The `stackkitsbase` job must run on a runner with LAN access to the host.
- Configure repository variable `STACKKITSBASE_RUNNER` for that runner label.

Test entrypoint:

- `TestStackKitsBaseLocalReinstall`

What it does:

1. Connects to `stackkitsbase`
2. Removes the previous Base Kit deployment
3. Verifies the host is clean
4. Builds the current branch's `stackkit` binary
5. Uploads the current branch's `base`, `base-kit`, and `modules`
6. Runs `prepare`, `init`, `generate`, `apply`
7. Verifies the expected core containers are up

This test is meant to validate the current repository state, not the last
published installer release.

### `simulate.kombify.space`

`simulate.kombify.space` is the user-facing cloud simulator for StackKits.

Canonical source of truth today:

- Base URL: `https://simulate.kombify.space`
- ZITADEL issuer comes from Doppler project `kombify`
- Sim cloud auth client config comes from Doppler project `kombify-sim`

GitHub Actions must map these stable secret names:

- `ZITADEL_ISSUER`
- `KOMBISIM_AUTH_CLOUD_CLIENT_ID`
- `KOMBISIM_AUTH_CLOUD_REDIRECT_URL`

Test entrypoint:

- `TestSimCloudSSORedirectConfigured`

What it does:

1. Loads the public login page
2. Verifies the `kombify Cloud` sign-in entrypoint exists
3. Calls the ZITADEL authorize URL with the configured client/redirect
4. Fails if the redirect URI is rejected or if the flow returns `invalid_request`

Important:

- This test is intentionally user-facing. It protects the SSO path used by the
  actual simulator UI.
- If this test fails with `redirect_uri is missing in the client configuration`,
  the problem is in live auth configuration, not in the test itself.

## Workflow Ownership

The canonical CI wiring lives in:

- `.github/workflows/production-tests.yml`

That workflow is responsible for:

- API smoke against the gateway
- Sim UI auth regression
- `stackkitsbase` local reinstall regression
- optional slower live Sim installer tests

If a future change needs another host, secret, or runner, document it here at
the same time as the code change.

## Current Live Findings

As of 2026-03-16 the newly codified regressions expose two real production
issues:

- `TestSimCloudSSORedirectConfigured` fails because the ZITADEL authorize flow
  rejects `https://simulate.kombify.space/auth/callback` with
  `redirect_uri is missing in the client configuration`.
- `TestStackKitsBaseLocalReinstall` currently reaches `apply`, but the deploy
  fails on `data.docker_network.paas_traefik` because Docker network
  `dokploy-network` is not present when OpenTofu expects it.

Keep these notes current when the underlying issues are fixed.
