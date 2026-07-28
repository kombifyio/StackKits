#!/usr/bin/env node
import {createHash} from 'node:crypto';
import {readFile, writeFile} from 'node:fs/promises';
import path from 'node:path';

const digest = /^sha256:[0-9a-f]{64}$/u;
const commit = /^[0-9a-f]{40}$/u;
const sha = (raw) => `sha256:${createHash('sha256').update(raw).digest('hex')}`;

function parse(argv) {
  const result = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    if (!key?.startsWith('--') || argv[index + 1] === undefined) {
      throw new Error('arguments must be --name value pairs');
    }
    result[key.slice(2)] = argv[index + 1];
  }
  const required = [
    'archive-evidence', 'runtime-receipt', 'ha-receipt', 'plan', 'inventory',
    'apply', 'verify', 'transcript', 'partition', 'process', 'tag',
    'source-commit', 'source-digest', 'output',
  ];
  for (const key of required) if (!result[key]) throw new Error(`--${key} is required`);
  return result;
}

async function document(file) {
  const raw = await readFile(path.resolve(file));
  return {raw, value: JSON.parse(raw)};
}

const exact = (actual, expected, label) => {
  if (actual !== expected) throw new Error(`${label} differs`);
};

export async function compose(options) {
  if (!commit.test(options['source-commit']) || !digest.test(options['source-digest'])) {
    throw new Error('source identity is invalid');
  }
  const [
    archive, runtime, ha, plan, inventory, apply, verify, transcript, partition, process,
  ] = await Promise.all([
    document(options['archive-evidence']), document(options['runtime-receipt']),
    document(options['ha-receipt']), document(options.plan), document(options.inventory),
    document(options.apply), document(options.verify), document(options.transcript),
    document(options.partition), readFile(path.resolve(options.process)),
  ]);
  exact(archive.value.release?.source?.commit, options['source-commit'], 'archive source commit');
  exact(archive.value.release?.source?.digest, options['source-digest'], 'archive source digest');
  exact(archive.value.release?.tag, options.tag, 'archive tag');
  exact(runtime.value.source?.commit, options['source-commit'], 'runtime source commit');
  exact(runtime.value.source?.digest, options['source-digest'], 'runtime source digest');
  exact(ha.value.source?.commit, options['source-commit'], 'HA source commit');
  exact(ha.value.source?.digest, options['source-digest'], 'HA source digest');
  exact(runtime.value.source?.tag, options.tag, 'runtime tag');
  exact(ha.value.source?.tag, options.tag, 'HA tag');
  if (archive.value.schemaVersion !== 'stackkit.modern-archive-live-proof-evidence/v3' ||
      archive.value.proofStatus !== 'pass' ||
      runtime.value.apiVersion !== 'stackkit.modern-runtime-live-receipt/v2' ||
      runtime.value.status !== 'pass' ||
      ha.value.apiVersion !== 'stackkit.modern-warm-standby-live-receipt/v2' ||
      ha.value.status !== 'pass' ||
      verify.value.schemaVersion !== 'stackkit.command-result/v1' ||
      verify.value.status !== 'success') {
    throw new Error('runtime, HA, or final verification did not pass');
  }
  const boundModules = (plan.value.modules ?? [])
    .filter((module) => module.enforcementRequirement?.status === 'bound');
  const requiredOwners = [...new Set(
    boundModules.map((module) => module.enforcementRequirement.ownerRef),
  )].sort();
  const appliedOwners = [...new Set(boundModules
    .filter((module) => (apply.value.runtime ?? []).some((item) =>
      item.status === 'applied' &&
      (item.requirementId === module.id || item.requirementId?.startsWith(`${module.id}/`))))
    .map((module) => module.enforcementRequirement.ownerRef))].sort();
  for (const owner of requiredOwners) {
    if (!appliedOwners.includes(owner)) throw new Error(`required runtime owner ${owner} has no applied result`);
  }
  const channels = Object.keys(inventory.value.executionChannels ?? {}).sort();
  if (JSON.stringify(channels) !== JSON.stringify([
    'local-cloud-edge', 'local-home-main', 'local-home-standby',
  ])) throw new Error('terminal Inventory does not bind all three exact channels');
  if (!Array.isArray(transcript.value.events) || transcript.value.events.length === 0 ||
      !digest.test(transcript.value.finalDigest ?? '')) {
    throw new Error('generic process transcript is incomplete');
  }
  for (const channel of channels) {
    if (!transcript.value.events.some((event) => event.channelRef === channel)) {
      throw new Error(`generic process transcript has no event for ${channel}`);
    }
  }
  for (const field of ['rpoSeconds', 'rtoSeconds']) {
    if (!Number.isFinite(ha.value.metrics?.[field]) || ha.value.metrics[field] < 0) {
      throw new Error(`HA receipt has no measured ${field}`);
    }
  }
  if (ha.value.metrics.rpoSeconds > plan.value.availability?.rpoSeconds ||
      ha.value.metrics.rtoSeconds > plan.value.availability?.rtoSeconds) {
    throw new Error('measured HA recovery exceeds the exact plan limits');
  }
  const files = {
    archiveEvidence: sha(archive.raw), runtimeReceipt: sha(runtime.raw),
    haReceipt: sha(ha.raw), plan: sha(plan.raw), inventory: sha(inventory.raw),
    apply: sha(apply.raw), verify: sha(verify.raw), transcript: sha(transcript.raw),
    partition: sha(partition.raw), process: sha(process),
  };
  const receipt = {
    apiVersion: 'stackkit.modern-terminal-live-receipt/v1',
    kind: 'ModernTerminalLiveReceipt',
    status: 'pass',
    source: {
      tag: options.tag, commit: options['source-commit'], digest: options['source-digest'],
    },
    files,
    channels,
    requiredRuntimeOwners: requiredOwners,
    appliedRuntimeOwners: appliedOwners,
    partition: {
      failClosed: partition.value.failClosed === true,
      homeContinued: partition.value.homeContinued === true,
      reconnected: partition.value.reconnected === true,
    },
    availability: {
      mode: 'warm-standby', fencedBeforeFailover: ha.value.fault?.fencedBeforeFailover === true,
      rpoSeconds: ha.value.metrics.rpoSeconds, rtoSeconds: ha.value.metrics.rtoSeconds,
      recovered: ha.value.recovery?.status === 'pass',
    },
    finalVerify: {status: 'success', sha256: files.verify},
  };
  if (!Object.values(receipt.partition).every(Boolean) ||
      !receipt.availability.fencedBeforeFailover || !receipt.availability.recovered) {
    throw new Error('partition or HA terminal claims are incomplete');
  }
  await writeFile(path.resolve(options.output), `${JSON.stringify(receipt, null, 2)}\n`, {mode: 0o600});
}

if (import.meta.url === new URL(`file://${path.resolve(process.argv[1]).replaceAll('\\', '/')}`).href) {
  compose(parse(process.argv.slice(2))).catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
