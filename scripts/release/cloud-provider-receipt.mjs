#!/usr/bin/env node

import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ALLOWED_PROVIDERS = new Set(['centron-managed', 'ionos-managed']);
const ALLOWED_SCENARIOS = new Set(['SK-S2', 'SK-S3']);
const PRIVATE_REPOSITORY = 'kombify/kombify-StackKits';

export function renderCloudProviderReceipt({
  scenarioId,
  provider,
  artifact,
  artifactPath = '',
  cleanup,
  cleanupPath = '',
  source = {},
  generatedAt = new Date().toISOString(),
}) {
  const failures = [];

  if (!ALLOWED_SCENARIOS.has(scenarioId)) {
    failures.push(`unsupported scenario ${JSON.stringify(scenarioId)}`);
  }
  if (!ALLOWED_PROVIDERS.has(provider)) {
    failures.push(`unsupported provider ${JSON.stringify(provider)}`);
  }
  if (!artifact || typeof artifact !== 'object' || Array.isArray(artifact)) {
    failures.push('scenario artifact is missing or invalid');
  } else {
    if (artifact.scenarioId !== scenarioId) {
      failures.push(`scenario artifact identifies ${JSON.stringify(artifact.scenarioId)} instead of ${scenarioId}`);
    }
    if (artifact.status !== 'passed') {
      failures.push(`scenario artifact status is ${JSON.stringify(artifact.status)} instead of "passed"`);
    }
    if (artifact.target?.provider !== provider) {
      failures.push(`scenario artifact provider is ${JSON.stringify(artifact.target?.provider)} instead of ${provider}`);
    }
  }

  if (!cleanup || typeof cleanup !== 'object' || Array.isArray(cleanup)) {
    failures.push('cleanup ledger is missing or invalid');
  } else {
    if (cleanup.schemaVersion !== 'stackkit.provider-cleanup/v1') {
      failures.push(`cleanup ledger schemaVersion is ${JSON.stringify(cleanup.schemaVersion)} instead of "stackkit.provider-cleanup/v1"`);
    }
    if (cleanup.scenarioId !== scenarioId) {
      failures.push(`cleanup ledger identifies scenario ${JSON.stringify(cleanup.scenarioId)} instead of ${scenarioId}`);
    }
    if (cleanup.provider !== provider) {
      failures.push(`cleanup ledger identifies provider ${JSON.stringify(cleanup.provider)} instead of ${provider}`);
    }
    if (cleanup.status !== 'completed') {
      failures.push(`cleanup ledger status is ${JSON.stringify(cleanup.status)} instead of "completed"`);
    }
    if (!Array.isArray(cleanup.observedResources)) {
      failures.push('cleanup ledger observedResources must be an array');
    }
    if (!Array.isArray(cleanup.deletedResources)) {
      failures.push('cleanup ledger deletedResources must be an array');
    }
    if (!Array.isArray(cleanup.remainingResources)) {
      failures.push('cleanup ledger remainingResources must be an array');
    } else if (cleanup.remainingResources.length !== 0) {
      failures.push(`cleanup ledger has ${cleanup.remainingResources.length} remaining resource(s)`);
    }
    if (!Array.isArray(cleanup.errors)) {
      failures.push('cleanup ledger errors must be an array');
    } else if (cleanup.errors.length !== 0) {
      failures.push(`cleanup ledger contains ${cleanup.errors.length} cleanup error(s)`);
    }
    const intent = cleanup.allocationIntent;
    if (!intent || typeof intent !== 'object' || Array.isArray(intent)) {
      failures.push('cleanup ledger allocationIntent is missing or invalid');
    } else {
      if (intent.schemaVersion !== 'stackkit.provider-allocation-intent/v1') failures.push('cleanup allocation intent schemaVersion is invalid');
      if (intent.scenarioId !== scenarioId || intent.provider !== provider) failures.push('cleanup allocation intent target does not match the provider cell');
      if (typeof intent.testName !== 'string' || intent.testName === '' || typeof intent.ownershipKey !== 'string' || intent.ownershipKey === '') {
        failures.push('cleanup allocation intent has no owned testName/ownershipKey');
      }
      if (intent.lifecycle !== 'ephemeral-release-smoke' || intent.protectedPersistent !== false) {
        failures.push('cleanup allocation intent is not an unprotected ephemeral release-smoke resource');
      }
      const createdAt = Date.parse(intent.createdAt ?? '');
      const expiresAt = Date.parse(intent.expiresAt ?? '');
      if (!Number.isFinite(createdAt) || !Number.isFinite(expiresAt) || expiresAt <= createdAt || expiresAt - createdAt > 4 * 60 * 60 * 1000) {
        failures.push('cleanup allocation intent TTL is invalid or exceeds four hours');
      }
    }
  }

  const testedSource = cleanup?.testedSource ?? {};
  const intentSource = cleanup?.allocationIntent?.testedSource ?? {};
  if (intentSource.mode !== testedSource.mode || intentSource.sha !== testedSource.sha
      || intentSource.bundleSha256 !== testedSource.bundleSha256 || intentSource.releaseVersion !== testedSource.releaseVersion) {
    failures.push('pre-allocation ownership ledger source does not match cleanup testedSource');
  }
  const sourceSHA = String(source.sha || '');
  if (source.repository !== PRIVATE_REPOSITORY) {
    failures.push(`source repository is ${JSON.stringify(source.repository)} instead of ${PRIVATE_REPOSITORY}`);
  }
  if (!/^[0-9a-f]{40}$/.test(sourceSHA)) {
    failures.push(`source SHA is not a lowercase full commit SHA: ${JSON.stringify(sourceSHA)}`);
  }
  if (testedSource.mode !== 'candidate-bundle') {
    failures.push(`tested source mode is ${JSON.stringify(testedSource.mode)} instead of "candidate-bundle"`);
  }
  if (testedSource.sha !== sourceSHA) {
    failures.push(`tested source SHA is ${JSON.stringify(testedSource.sha)} instead of ${sourceSHA}`);
  }
  if (!/^[0-9a-f]{64}$/.test(String(testedSource.bundleSha256 || ''))) {
    failures.push(`tested candidate bundle SHA256 is invalid: ${JSON.stringify(testedSource.bundleSha256)}`);
  }
  if (testedSource.releaseVersion !== '') {
    failures.push(`tested releaseVersion must be empty for exact-source evidence, got ${JSON.stringify(testedSource.releaseVersion)}`);
  }

  const sourceTreeSHA = String(source.treeSha || '');
  if (!/^[0-9a-f]{40}$/.test(sourceTreeSHA)) {
    failures.push(`source tree SHA is not a lowercase full Git tree SHA: ${JSON.stringify(sourceTreeSHA)}`);
  }
  const sourceRunID = String(source.runId || '');
  if (!/^[A-Za-z0-9][A-Za-z0-9_.-]{5,127}$/.test(sourceRunID)) {
    failures.push(`source run ID is not a canonical provider-preview run_id: ${JSON.stringify(sourceRunID)}`);
  }
  const previewURL = String(artifact?.browserUrl || '');
  if (!/^https:\/\//.test(previewURL)) {
    failures.push(`scenario artifact browserUrl is not an HTTPS preview URL: ${JSON.stringify(previewURL)}`);
  }
  const cleanupPassed = cleanup?.status === 'completed'
    && Array.isArray(cleanup?.remainingResources)
    && cleanup.remainingResources.length === 0
    && Array.isArray(cleanup?.errors)
    && cleanup.errors.length === 0;

  return {
    schema_version: 1,
    kind: 'provider-preview',
    repo: String(source.repository || ''),
    run_id: sourceRunID,
    preview_url: previewURL,
    head_sha: sourceSHA,
    tree_sha: sourceTreeSHA,
    scenario_result: artifact?.status === 'passed' ? 'PASS' : 'FAIL',
    schemaVersion: 'stackkit.cloud-provider-receipt/v2',
    scenarioId,
    provider,
    status: failures.length === 0 ? 'pass' : 'blocked',
    generatedAt,
    source: {
      repository: String(source.repository || ''),
      sha: String(source.sha || ''),
      workflow: String(source.workflow || ''),
      runId: String(source.runId || ''),
      runAttempt: String(source.runAttempt || ''),
      treeSha: sourceTreeSHA,
      mode: String(testedSource.mode || ''),
      bundleSha256: String(testedSource.bundleSha256 || ''),
      releaseVersion: String(testedSource.releaseVersion || ''),
    },
    evidence: {
      scenarioArtifact: artifactPath,
      cleanupLedger: cleanupPath,
      observedProvider: artifact?.target?.provider || '',
      scenarioStatus: artifact?.status || '',
    },
    cleanup: {
      result: cleanupPassed ? 'PASS' : 'FAIL',
      remaining_resources: Array.isArray(cleanup?.remainingResources) ? cleanup.remainingResources : null,
      status: String(cleanup?.status || ''),
      allocationIntent: cleanup?.allocationIntent ?? null,
      observedResources: Array.isArray(cleanup?.observedResources) ? cleanup.observedResources : [],
      deletedResources: Array.isArray(cleanup?.deletedResources) ? cleanup.deletedResources : [],
      remainingResources: Array.isArray(cleanup?.remainingResources) ? cleanup.remainingResources : null,
      errors: Array.isArray(cleanup?.errors) ? cleanup.errors : null,
    },
    ...(failures.length > 0 ? { failures } : {}),
  };
}

export async function renderCloudProviderReceiptFile(options) {
  let artifact;
  let cleanup;
  const readFailures = [];
  try {
    artifact = JSON.parse(await readFile(options.artifactPath, 'utf8'));
  } catch (error) {
    readFailures.push(`cannot read scenario artifact: ${error.message}`);
  }
  try {
    cleanup = JSON.parse(await readFile(options.cleanupPath, 'utf8'));
  } catch (error) {
    readFailures.push(`cannot read cleanup ledger: ${error.message}`);
  }

  const receipt = renderCloudProviderReceipt({
    ...options,
    artifact,
    cleanup,
  });
  if (readFailures.length > 0) {
    receipt.status = 'blocked';
    receipt.failures = [...readFailures, ...(receipt.failures || [])];
  }

  await mkdir(path.dirname(options.outputPath), { recursive: true });
  await writeFile(options.outputPath, `${JSON.stringify(receipt, null, 2)}\n`, 'utf8');
  return receipt;
}

export function validateCloudProviderReceipt({
  receipt,
  scenarioId,
  provider,
  sourceSha = '',
  runId = '',
  expectedBundleSha256 = '',
  artifact,
}) {
  const errors = [];
  if (!receipt || typeof receipt !== 'object' || Array.isArray(receipt)) {
    return ['provider receipt is missing or invalid'];
  }
  if (receipt.schemaVersion !== 'stackkit.cloud-provider-receipt/v2') {
    errors.push('provider receipt schemaVersion is invalid');
  }
  if (receipt.schema_version !== 1 || receipt.kind !== 'provider-preview') {
    errors.push('provider receipt does not implement provider-preview schema_version 1');
  }
  if (receipt.repo !== PRIVATE_REPOSITORY || receipt.repo !== receipt.source?.repository) {
    errors.push('provider receipt canonical repo does not match its exact source repository');
  }
  if (receipt.run_id !== String(receipt.source?.runId || '')
      || !/^[A-Za-z0-9][A-Za-z0-9_.-]{5,127}$/.test(String(receipt.run_id || ''))) {
    errors.push('provider receipt canonical run_id is invalid or source-mismatched');
  }
  if (!/^https:\/\//.test(String(receipt.preview_url || ''))) {
    errors.push('provider receipt preview_url must use HTTPS');
  }
  if (receipt.head_sha !== receipt.source?.sha || !/^[0-9a-f]{40}$/.test(String(receipt.head_sha || ''))) {
    errors.push('provider receipt canonical head_sha is invalid or source-mismatched');
  }
  if (receipt.tree_sha !== receipt.source?.treeSha || !/^[0-9a-f]{40}$/.test(String(receipt.tree_sha || ''))) {
    errors.push('provider receipt canonical tree_sha is invalid or source-mismatched');
  }
  if (receipt.scenario_result !== 'PASS') {
    errors.push(`provider receipt scenario_result is ${JSON.stringify(receipt.scenario_result)} instead of "PASS"`);
  }
  if (receipt.scenarioId !== scenarioId) {
    errors.push(`provider receipt scenario is ${JSON.stringify(receipt.scenarioId)} instead of ${scenarioId}`);
  }
  if (receipt.provider !== provider) {
    errors.push(`provider receipt identifies ${JSON.stringify(receipt.provider)} instead of ${provider}`);
  }
  if (receipt.status !== 'pass') {
    errors.push(`provider receipt status is ${JSON.stringify(receipt.status)} instead of "pass"`);
  }
  if (sourceSha && receipt.source?.sha !== sourceSha) {
    errors.push(`provider receipt source SHA is ${JSON.stringify(receipt.source?.sha)} instead of ${sourceSha}`);
  }
  if (runId && String(receipt.source?.runId || '') !== String(runId)) {
    errors.push(`provider receipt run ID is ${JSON.stringify(receipt.source?.runId)} instead of ${runId}`);
  }
  if (receipt.source?.mode !== 'candidate-bundle') {
    errors.push(`provider receipt source mode is ${JSON.stringify(receipt.source?.mode)} instead of "candidate-bundle"`);
  }
  if (!/^[0-9a-f]{64}$/.test(String(receipt.source?.bundleSha256 || ''))) {
    errors.push(`provider receipt bundle SHA256 is invalid: ${JSON.stringify(receipt.source?.bundleSha256)}`);
  }
  if (expectedBundleSha256 && receipt.source?.bundleSha256 !== expectedBundleSha256) {
    errors.push(`provider receipt bundle SHA256 is ${JSON.stringify(receipt.source?.bundleSha256)} instead of ${expectedBundleSha256}`);
  }
  if (receipt.source?.releaseVersion !== '') {
    errors.push(`provider receipt releaseVersion must be empty, got ${JSON.stringify(receipt.source?.releaseVersion)}`);
  }
  if (receipt.cleanup?.status !== 'completed') {
    errors.push(`provider receipt cleanup status is ${JSON.stringify(receipt.cleanup?.status)} instead of "completed"`);
  }
  if (receipt.cleanup?.allocationIntent?.schemaVersion !== 'stackkit.provider-allocation-intent/v1'
      || receipt.cleanup?.allocationIntent?.scenarioId !== scenarioId
      || receipt.cleanup?.allocationIntent?.provider !== provider
      || !receipt.cleanup?.allocationIntent?.ownershipKey
      || receipt.cleanup?.allocationIntent?.lifecycle !== 'ephemeral-release-smoke'
      || receipt.cleanup?.allocationIntent?.protectedPersistent !== false) {
    errors.push('provider receipt cleanup allocationIntent is invalid or does not match the provider cell');
  }
  const receiptIntentSource = receipt.cleanup?.allocationIntent?.testedSource ?? {};
  if (receiptIntentSource.mode !== receipt.source?.mode || receiptIntentSource.sha !== receipt.source?.sha
      || receiptIntentSource.bundleSha256 !== receipt.source?.bundleSha256
      || receiptIntentSource.releaseVersion !== receipt.source?.releaseVersion) {
    errors.push('provider receipt allocationIntent source does not match receipt source');
  }
  const intentCreatedAt = Date.parse(receipt.cleanup?.allocationIntent?.createdAt ?? '');
  const intentExpiresAt = Date.parse(receipt.cleanup?.allocationIntent?.expiresAt ?? '');
  if (!Number.isFinite(intentCreatedAt) || !Number.isFinite(intentExpiresAt)
      || intentExpiresAt <= intentCreatedAt || intentExpiresAt - intentCreatedAt > 4 * 60 * 60 * 1000) {
    errors.push('provider receipt cleanup allocationIntent TTL is invalid or exceeds four hours');
  }
  if (!Array.isArray(receipt.cleanup?.remainingResources) || receipt.cleanup.remainingResources.length !== 0) {
    errors.push('provider receipt cleanup remainingResources must be exactly an empty array');
  }
  if (receipt.cleanup?.result !== 'PASS'
      || !Array.isArray(receipt.cleanup?.remaining_resources)
      || receipt.cleanup.remaining_resources.length !== 0) {
    errors.push('provider receipt canonical cleanup requires result=PASS and remaining_resources=[]');
  }
  if (!Array.isArray(receipt.cleanup?.errors) || receipt.cleanup.errors.length !== 0) {
    errors.push('provider receipt cleanup errors must be exactly an empty array');
  }
  if (artifact) {
    if (artifact.scenarioId !== scenarioId) {
      errors.push(`scenario artifact identifies ${JSON.stringify(artifact.scenarioId)} instead of ${scenarioId}`);
    }
    if (artifact.status !== 'passed') {
      errors.push(`scenario artifact status is ${JSON.stringify(artifact.status)} instead of "passed"`);
    }
    if (artifact.target?.provider !== provider) {
      errors.push(`scenario artifact provider is ${JSON.stringify(artifact.target?.provider)} instead of ${provider}`);
    }
  }
  return errors;
}

function parseArgs(argv) {
  const options = {};
  for (let index = 0; index < argv.length; index += 1) {
    const key = argv[index];
    const value = argv[index + 1];
    if (!key.startsWith('--') || value === undefined) {
      throw new Error(`invalid argument near ${key}`);
    }
    options[key.slice(2)] = value;
    index += 1;
  }
  const requiredArgs = options['validate-receipt']
    ? ['scenario', 'provider', 'validate-receipt']
    : ['scenario', 'provider', 'artifact', 'cleanup', 'output'];
  for (const required of requiredArgs) {
    if (!options[required]) {
      throw new Error(`--${required} is required`);
    }
  }
  return options;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  if (args['validate-receipt']) {
    const receipt = JSON.parse(await readFile(args['validate-receipt'], 'utf8'));
    const artifact = args.artifact ? JSON.parse(await readFile(args.artifact, 'utf8')) : undefined;
    const errors = validateCloudProviderReceipt({
      receipt,
      scenarioId: args.scenario,
      provider: args.provider,
      sourceSha: args['source-sha'],
      runId: args['run-id'],
      expectedBundleSha256: args['expected-bundle-sha256'],
      artifact,
    });
    for (const error of errors) {
      console.error(`::error title=${args.scenario} ${args.provider} receipt rejected::${error}`);
    }
    if (errors.length > 0) {
      process.exitCode = 1;
    }
    return;
  }
  const receipt = await renderCloudProviderReceiptFile({
    scenarioId: args.scenario,
    provider: args.provider,
    artifactPath: args.artifact,
    cleanupPath: args.cleanup,
    outputPath: args.output,
    source: {
      repository: args.repository || process.env.GITHUB_REPOSITORY,
      sha: args['source-sha'] || process.env.GITHUB_SHA,
      treeSha: args['source-tree-sha'] || process.env.STACKKIT_SOURCE_TREE_SHA,
      workflow: args.workflow || process.env.GITHUB_WORKFLOW,
      runId: args['run-id'] || process.env.GITHUB_RUN_ID,
      runAttempt: args['run-attempt'] || process.env.GITHUB_RUN_ATTEMPT,
    },
  });
  if (receipt.status !== 'pass') {
    for (const failure of receipt.failures || []) {
      console.error(`::error title=${receipt.scenarioId} ${receipt.provider} release smoke blocked::${failure}`);
    }
    process.exitCode = 1;
  }
}

const isMain = process.argv[1] && path.resolve(process.argv[1]) === path.resolve(fileURLToPath(import.meta.url));
if (isMain) {
  await main();
}
