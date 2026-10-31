# StackKits WebMCP v2alpha1

This directory is the public, framework-independent StackKits WebMCP package.
It contains the versioned JSON Schemas, generated TypeScript types, the pure
module-profile planner, four read-only browser tools, a deterministic authority
projection generator, and a small reference host.

The tool contract never selects a StackKit, use-case alternative, or module
profile implicitly. In the human planner, choosing a kit visibly loads its
CUE-declared starting workloads and alternatives. Module-local defaults, or
the only declared profile for a dimension, initialize editable selections;
ambiguous dimensions without a declared default remain an explicit choice.
Use-case tiles add or remove optional workloads without clearing the user's
declared host capacity. A changed selection invalidates the previous assessment
and handoff. Workloads fixed by the current CLI authoring contract stay included;
for example, Modern Homelab's initial Photos workload carries dependent publication
and data bindings. No capacity, host compatibility, or installation is inferred.

The package does not connect to a provider, inspect a host, execute a CLI command, or mutate a
target. Host facts are user declarations and remain separate from the full
StackKits host preflight.

## Build and generate

Use Node 24 or newer:

The build also needs the repository's pinned Go toolchain. Capacity runs through
`internal/capacityfit`, compiled into a static Go WebAssembly module with the
matching `wasm_exec.js`; the browser carries no parallel sizing arithmetic.
Published npm packages include the verified WASM, runtime, Go license and
asset manifest. Consumers outside the StackKits source repository reuse those
packaged bytes; their build does not require Go or a surrounding Go checkout.

```sh
pnpm install --frozen-lockfile
pnpm run build
pnpm test
pnpm run generate:catalog --authority-bundle /path/to/authority_bundle --schema v2alpha1 --out data/stackkits-webmcp/v2alpha1/catalog.json --source-sha <40-char-sha>
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
pnpm install --frozen-lockfile
pnpm run check
pnpm run build
```

The build copies the exported `../data/stackkits-webmcp/v2alpha1/catalog.json`
to its same-origin public path and injects that catalog's exact `source_sha`. In a
full private checkout it can materialize a missing local v2 catalog from the OSS
authority bundle first. It does not fetch private website source or depend on
kombify Sites or kombify Sites Framework.

## Browser integration

The page adapter exports `registerStackKitsWebMcp`. It registers exactly four
same-origin tools through `document.modelContext.registerTool` when that API
exists. A registration `AbortController` is passed to the registration call,
and each execution receives and observes the WebMCP execution signal. A
browser without WebMCP continues to work; the adapter simply reports
`available: false` with the explicit `browser_api_unavailable` status. The host
also reports distinct `catalog_fetch_failed`, `catalog_fetch_timeout`,
`catalog_integrity_failed`, `catalog_source_mismatch`, `registration_failed`,
and `registration_timeout` states. Catalog loading and tool registration each
have a 4,500 ms safety deadline. Retry restarts the catalog and registration
attempt; a missing browser API requires a WebMCP-capable browser and is never
replaced with a polyfill.

The four public tools are `stackkits_list_catalog`,
`stackkits_get_module_profiles`, `stackkits_assess_capacity`, and
`stackkits_prepare_handoff`. Every result uses `stackkits-webmcp/v2alpha1`, carries
CUE/build/catalog provenance, and declares zero executed, target, or provider
effects. `apply` is only described as a non-executable follow-up.

Catalog discovery returns one complete kit per page, including all its use-case
IDs. Start `stackkits_list_catalog` with `{}`, then repeat with
`cursor: data.next_cursor` until `next_cursor` is absent.

Profile discovery returns one module per page. Prefer an exact `module_id` when
it is already known; otherwise repeat the same kit and filters with
`cursor: data.next_cursor` until `next_cursor` is absent. `use_case_ids` filters
both modules and their alternatives. Paging never selects a profile; the
visible planner reads the same complete catalog.

Use only the calls needed for the current question. When the user has already
chosen the complete module selection and declared capacity, call
`stackkits_prepare_handoff` directly: it includes the capacity assessment.
There is no mandatory list → profile → assess → handoff round trip. No tool
calls a capacity HTTP service or model inference. The catalog and same-origin
static capacity assets load at session initialization and are reused for all
assessments and handoffs, including while the page is offline. Missing engine
assets return `CAPACITY_ENGINE_UNAVAILABLE`; no JavaScript sizing fallback exists.
Library consumers can set `capacityAssetBaseURL` in the planner/session options
or the host component to their own same-origin static directory. The reference
host copies the engine/runtime next to its public catalog and honors its base path.

Compute profiles may include `host_requirements` for CPU instruction sets and
the persistent local data filesystem. Profile discovery groups identical
requirements under `host_requirements`, with explicit `profile_ids` per group,
preserving the existing compact profile tuples and bounded response size.
The UI shows the same CUE declarations. Numeric capacity assessment does not
attest these requirements: the CLI must observe them on the exact target before
Apply. A matching amount of RAM or free space cannot replace that observation.

To keep agent responses compact without dropping approval metadata, each handoff
step is a schema-defined tuple:
`[operation_id, argv, mutation, idempotent, owner_approval]`.
All five operations are returned together. The validated `init` argv carries the
complete selection, so a successful handoff does not echo those IDs again in the
envelope. Capacity results likewise do not repeat the supplied profile IDs.
The shared session preserves the full validated selection in the visible planner.
Catalog pages retain the existing 1,500-character response budget. Handoffs carry
the explicit local owner email and target Inventory path through the existing
`authoring_inputs` contract (`owner.email`, `inventory.path`). All required argv
and approval metadata are preserved regardless of the supplied path lengths;
browser-declared capacity does not attest the target. Module profile pages use a
4,096-character budget because they also carry the CUE description and the
component, capability, and degradation details for every available profile.

For a bounded local latency diagnostic over the existing build, run:

```sh
node scripts/benchmark.mjs
```

The diagnostic covers local parsing, integrity, registration, schema checks,
and shared state. It excludes network, browser-bridge, DOM-rendering, and agent
reasoning time.

The stateful Svelte component is available at
`@kombifyio/stackkits-webmcp/StackKitsPlanner.svelte`. Pass it the same
catalog/session used by the WebMCP adapter so human actions and agent calls
update one planner state. Browser hosts normally use
`@kombifyio/stackkits-webmcp/StackKitsPlannerHost.svelte`; it loads the
same-origin catalog without blocking the surrounding page, creates the shared
session, and registers the same four tools fail-closed.

The shared capacity control uses logarithmic pointer travel so small hosts stay
usable across the supported input range, while number fields retain exact
values. Keyboard arrows adjust capacity units, not the internal track position.
Blank fields remain undeclared. Every edit invalidates prior assessments and
handoffs, including invalid numeric input; aborting an older agent invocation
cannot restore stale capacity over a newer human edit.

## Human-to-agent handoff

The manual UI and browser-agent tools share one validated planner state. A ready
handoff offers a shell selector (Bash/POSIX or PowerShell), a copy action for each
command, and a complete **Copy agent Markdown** brief. Machine-readable `argv`
arrays remain available in expandable details. Shell formatting preserves argument
boundaries; it does not re-solve the selection or change the tool contract.

The Markdown brief includes the selection, declared capacity and notices, each
step's mutation/idempotency/approval metadata, and CUE/source/catalog provenance.
It asks the receiving agent to review one step at a time and stop on failure.
Copying is not permission to execute: local writes still need owner approval,
target compatibility must be checked, and `apply` is never a runnable handoff
step. There is deliberately no one-click multi-command runner.

Clipboard access is requested only from a user click. When the browser denies
it, the same output remains visible in a selectable text area. Changes to
selection, capacity, or authoring inputs invalidate obsolete handoffs and copy
feedback. These UI exports do not expand the compact WebMCP result budget.

## Public exports

`src/index.ts` exports the catalog and planner contracts, `createPlanner`, the
shared planner-session helpers, the four tool functions and definitions,
`registerStackKitsWebMcp`, and `StackKitsPlanner.svelte` through the package
subpath. The Node-only authority generator is exposed through `./generator`.
