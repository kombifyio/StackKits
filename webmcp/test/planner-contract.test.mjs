import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import {
  TOOL_NAMES,
  createPlannerSession,
  createToolDefinitions,
  registerStackKitsWebMcp,
  validateAndVerifyCatalog,
} from '../dist/index.js'
import { projectAuthorityBundle } from '../dist/generator.js'

const catalog = await projectAuthorityBundle(
  fileURLToPath(new URL('../../internal/architecturev2/authority_bundle/', import.meta.url)),
  '0123456789abcdef0123456789abcdef01234567',
)

test('catalog and WebMCP definitions preserve the closed read-only contract', async () => {
  const integrity = await validateAndVerifyCatalog(catalog)
  assert.equal(integrity.valid, true)
  assert.equal(Object.isFrozen(integrity.catalog), true)
  assert.equal(Object.isFrozen(integrity.catalog.kits[0].tiers), true)
  const session = createPlannerSession(catalog, { sourceSha: catalog.source_sha })
  const definitions = createToolDefinitions(session)
  assert.deepEqual(definitions.map(({ name }) => name), [...TOOL_NAMES])
  for (const definition of definitions) {
    assert.ok(definition.name.length < 30)
    assert.ok(definition.description.length < 500)
    assert.equal(JSON.stringify(definition.inputSchema).includes('"$ref"'), false)
    assert.ok(schemaDescriptions(definition.inputSchema).every((description) => description.length < 150))
    assert.deepEqual(Object.keys(definition.annotations).sort(), ['readOnlyHint', 'untrustedContentHint'])
    assert.equal(definition.annotations.readOnlyHint, true)
    assert.equal(definition.inputSchema.additionalProperties, false)
  }
  assert.deepEqual(definitions.map(({ annotations }) => annotations.untrustedContentHint), [false, false, true, true])

  const invalid = await session.invoke('stackkits_get_tier_profile', {
    stackkit_id: 'basement-kit',
    compute_tier: 'low',
    unknown: true,
  })
  assert.equal(invalid.outcome, 'invalid_input')
})

function schemaDescriptions(value) {
  if (Array.isArray(value)) return value.flatMap(schemaDescriptions)
  if (!value || typeof value !== 'object') return []
  return [
    ...(typeof value.description === 'string' ? [value.description] : []),
    ...Object.values(value).flatMap(schemaDescriptions),
  ]
}

test('Basement low exposes CUE substitutions, functions, and excluded media behavior', async () => {
  const session = createPlannerSession(catalog)
  const profile = await session.invoke('stackkits_get_tier_profile', { stackkit_id: 'basement-kit', compute_tier: 'low' })
  assert.equal(profile.outcome, 'success')
  assert.ok(profile.data.graph.substitutions.some(([from, to]) => from.includes('core-runtime') && to.includes('core-lite-runtime')))
  assert.ok(profile.data.graph.substitutions.some(([from, to]) => from.includes('immich-runtime') && to.includes('immich-lite-runtime')))
  const photos = profile.data.use_cases.included.find(([id]) => id === 'photos')
  assert.ok(photos)
  assert.ok(photos[2].includes('photo-management'))
  assert.ok(profile.data.use_cases.excluded.includes('media'))

  const media = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'basement-kit',
    compute_tier: 'low',
    declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
    use_case_ids: ['media'],
  })
  assert.equal(media.outcome, 'blocked')
  assert.ok(media.notices.some(({ code }) => code === 'USE_CASE_NOT_AVAILABLE_FOR_TIER'))
  assert.equal(media.data.steps.length, 0)
})

test('undeclared tiers fail closed without a standard fallback', async () => {
  const session = createPlannerSession(catalog)
  const result = await session.invoke('stackkits_get_tier_profile', { stackkit_id: 'cloud-kit', compute_tier: 'low' })
  assert.equal(result.outcome, 'invalid_input')
  assert.notEqual(result.selection?.compute_tier, 'standard')
  assert.ok(result.notices.some(({ code }) => code === 'UNDECLARED_COMPUTE_TIER'))
})

test('declared capacity precedence is fail, unverified, warning, then pass', async () => {
  const session = createPlannerSession(catalog)
  const base = { stackkit_id: 'basement-kit', compute_tier: 'standard' }
  const fail = await session.invoke('stackkits_assess_capacity', { ...base, declared_capacity: { cpu_cores: 1 } })
  assert.equal(fail.data.overall, 'fail')
  const incomplete = await session.invoke('stackkits_assess_capacity', { ...base, declared_capacity: { cpu_cores: 2 } })
  assert.equal(incomplete.data.overall, 'unverified')
  const warning = await session.invoke('stackkits_assess_capacity', { ...base, declared_capacity: { cpu_cores: 2, ram_gb: 4, storage_gb: 20 } })
  assert.equal(warning.data.overall, 'warning')
  const pass = await session.invoke('stackkits_assess_capacity', { ...base, declared_capacity: { cpu_cores: 4, ram_gb: 4, storage_gb: 20 } })
  assert.equal(pass.data.overall, 'pass')

  const blocked = await session.invoke('stackkits_prepare_handoff', { ...base, declared_capacity: { cpu_cores: 4 } })
  assert.equal(blocked.outcome, 'blocked')
  assert.ok(blocked.notices.some(({ code }) => code === 'DECLARED_CAPACITY_INCOMPLETE'))

  const warningHandoff = await session.invoke('stackkits_prepare_handoff', {
    ...base,
    declared_capacity: { cpu_cores: 2, ram_gb: 4, storage_gb: 20 },
  })
  assert.equal(warningHandoff.outcome, 'success')
  assert.equal(warningHandoff.data.ready, true)
  assert.equal(warningHandoff.data.capacity_status, 'warning')
})

test('handoff projects registry metadata but never makes apply executable', async () => {
  const session = createPlannerSession(catalog)
  const result = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'basement-kit',
    compute_tier: 'low',
    declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
    use_case_ids: ['photos'],
  })
  assert.equal(result.outcome, 'success')
  assert.equal(result.data.ready, true)
  assert.deepEqual(result.data.steps.map(({ id }) => id), [
    'stackkit.init', 'stackkit.validate', 'stackkit.resolve', 'stackkit.generate', 'stackkit.plan',
  ])
  assert.ok(result.data.steps.every(({ argv, mutation, idempotent, owner_approval }) =>
    Array.isArray(argv) && argv[0] === 'stackkit'
    && typeof mutation === 'boolean'
    && typeof idempotent === 'boolean'
    && typeof owner_approval === 'boolean'))
  assert.ok(result.data.steps[0].argv.includes('--compute-tier'))
  assert.ok(result.data.steps[0].argv.includes('--use-case'))
  assert.equal(result.data.steps.some(({ id }) => id === 'stackkit.apply'), false)
  assert.deepEqual(result.data.apply_follow_up, {
    id: 'stackkit.apply', mutation: true, idempotent: false, owner_approval: true, executable: false,
  })
  assert.deepEqual(result.effects, { executed: false, target_mutation: false, provider_action: false })
})

test('authoring contract and aborts block without changing planner state', async () => {
  const session = createPlannerSession(catalog)
  const missing = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'cloud-kit',
    compute_tier: 'standard',
    declared_capacity: { cpu_cores: 4, ram_gb: 4, storage_gb: 20 },
  })
  assert.equal(missing.outcome, 'blocked')
  assert.ok(missing.notices.some(({ code, field }) => code === 'REQUIRED_AUTHORING_INPUT_MISSING' && field === 'network.domain.base'))

  const bypass = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'cloud-kit',
    compute_tier: 'standard',
    declared_capacity: { cpu_cores: 4, ram_gb: 4, storage_gb: 20 },
    authoring_inputs: [{ path: 'network.domain.base', value: 'not a domain' }],
  })
  assert.equal(bypass.outcome, 'invalid_input')
  assert.equal(bypass.data.ready, false)
  assert.ok(bypass.notices.some(({ code, field }) => code === 'INVALID_INPUT' && field === 'network.domain.base'))

  session.setSelection({ stackkit_id: 'basement-kit' })
  const before = session.state
  const controller = new AbortController()
  controller.abort()
  const aborted = await session.invoke('stackkits_get_tier_profile', { stackkit_id: 'basement-kit', compute_tier: 'low' }, controller.signal)
  assert.ok(aborted.notices.some(({ code }) => code === 'ABORTED'))
  assert.deepEqual(session.state, before)

  const duringController = new AbortController()
  const running = session.invoke(
    'stackkits_get_tier_profile',
    { stackkit_id: 'basement-kit', compute_tier: 'low' },
    duringController.signal,
  )
  duringController.abort()
  const cancelled = await running
  assert.ok(cancelled.notices.some(({ code }) => code === 'ABORTED'))
  assert.deepEqual(session.state, before)
})

test('dependent planner state cannot show a stale handoff', async () => {
  const session = createPlannerSession(catalog)
  await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'basement-kit',
    compute_tier: 'low',
    declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
    use_case_ids: ['photos'],
  })
  assert.equal(session.state.handoff?.ready, true)
  await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    compute_tier: 'low',
    declared_capacity: { cpu_cores: 1, ram_gb: 2, storage_gb: 10 },
  })
  assert.equal(session.state.capacity?.overall, 'fail')
  assert.equal(session.state.handoff, undefined)
  assert.deepEqual(session.state.selection, { stackkit_id: 'basement-kit', compute_tier: 'low' })
})

test('representative outputs stay under the Chrome tool budget', async () => {
  const session = createPlannerSession(catalog)
  const results = [
    await session.invoke('stackkits_list_catalog', {}),
    await session.invoke('stackkits_get_tier_profile', { stackkit_id: 'basement-kit', compute_tier: 'low' }),
    await session.invoke('stackkits_assess_capacity', {
      stackkit_id: 'basement-kit', compute_tier: 'low', declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
    }),
    await session.invoke('stackkits_prepare_handoff', {
      stackkit_id: 'basement-kit', compute_tier: 'low', declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 }, use_case_ids: ['photos'],
    }),
  ]
  assert.ok(results.every((result) => JSON.stringify(result).length < 1500), results.map((result) => JSON.stringify(result).length).join(','))
})

test('registration discovers four tools and rejects a tampered direct catalog', async () => {
  const registered = []
  const document = { modelContext: { registerTool(tool) { registered.push(tool) } } }
  const session = createPlannerSession(catalog, { sourceSha: catalog.source_sha })
  const registration = await registerStackKitsWebMcp({ document, session, sourceSha: catalog.source_sha })
  assert.equal(registration.registered, true)
  assert.deepEqual(registered.map(({ name }) => name), [...TOOL_NAMES])
  registration.dispose()

  const tampered = structuredClone(catalog)
  tampered.kits[0].status = `${tampered.kits[0].status}-tampered`
  const rejected = []
  const badDocument = { modelContext: { registerTool(tool) { rejected.push(tool) } } }
  const badRegistration = await registerStackKitsWebMcp({ document: badDocument, catalog: tampered })
  assert.equal(badRegistration.registered, false)
  assert.equal(rejected.length, 0)
})
