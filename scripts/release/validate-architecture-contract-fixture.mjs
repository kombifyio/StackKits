#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

const manifestSchema = 'stackkit.architecture-contract-fixtures/v2'
const manifestPath = 'architecture/v2/fixtures/contract-fixtures.manifest.json'
const authoritySourceManifestPath = 'architecture/v2/authority-manifest.json'
const contractFixtureSourcePath = 'architecture/v2/contractfixture/catalog.cue'
const definitionBindingSourcePath = 'base/architecture_v2_definition_binding.cue'
const contractFixtureBundleRoot = 'internal/architecturev2/contract_fixture_bundle'
const productBundleRoot = 'internal/architecturev2/authority_bundle'
const sha256Pattern = /^sha256:[0-9a-f]{64}$/
const contractFixtureAuthorityIssuer = 'stackkits-contract-fixture-authority/v1'
const expectedFixture = {
  spec: 'contract-two-node.yaml',
  inventory: 'contract-two-node.inventory.yaml',
  plan: 'contract-two-node.resolved-plan.json',
  kit: 'basement-kit',
  authorityClass: 'contract-fixture',
  authorityDocument: 'contractFixtureCatalog',
  authorityIssuer: contractFixtureAuthorityIssuer,
  scope: 'contract',
  graduationEligible: false,
  rendererRef: 'stackkit-contract-fixture'
}

function fail(message) {
  throw new Error(`Architecture v2 contract fixture proof failed: ${message}`)
}

function readJSON(root, relativePath) {
  try {
    return JSON.parse(readFileSync(path.join(root, relativePath), 'utf8'))
  } catch (error) {
    fail(`cannot decode ${relativePath}: ${error instanceof Error ? error.message : String(error)}`)
  }
}

function readBytes(root, relativePath) {
  try {
    return readFileSync(path.join(root, relativePath))
  } catch (error) {
    fail(`cannot read ${relativePath}: ${error instanceof Error ? error.message : String(error)}`)
  }
}

function sha256(bytes) {
  return `sha256:${createHash('sha256').update(bytes).digest('hex')}`
}

function assertEqual(actual, expected, label) {
  if (actual !== expected) {
    fail(`${label} is ${JSON.stringify(actual)}, want ${JSON.stringify(expected)}`)
  }
}

function assertExactKeys(value, expected, label) {
  const actual = Object.keys(value ?? {}).sort()
  const wanted = [...expected].sort()
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) {
    fail(`${label} fields are ${JSON.stringify(actual)}, want exactly ${JSON.stringify(wanted)}`)
  }
}

function assertLocalFilename(value, label) {
  if (typeof value !== 'string' || value === '' || path.basename(value) !== value || value.includes('/') || value.includes('\\')) {
    fail(`${label} must be one local fixture filename`)
  }
}

function assertSHA256(value, label) {
  if (typeof value !== 'string' || !sha256Pattern.test(value)) {
    fail(`${label} must be a lowercase sha256:<64-hex> digest`)
  }
}

function runPackagedSemanticProof(root) {
  const candidates = process.platform === 'win32'
    ? [path.join(root, 'stackkit.exe'), path.join(root, 'stackkit')]
    : [path.join(root, 'stackkit'), path.join(root, 'stackkit.exe')]
  const executable = candidates.find((candidate) => existsSync(candidate))
  if (!executable) {
    fail('packaged proof requires the stackkit executable at the archive root')
  }
  const result = spawnSync(executable, ['contract-proof', '--repo-root', root], {
    cwd: root,
    encoding: 'utf8',
    timeout: 120_000,
    windowsHide: true
  })
  if (result.error || result.status !== 0) {
    const detail = result.error?.message || result.stderr || result.stdout || `exit ${String(result.status)}`
    fail(`packaged semantic resolver proof failed: ${detail.trim()}`)
  }
}

export function validateContractFixtureProof(repoRoot, { proofOnly = false } = {}) {
  const root = path.resolve(repoRoot)
  const definitionBindingSource = readBytes(root, definitionBindingSourcePath)
  const manifest = readJSON(root, manifestPath)
  assertExactKeys(manifest, ['schemaVersion', 'compilerVersion', 'contractFixtures'], 'manifest')
  assertEqual(manifest.schemaVersion, manifestSchema, 'manifest.schemaVersion')
  if (typeof manifest.compilerVersion !== 'string' || manifest.compilerVersion === '') {
    fail('manifest.compilerVersion must be a non-empty string')
  }
  if (!Array.isArray(manifest.contractFixtures) || manifest.contractFixtures.length !== 1) {
    fail('manifest.contractFixtures must contain exactly the public contract proof')
  }

  const fixture = manifest.contractFixtures[0]
  assertExactKeys(fixture, [
    'spec', 'inventory', 'plan', 'kit', 'authorityClass', 'authorityDocument', 'authorityIssuer',
    'authorityCatalogHash', 'scope', 'graduationEligible', 'rendererRef', 'compilerVersion', 'specSha256', 'inventorySha256',
    'planSha256', 'planHash'
  ], 'contractFixtures[0]')
  for (const [field, expected] of Object.entries(expectedFixture)) {
    assertEqual(fixture[field], expected, `contractFixtures[0].${field}`)
  }
  assertEqual(fixture.compilerVersion, manifest.compilerVersion, 'contractFixtures[0].compilerVersion')
  for (const field of ['spec', 'inventory', 'plan']) {
    assertLocalFilename(fixture[field], `contractFixtures[0].${field}`)
  }

  const fixtureRoot = 'architecture/v2/fixtures'
  const spec = readBytes(root, `${fixtureRoot}/${fixture.spec}`)
  const inventory = readBytes(root, `${fixtureRoot}/${fixture.inventory}`)
  const planBytes = readBytes(root, `${fixtureRoot}/${fixture.plan}`)
  assertEqual(fixture.specSha256, sha256(spec), 'contractFixtures[0].specSha256')
  assertEqual(fixture.inventorySha256, sha256(inventory), 'contractFixtures[0].inventorySha256')
  assertEqual(fixture.planSha256, sha256(planBytes), 'contractFixtures[0].planSha256')

  let plan
  try {
    plan = JSON.parse(planBytes.toString('utf8'))
  } catch (error) {
    fail(`cannot decode ${fixture.plan}: ${error instanceof Error ? error.message : String(error)}`)
  }
  assertEqual(plan.planHash, fixture.planHash, 'ResolvedPlan.planHash')
  assertEqual(plan.compilerVersion, fixture.compilerVersion, 'ResolvedPlan.compilerVersion')
  assertEqual(plan.kit?.slug, fixture.kit, 'ResolvedPlan.kit.slug')
  assertEqual(plan.generation?.renderer?.id, fixture.rendererRef, 'ResolvedPlan.generation.renderer.id')
  assertEqual(plan.authority?.class, fixture.authorityClass, 'ResolvedPlan.authority.class')
  assertEqual(plan.authority?.document, fixture.authorityDocument, 'ResolvedPlan.authority.document')
  assertEqual(plan.authority?.graduationEligible, false, 'ResolvedPlan.authority.graduationEligible')
  assertEqual(plan.authority?.issuer, fixture.authorityIssuer, 'ResolvedPlan.authority.issuer')
  assertEqual(plan.authority?.catalogHash, fixture.authorityCatalogHash, 'ResolvedPlan.authority.catalogHash')
  if (Object.prototype.hasOwnProperty.call(plan.authority ?? {}, 'authorityFingerprint')) {
    fail('ResolvedPlan.authority.authorityFingerprint must be absent for the contract fixture authority')
  }
  assertExactKeys(
    plan.authority,
    ['catalogHash', 'class', 'document', 'graduationEligible', 'issuer'],
    'ResolvedPlan.authority'
  )
  assertSHA256(plan.authority?.catalogHash, 'ResolvedPlan.authority.catalogHash')
  assertSHA256(plan.kit?.definitionHash, 'ResolvedPlan.kit.definitionHash')
  if (typeof plan.kit?.version !== 'string' || !plan.kit.version.endsWith('-contract.fixture')) {
    fail('ResolvedPlan.kit.version must remain in the non-product contract.fixture namespace')
  }
  if (Array.isArray(plan.evidence) && plan.evidence.includes('SK-S1')) {
    fail('ResolvedPlan.evidence must not claim the product Basement SK-S1 scenario')
  }

  if (!proofOnly) {
    const authoritySourceManifest = readJSON(root, authoritySourceManifestPath)
    if (!Array.isArray(authoritySourceManifest.baseSources) ||
        !authoritySourceManifest.baseSources.includes(definitionBindingSourcePath)) {
      fail(`${authoritySourceManifestPath}.baseSources must include ${definitionBindingSourcePath}`)
    }
    const bundleManifest = readJSON(root, `${contractFixtureBundleRoot}/manifest.json`)
    if (Object.prototype.hasOwnProperty.call(bundleManifest, 'distributionFingerprint')) {
      fail('contract fixture authority bundle must not declare a product distributionFingerprint')
    }
    assertEqual(bundleManifest.documents?.contractFixtureCatalog, 'contract-fixture-catalog.json', 'authority bundle contract fixture document')
    assertEqual(bundleManifest.profiles?.[fixture.kit], 'definitions/basement-kit.json', 'contract fixture authority profile')
    assertEqual(Object.keys(bundleManifest.profiles ?? {}).length, 1, 'contract fixture authority profile count')
    assertEqual(bundleManifest.sourceHashes?.[contractFixtureSourcePath], sha256(readBytes(root, contractFixtureSourcePath)), 'contract fixture CUE source hash')
    assertEqual(
      bundleManifest.sourceHashes?.[definitionBindingSourcePath],
      sha256(definitionBindingSource),
      'contract fixture Definition binding CUE source hash'
    )

    const productBundleManifest = readJSON(root, `${productBundleRoot}/manifest.json`)
    if (productBundleManifest.documents?.contractFixtureCatalog !== undefined) {
      fail('product authority bundle declares the contract fixture catalog')
    }
    const productCatalog = JSON.stringify(readJSON(root, `${productBundleRoot}/catalog.json`))
    const contractCatalog = JSON.stringify(readJSON(root, `${contractFixtureBundleRoot}/contract-fixture-catalog.json`))
    const contractDefinition = readJSON(root, `${contractFixtureBundleRoot}/definitions/basement-kit.json`)
    assertEqual(contractDefinition.metadata?.slug, fixture.kit, 'contract fixture Definition slug')
    assertEqual(contractDefinition.metadata?.version, plan.kit?.version, 'contract fixture Definition version')
    for (const fixtureID of ['fixture-basement-provider', 'fixture-socket-proxy', 'fixture-http-consumer']) {
      if (productCatalog.includes(fixtureID)) {
        fail(`product catalog contains contract-only ID ${fixtureID}`)
      }
      if (!contractCatalog.includes(fixtureID)) {
        fail(`contract fixture catalog is missing ${fixtureID}`)
      }
    }
  }
  readBytes(root, contractFixtureSourcePath)

  if (proofOnly) {
    runPackagedSemanticProof(root)
  }

  return { compilerVersion: manifest.compilerVersion, planHash: fixture.planHash }
}

function parseRepoRoot(argv) {
  let repoRoot = process.cwd()
  let proofOnly = false
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === '--repo-root') {
      repoRoot = argv[index + 1]
      index += 1
    } else if (argv[index] === '--proof-only') {
      proofOnly = true
    } else {
      fail(`unknown argument ${argv[index]}`)
    }
  }
  return { repoRoot, proofOnly }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const options = parseRepoRoot(process.argv.slice(2))
    const result = validateContractFixtureProof(options.repoRoot, options)
    console.log(`Architecture v2 contract fixture proof passed (${result.compilerVersion}, ${result.planHash}).`)
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error))
    process.exit(1)
  }
}
