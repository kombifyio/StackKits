# StackKits WebMCP v1

This directory is the public, framework-independent StackKits WebMCP package.
It contains the versioned JSON Schemas, generated TypeScript types, the pure
compute-tier planner, four read-only browser tools, a deterministic authority
projection generator, and a small reference host.

The package never selects a StackKit or compute tier implicitly. It does not
connect to a provider, inspect a host, execute a CLI command, or mutate a
target. Host facts are user declarations and remain separate from the full
StackKits host preflight.

## Build and generate

Use Node 24 or newer:

```sh
npm ci
npm run build
npm test
npm run generate:catalog -- --authority-bundle /path/to/authority_bundle --out data/stackkits-catalog.json --source-sha <40-char-sha>
```

The generator accepts only an OSS StackKits authority bundle. It requires the
bundle manifest, catalog, profile definitions, `compute-tier-fits.json`, and
`operations.json`; missing or unknown authority documents fail closed. Supply
the exact 40-character deployment commit with `--source-sha` or
`STACKKITS_BUILD_SOURCE_SHA`. If omitted, the CLI reads the current Git
commit.

The generated catalog is deterministic. It contains `source_sha`,
`authority_bundle_sha256`, and `catalog_sha256`; it never contains provider,
secret, credential, endpoint, or private-URL references.

Build the public reference host from this source checkout with the exact same
generated catalog and component:

```sh
cd reference-host
npm ci
npm run check
npm run build
```

The build copies the exported `../data/stackkits-catalog.json` to its
same-origin public path and injects that catalog's exact `source_sha`. In a
full private checkout it can materialize a missing local catalog from the OSS
authority bundle first. It does not fetch private website source or depend on
kombify Sites/Cubi.

## Browser integration

The page adapter exports `registerStackKitsWebMcp`. It registers exactly four
same-origin tools through `document.modelContext.registerTool` when that API
exists. A registration `AbortController` is passed to the registration call,
and each execution receives and observes the WebMCP execution signal. A
browser without WebMCP continues to work; the adapter simply reports
`available: false`.

The four public tools are `stackkits_list_catalog`,
`stackkits_get_tier_profile`, `stackkits_assess_capacity`, and
`stackkits_prepare_handoff`. Every result uses `stackkits-webmcp/v1`, carries
CUE/build/catalog provenance, and declares zero executed, target, or provider
effects. `apply` is only described as a non-executable follow-up.

The stateful Svelte component is available at
`@kombifyio/stackkits-webmcp/StackKitsPlanner.svelte`. Pass it the same
catalog/session used by the WebMCP adapter so human actions and agent calls
update one planner state. Browser hosts normally use
`@kombifyio/stackkits-webmcp/StackKitsPlannerHost.svelte`; it loads the
same-origin catalog without blocking the surrounding page, creates the shared
session, and registers the same four tools fail-closed.

## Public exports

`src/index.ts` exports the catalog and planner contracts, `createPlanner`, the
shared planner-session helpers, the four tool functions and definitions,
`registerStackKitsWebMcp`, and `StackKitsPlanner.svelte` through the package
subpath. The Node-only authority generator is exposed through `./generator`.
