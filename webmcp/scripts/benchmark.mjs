// Diagnostic only: never a wall-clock CI or release gate.
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { performance } from 'node:perf_hooks'
import { gzipSync } from 'node:zlib'
import { createPlannerSession, createToolDefinitions, loadCatalog, registerStackKitsWebMcp } from '../dist/index.js'

const raw = await readFile(new URL('../data/stackkits-webmcp/v2alpha1/catalog.json', import.meta.url), 'utf8')
const samples = 200
const summarize = (values) => {
  const sorted = [...values].sort((a, b) => a - b)
  return { samples: sorted.length, p50_ms: +sorted[Math.floor(sorted.length * 0.5)].toFixed(3), p95_ms: +sorted[Math.ceil(sorted.length * 0.95) - 1].toFixed(3), max_ms: +sorted.at(-1).toFixed(3) }
}
const startup = []
let session
for (let index = 0; index < 20; index++) {
  const start = performance.now()
  const catalog = await loadCatalog(async () => new Response(raw))
  session = createPlannerSession(catalog, { sourceSha: catalog.source_sha })
  const registration = await registerStackKitsWebMcp({ session, document: { modelContext: { registerTool() {} } } })
  assert.equal(registration.registered, true)
  registration.dispose()
  startup.push(performance.now() - start)
}
// Include shared-state projection and a subscribed UI consumer, not only arithmetic.
session.subscribe(() => {})
const selection = {
  stackkit_id: 'basement-kit',
  module_profiles: [{ module_id: 'stackkits-basement-core-lite-runtime', compute_profile: 'low' }],
  use_cases: [{ use_case_id: 'basement-core', alternative_id: 'standalone-lite' }],
  declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
}
const inputs = {
  stackkits_list_catalog: {},
  stackkits_get_module_profiles: { stackkit_id: 'basement-kit', module_id: 'stackkits-basement-core-lite-runtime' },
  stackkits_assess_capacity: selection,
  stackkits_prepare_handoff: selection,
}
const calls = []
globalThis.fetch = () => { throw new Error('Tool execution must not use the network') }
for (const definition of createToolDefinitions(session)) {
  const times = []
  let output
  for (let index = 0; index < samples + 20; index++) {
    const start = performance.now()
    output = await definition.execute(inputs[definition.name])
    const elapsed = performance.now() - start
    assert.equal(output.outcome, 'success')
    if (index >= 20) times.push(elapsed)
  }
  calls.push({ tool: definition.name, ...summarize(times), json_characters: JSON.stringify(output).length })
}
console.log(JSON.stringify({
  diagnostic: 'stackkits-webmcp-local-latency/v1',
  node: process.version,
  source_sha: session.service.catalog.source_sha,
  catalog_bytes: Buffer.byteLength(raw),
  catalog_gzip_bytes: gzipSync(raw).length,
  scope: 'Local parse, integrity, registration, schema validation and shared state. Excludes network, browser bridge, DOM rendering and agent reasoning.',
  startup: summarize(startup),
  calls,
}, null, 2))
