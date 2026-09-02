import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import {
  TOOL_NAMES,
  canonicalJson,
  catalogDigestPayload,
  createPlannerSession,
  createToolDefinitions,
  profileDigestPayload,
  registerStackKitsWebMcp,
  sha256Hex,
  validateAndVerifyCatalog,
} from '../dist/index.js'
import { projectAuthorityBundle } from '../dist/generator.js'

const catalog = await projectAuthorityBundle(
  fileURLToPath(new URL('../../internal/architecturev2/authority_bundle/', import.meta.url)),
  '0123456789abcdef0123456789abcdef01234567',
)

test('v2 catalog and tool definitions preserve the closed read-only contract', async () => {
  const integrity = await validateAndVerifyCatalog(catalog)
  assert.equal(integrity.valid, true)
  assert.equal(Object.isFrozen(integrity.catalog), true)
  assert.equal(Object.isFrozen(integrity.catalog.kits[0].modules), true)
  const session = createPlannerSession(catalog, { sourceSha: catalog.source_sha })
  const definitions = createToolDefinitions(session)
  assert.deepEqual(definitions.map(({ name }) => name), [...TOOL_NAMES])
  assert.equal(definitions.some(({ name }) => name === 'stackkits_get_tier_profile'), false)
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

  const invalid = await session.invoke('stackkits_get_module_profiles', {
    stackkit_id: 'basement-kit',
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

test('profile discovery is complete through bounded pages and supports a direct module lookup', async () => {
  const session = createPlannerSession(catalog)
  const kit = catalog.kits.find(({ stackkit_id }) => stackkit_id === 'basement-kit')
  const discovered = new Set()
  let cursor
  do {
    const result = await session.invoke('stackkits_get_module_profiles', {
      stackkit_id: kit.stackkit_id,
      ...(cursor === undefined ? {} : { cursor }),
    })
    assert.equal(result.outcome, 'success')
    assert.ok(JSON.stringify(result).length < 1500, 'each current profile page must fit the public output budget')
    for (const module of result.data.modules) {
      assert.equal(discovered.has(module.module_id), false, 'pages must not repeat a module')
      discovered.add(module.module_id)
    }
    cursor = result.data.next_cursor
  } while (cursor !== undefined)
  assert.deepEqual([...discovered].sort(), kit.modules.map(({ module_id }) => module_id).sort())
  const direct = await session.invoke('stackkits_get_module_profiles', {
    stackkit_id: kit.stackkit_id,
    module_id: 'stackkits-immich-runtime',
  })
  assert.equal(direct.outcome, 'success')
  assert.ok(direct.data.modules.some(({ module_id }) => module_id === 'stackkits-immich-runtime'))
})

test('representative catalog, capacity and complete handoffs stay within the public output budget', async () => {
  const session = createPlannerSession(catalog)
  const core = {
    stackkit_id: 'basement-kit',
    ...selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone' }], 'standard'),
    declared_capacity: { cpu_cores: 4, ram_gb: 4, storage_gb: 20 },
  }
  const cloud = {
    stackkit_id: 'cloud-kit',
    ...selectionFor('cloud-kit', [{ use_case_id: 'cloud-core', alternative_id: 'standalone' }], 'standard'),
    declared_capacity: core.declared_capacity,
    authoring: { domain_base: 'example.test' },
  }
  const photo = {
    stackkit_id: 'basement-kit',
    ...selectionFor('basement-kit', [
      { use_case_id: 'basement-core', alternative_id: 'standalone-lite' },
      { use_case_id: 'photos', alternative_id: 'immich-lite' },
    ]),
    declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
  }
  for (const [tool, input] of [
    ['stackkits_list_catalog', {}],
    ['stackkits_assess_capacity', core],
    ['stackkits_prepare_handoff', core],
    ['stackkits_prepare_handoff', cloud],
    ['stackkits_prepare_handoff', { ...core, declared_capacity: { cpu_cores: 2, ram_gb: 4, storage_gb: 20 } }],
    ['stackkits_assess_capacity', photo],
    ['stackkits_assess_capacity', { ...core, declared_capacity: { cpu_cores: 1, ram_gb: 1, storage_gb: 1 } }],
    ['stackkits_assess_capacity', { ...core, declared_capacity: { cpu_cores: 4 } }],
  ]) {
    const result = await session.invoke(tool, input)
    assert.equal(result.outcome, 'success')
    assert.ok(JSON.stringify(result).length < 1500, `${tool}: ${JSON.stringify(result).length} characters`)
  }
})

test('module profile projection exposes independent dimensions and explicit alternatives', async () => {
  const session = createPlannerSession(catalog)
  const existingSelection = {
    stackkit_id: 'basement-kit',
    use_cases: [{ use_case_id: 'basement-core', alternative_id: 'standalone-lite' }],
  }
  session.setSelection(existingSelection)
  const result = await session.invoke('stackkits_get_module_profiles', {
    stackkit_id: 'basement-kit',
    use_case_ids: ['basement-core', 'photos'],
  })
  assert.equal(result.outcome, 'success')
  assert.ok(result.data.use_case_alternatives.some(([useCaseId, alternativeId, moduleId]) =>
    useCaseId === 'basement-core' && alternativeId === 'standalone-lite' && moduleId.includes('core-lite-runtime')))
  const photos = await session.invoke('stackkits_get_module_profiles', {
    stackkit_id: 'basement-kit', module_id: 'stackkits-immich-lite-runtime',
  })
  assert.ok(photos.data.modules.some(({ profiles }) => profiles.some(([id]) => id === 'low')))
  assert.ok(result.data.modules.every(({ storage_profiles, accelerator_profiles }) =>
    Array.isArray(storage_profiles) && Array.isArray(accelerator_profiles)))
  const fullPhotos = await session.invoke('stackkits_get_module_profiles', {
    stackkit_id: 'basement-kit', module_id: 'stackkits-immich-runtime',
  })
  assert.deepEqual(Object.fromEntries(fullPhotos.data.modules[0].host_requirements
    .flatMap(({ profile_ids, requirements }) => profile_ids.map((id) => [id, requirements]))),
    Object.fromEntries(catalog.kits.find(({ stackkit_id }) => stackkit_id === 'basement-kit')
      .modules.find(({ module_id }) => module_id === 'stackkits-immich-runtime').compute_profiles
      .map(({ id, host_requirements }) => [id, host_requirements])))
  const policy = await session.invoke('stackkits_get_module_profiles', {
    stackkit_id: 'basement-kit', module_id: 'stackkits-home-backup-target',
  })
  const planOnly = policy.data.modules.find(({ profiles }) => profiles.length === 0)
  assert.ok(planOnly, 'the public projection must preserve at least one plan-only module without synthesizing a profile')
  assert.equal('compute_tier' in result.data, false)
  assert.deepEqual(session.state.selection, existingSelection, 'profile discovery must not clear existing use-case selections')
})

test('required use-case alternatives and active module profiles are fail-closed', async () => {
  const session = createPlannerSession(catalog)
  const coreOnly = selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone-lite' }])
  const missingAlternative = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    module_profiles: coreOnly.module_profiles,
    declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
  })
  assert.equal(missingAlternative.outcome, 'invalid_input')
  assert.ok(missingAlternative.notices.some(({ code }) => code === 'REQUIRED_USE_CASE_SELECTION_MISSING'))

  const missingProfile = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    use_cases: [{ use_case_id: 'basement-core', alternative_id: 'standalone-lite' }],
    module_profiles: [],
    declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
  })
  assert.equal(missingProfile.outcome, 'invalid_input')
})

test('native initial workloads are required before a WebMCP handoff can be ready', async () => {
  const session = createPlannerSession(catalog)
  const selection = selectionFor('modern-homelab', [{ use_case_id: 'basement-core', alternative_id: 'standalone' }], 'standard')
  const result = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'modern-homelab',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 8, ram_gb: 16, storage_gb: 100 },
  })
  assert.equal(result.outcome, 'invalid_input')
  assert.ok(result.notices.some(({ code, field }) =>
    code === 'REQUIRED_USE_CASE_SELECTION_MISSING' && field === 'use_cases.photos'))
})

test('mixed module profiles expose per-axis unverified facts without recommendation selection', async () => {
  const partial = structuredClone(catalog)
  const photo = partial.kits.find(({ stackkit_id }) => stackkit_id === 'basement-kit')
    .modules.find(({ module_id }) => module_id === 'stackkits-immich-lite-runtime').compute_profiles[0]
  delete photo.host_floor
  photo.capacity_declaration = 'partial'
  await rehashCatalog(partial)
  const session = createPlannerSession(partial)
  const selection = selectionFor('basement-kit', [
    { use_case_id: 'basement-core', alternative_id: 'standalone-lite' },
    { use_case_id: 'photos', alternative_id: 'immich-lite' },
  ])
  const result = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
  })
  assert.equal(result.outcome, 'success')
  assert.equal(result.data.overall, 'unverified')
  assert.ok(result.data.unverified_modules.includes('stackkits-immich-lite-runtime'))
  assert.equal('compute_tier' in (result.selection ?? {}), false)

  const handoff = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'basement-kit',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 2, ram_gb: 2, storage_gb: 10 },
  })
  assert.equal(handoff.outcome, 'blocked')
  assert.equal(handoff.data.ready, false)
  assert.deepEqual(handoff.data.steps, [])
})

test('known minimum failure wins over another incomplete module axis', async () => {
  const session = createPlannerSession(catalog)
  const selection = selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone-lite' }])
  const result = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 1, ram_gb: 1, storage_gb: 1 },
  })
  assert.equal(result.outcome, 'success')
  assert.equal(result.data.checks.find(({ axis }) => axis === 'cpu_cores')?.status, 'fail')
  assert.equal(result.data.overall, 'fail')
})

test('handoff carries explicit v2 argv and keeps apply non-executable', async () => {
  const session = createPlannerSession(catalog)
  const selection = selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone' }], 'standard')
  const result = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'basement-kit',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 4, ram_gb: 4, storage_gb: 20 },
  })
  assert.equal(result.outcome, 'success')
  assert.equal(result.data.ready, true)
  assert.deepEqual(result.data.steps.map(([id]) => id), [
    'stackkit.init', 'stackkit.validate', 'stackkit.resolve', 'stackkit.generate', 'stackkit.plan',
  ])
  const argv = result.data.steps[0][1]
  assert.ok(argv.includes('--api-version'))
  assert.ok(argv.includes('stackkit/v2alpha2'))
  assert.ok(argv.includes('--module-compute-profile'))
  assert.ok(argv.includes('stackkits-basement-core-runtime=standard'))
  assert.ok(argv.includes('--use-case'))
  assert.ok(argv.includes('--use-case-alternative'))
  assert.equal(argv.includes('--compute-tier'), false)
  assert.equal(result.data.steps.some(([id]) => id === 'stackkit.apply'), false)
  for (const [id, , mutation, idempotent, ownerApproval] of result.data.steps) {
    const operation = catalog.operations.find((operation) => operation.id === id)
    assert.deepEqual([mutation, idempotent, ownerApproval], [operation.mutation, operation.idempotent, operation.owner_approval])
  }
  assert.deepEqual(session.state.selection.module_profiles, selection.module_profiles)
  assert.deepEqual(session.state.selection.use_cases, selection.use_cases)
  assert.deepEqual(result.data.apply_follow_up, {
    id: 'stackkit.apply', mutation: true, idempotent: false, owner_approval: true, executable: false,
  })
  assert.deepEqual(result.effects, { executed: false, target_mutation: false, provider_action: false })
})

test('unselected workload module profiles are rejected at the public boundary', async () => {
  const session = createPlannerSession(catalog)
  const selection = selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone-lite' }], 'low')
  selection.module_profiles.push({
    module_id: 'stackkits-basement-core-runtime',
    compute_profile: 'standard',
  })
  const result = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 8, ram_gb: 16, storage_gb: 100 },
  })
  assert.equal(result.outcome, 'invalid_input')
  assert.ok(result.notices.some(({ code }) => code === 'MODULE_PROFILE_UNSELECTED_WORKLOAD'))
})

test('every declared storage and accelerator axis requires an explicit selection', async () => {
  const axisCatalog = await catalogWithAxes()
  const session = createPlannerSession(axisCatalog)
  const selection = selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone' }], 'standard')
  const result = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 8, ram_gb: 16, storage_gb: 100 },
  })
  assert.equal(result.outcome, 'invalid_input')
  assert.ok(result.notices.some(({ code, field }) =>
    code === 'MODULE_PROFILE_SELECTION_INCOMPLETE' && field?.includes('.storage_profile')))
  assert.ok(result.notices.some(({ code, field }) =>
    code === 'MODULE_PROFILE_SELECTION_INCOMPLETE' && field?.includes('.accelerator_profile')))
})

test('axis profiles stay visible in projection and explicit handoff argv', async () => {
  const axisCatalog = await catalogWithAxes()
  const session = createPlannerSession(axisCatalog)
  const profiles = await session.invoke('stackkits_get_module_profiles', {
    stackkit_id: 'basement-kit', module_id: 'stackkits-basement-core-runtime',
  })
  assert.equal(profiles.outcome, 'success')
  const core = profiles.data.modules.find(({ module_id }) => module_id === 'stackkits-basement-core-runtime')
  assert.ok(core)
  assert.ok(core.storage_profiles.some(([id, declaration, reservation, realization, digest]) =>
    id === 'ssd' && declaration === 'declared' && reservation.some((value) => value !== null) &&
    realization === 'apply-ready' && typeof digest === 'string'))
  assert.ok(core.accelerator_profiles.some(([id]) => id === 'gpu'))

  const selection = selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone' }], 'standard')
  selection.module_profiles[0].storage_profile = 'ssd'
  selection.module_profiles[0].accelerator_profile = 'gpu'
  const handoff = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'basement-kit',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 8, ram_gb: 16, storage_gb: 100 },
  })
  assert.equal(handoff.outcome, 'success')
  assert.equal(handoff.data.ready, true)
  const argv = handoff.data.steps[0][1]
  assert.ok(argv.includes('--module-storage-profile'))
  assert.ok(argv.includes('stackkits-basement-core-runtime=ssd'))
  assert.ok(argv.includes('--module-accelerator-profile'))
  assert.ok(argv.includes('stackkits-basement-core-runtime=gpu'))
})

test('handoff blocks every selected module dimension until it is apply-ready', async () => {
  for (const [label, options] of [
    ['compute', { computeRealization: 'generation-ready' }],
    ['storage', { storageRealization: 'generation-ready' }],
    ['accelerator', { acceleratorRealization: 'generation-ready' }],
  ]) {
    const axisCatalog = await catalogWithAxes(options)
    const session = createPlannerSession(axisCatalog)
    const selection = selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone' }], 'standard')
    selection.module_profiles[0].storage_profile = 'ssd'
    selection.module_profiles[0].accelerator_profile = 'gpu'
    const result = await session.invoke('stackkits_prepare_handoff', {
      stackkit_id: 'basement-kit',
      module_profiles: selection.module_profiles,
      use_cases: selection.use_cases,
      declared_capacity: { cpu_cores: 8, ram_gb: 16, storage_gb: 100 },
    })
    assert.equal(result.outcome, 'blocked', label)
    assert.equal(result.data.ready, false, label)
  }
})

test('missing fields in a selected axis reservation remain independently unverified', async () => {
  const axisCatalog = await catalogWithAxes({ storageReservation: { ram_gb: 1 } })
  const session = createPlannerSession(axisCatalog)
  const selection = selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone' }], 'standard')
  selection.module_profiles[0].storage_profile = 'ssd'
  selection.module_profiles[0].accelerator_profile = 'gpu'
  const result = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    module_profiles: selection.module_profiles,
    use_cases: selection.use_cases,
    declared_capacity: { cpu_cores: 8, ram_gb: 16, storage_gb: 100 },
  })
  assert.equal(result.outcome, 'success')
  assert.equal(result.data.overall, 'unverified')
  assert.equal(result.data.checks.find(({ axis }) => axis === 'cpu_cores')?.status, 'unverified')
  assert.equal(result.data.checks.find(({ axis }) => axis === 'storage_gb')?.status, 'unverified')
  assert.ok(result.data.unverified_modules.includes('stackkits-basement-core-runtime'))
})

test('authoring, dependent state, and aborted invocations fail closed', async () => {
  const session = createPlannerSession(catalog)
  const cloud = selectionFor('cloud-kit', [{ use_case_id: 'cloud-core', alternative_id: 'standalone' }], 'standard')
  const missing = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'cloud-kit',
    module_profiles: cloud.module_profiles,
    use_cases: cloud.use_cases,
    declared_capacity: { cpu_cores: 4, ram_gb: 4, storage_gb: 20 },
  })
  assert.equal(missing.outcome, 'blocked')
  assert.ok(missing.notices.some(({ code, field }) => code === 'REQUIRED_AUTHORING_INPUT_MISSING' && field === 'network.domain.base'))

  const invalid = await session.invoke('stackkits_prepare_handoff', {
    stackkit_id: 'cloud-kit',
    module_profiles: cloud.module_profiles,
    use_cases: cloud.use_cases,
    declared_capacity: { cpu_cores: 4, ram_gb: 4, storage_gb: 20 },
    authoring_inputs: [{ path: 'network.domain.base', value: 'not a domain' }],
  })
  assert.equal(invalid.outcome, 'invalid_input')
  assert.ok(invalid.notices.some(({ code, field }) => code === 'INVALID_INPUT' && field === 'network.domain.base'))

  const valid = await session.invoke('stackkits_assess_capacity', {
    stackkit_id: 'basement-kit',
    module_profiles: selectionFor('basement-kit', [{ use_case_id: 'basement-core', alternative_id: 'standalone' }], 'standard').module_profiles,
    use_cases: [{ use_case_id: 'basement-core', alternative_id: 'standalone' }],
    declared_capacity: { cpu_cores: 4, ram_gb: 4, storage_gb: 20 },
  })
  assert.equal(valid.outcome, 'success')
  assert.ok(session.state.capacity)
  session.setCapacity({ cpu_cores: 1 })
  assert.equal(session.state.capacity, undefined)

  const before = session.state
  const controller = new AbortController()
  controller.abort()
  const aborted = await session.invoke('stackkits_get_module_profiles', { stackkit_id: 'basement-kit' }, controller.signal)
  assert.ok(aborted.notices.some(({ code }) => code === 'ABORTED'))
  assert.deepEqual(session.state, before)

  const pendingController = new AbortController()
  const pending = session.invoke('stackkits_get_module_profiles', { stackkit_id: 'basement-kit' }, pendingController.signal)
  session.setCapacity({ cpu_cores: 8, ram_gb: 16, storage_gb: 100 })
  const newerState = session.state
  pendingController.abort()
  assert.ok((await pending).notices.some(({ code }) => code === 'ABORTED'))
  assert.deepEqual(session.state, newerState, 'aborting an older invocation must not roll back a newer human edit')
})

test('registration discovers v2 tools and rejects a tampered direct catalog', async () => {
  const registered = []
  const document = { modelContext: { registerTool(tool) { registered.push(tool) } } }
  const session = createPlannerSession(catalog, { sourceSha: catalog.source_sha })
  const registration = await registerStackKitsWebMcp({ document, session, sourceSha: catalog.source_sha })
  assert.equal(registration.registered, true)
  assert.equal(registration.status.code, 'ready')
  assert.deepEqual(registered.map(({ name }) => name), [...TOOL_NAMES])
  registration.dispose()

  const unavailable = await registerStackKitsWebMcp({ document: {}, session, sourceSha: catalog.source_sha })
  assert.equal(unavailable.status.code, 'browser_api_unavailable')

  const tampered = structuredClone(catalog)
  tampered.kits[0].status = `${tampered.kits[0].status}-tampered`
  const rejected = []
  const badDocument = { modelContext: { registerTool(tool) { rejected.push(tool) } } }
  const badRegistration = await registerStackKitsWebMcp({ document: badDocument, catalog: tampered })
  assert.equal(badRegistration.registered, false)
  assert.equal(badRegistration.status.code, 'catalog_integrity_failed')
  assert.equal(rejected.length, 0)

  const mismatch = await registerStackKitsWebMcp({ document, session, sourceSha: '0'.repeat(40) })
  assert.equal(mismatch.status.code, 'catalog_source_mismatch')

  const failed = await registerStackKitsWebMcp({
    document: { modelContext: { registerTool() { throw new Error('registration rejected') } } }, session, sourceSha: catalog.source_sha,
  })
  assert.equal(failed.status.code, 'registration_failed')

  const timedOut = await registerStackKitsWebMcp({
    document, fetcher: () => new Promise(() => {}), catalogLoadOptions: { timeoutMs: 10 },
  })
  assert.equal(timedOut.status.code, 'catalog_fetch_timeout')
})

function selectionFor(stackkitId, useCases, preferredProfile) {
  const kit = catalog.kits.find(({ stackkit_id }) => stackkit_id === stackkitId)
  assert.ok(kit)
  const activeModuleIds = new Set(kit.modules.filter(({ required }) => required).map(({ module_id }) => module_id))
  for (const selected of useCases) {
    const useCase = kit.use_cases.find(({ use_case_id }) => use_case_id === selected.use_case_id)
    assert.ok(useCase)
    const alternative = useCase.alternatives.find(({ alternative_id }) => alternative_id === selected.alternative_id)
    assert.ok(alternative)
    activeModuleIds.add(alternative.module_id)
  }
  const module_profiles = [...activeModuleIds].sort().flatMap((moduleId) => {
    const module = kit.modules.find(({ module_id }) => module_id === moduleId)
    const profiles = module?.compute_profiles ?? []
    if (profiles.length === 0) return []
    const profile = profiles.find(({ id }) => id === preferredProfile) ?? profiles[0]
    return [{ module_id: moduleId, compute_profile: profile.id }]
  })
  return { module_profiles, use_cases: useCases }
}

async function catalogWithAxes({
  computeRealization,
  storageRealization = 'apply-ready',
  acceleratorRealization = 'apply-ready',
  storageReservation = { cpu_cores: 1, ram_gb: 2, storage_gb: 10 },
  acceleratorReservation = { cpu_cores: 1, ram_gb: 1, storage_gb: 1 },
} = {}) {
  const next = structuredClone(catalog)
  const kit = next.kits.find(({ stackkit_id }) => stackkit_id === 'basement-kit')
  const module = kit.modules.find(({ module_id }) => module_id === 'stackkits-basement-core-runtime')
  assert.ok(module)
  if (computeRealization) {
    const compute = module.compute_profiles.find(({ id }) => id === 'standard')
    assert.ok(compute)
    compute.realization = computeRealization
  }
  module.storage_profiles = [axisProfile('ssd', storageRealization, storageReservation)]
  module.accelerator_profiles = [axisProfile('gpu', acceleratorRealization, acceleratorReservation)]
  await rehashCatalog(next)
  return next
}

function axisProfile(id, realization, reservation) {
  return {
    id,
    profile_sha256: '',
    capacity_declaration: 'declared',
    maturity: 'supported',
    realization,
    reservation,
    components: [],
    capabilities: [],
  }
}

async function rehashCatalog(value) {
  for (const kit of value.kits) {
    for (const module of kit.modules) {
      for (const profile of [
        ...module.compute_profiles,
        ...module.storage_profiles,
        ...module.accelerator_profiles,
      ]) {
        profile.profile_sha256 = await digestJson(profileDigestPayload(profile))
      }
    }
  }
  value.catalog_sha256 = await digestJson(catalogDigestPayload(value))
}

async function digestJson(value) {
  return sha256Hex(new TextEncoder().encode(canonicalJson(value)))
}
