#!/usr/bin/env node
import {createHash} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import path from 'node:path';

const sha = (raw) => `sha256:${createHash('sha256').update(raw).digest('hex')}`;
const digest = /^sha256:[0-9a-f]{64}$/u;

function parse(argv) {
  const value = {};
  for (let index = 0; index < argv.length; index += 2) value[argv[index].slice(2)] = argv[index + 1];
  for (const key of ['receipt', 'artifacts-root', 'tag', 'source-commit', 'source-digest']) {
    if (!value[key]) throw new Error(`--${key} is required`);
  }
  return value;
}

export async function validate(options) {
  const receipt = JSON.parse(await readFile(path.resolve(options.receipt)));
  if (receipt.apiVersion !== 'stackkit.modern-terminal-live-receipt/v1' ||
      receipt.kind !== 'ModernTerminalLiveReceipt' || receipt.status !== 'pass' ||
      receipt.source?.tag !== options.tag ||
      receipt.source?.commit !== options['source-commit'] ||
      receipt.source?.digest !== options['source-digest']) {
    throw new Error('terminal receipt source or contract differs');
  }
  const names = {
    archiveEvidence: 'modern-archive-live-proof-evidence.json',
    runtimeReceipt: 'modern-runtime-live-receipt.json',
    haReceipt: 'modern-warm-standby-live-receipt.json',
    plan: 'resolved-plan.json', inventory: 'inventory.json',
    apply: 'apply-result.json', verify: 'verify.json',
    transcript: 'modern-runtime-process-transcript.json',
    partition: 'docker-partition-evidence.json',
    process: 'modern-runtime-process',
  };
  const values = {};
  for (const [key, name] of Object.entries(names)) {
    if (!digest.test(receipt.files?.[key] ?? '')) throw new Error(`receipt ${key} digest is invalid`);
    const raw = await readFile(path.join(path.resolve(options['artifacts-root']), name));
    if (sha(raw) !== receipt.files[key]) throw new Error(`${name} differs from terminal receipt`);
    if (name.endsWith('.json')) values[key] = JSON.parse(raw);
  }
  const exactSource = (value, label) => {
    if (value?.tag !== options.tag ||
        value?.commit !== options['source-commit'] ||
        value?.digest !== options['source-digest']) {
      throw new Error(`${label} is not bound to the exact release source`);
    }
  };
  exactSource({
    tag: values.archiveEvidence?.release?.tag,
    ...values.archiveEvidence?.release?.source,
  }, 'archive evidence');
  if (values.archiveEvidence?.release?.tag !== options.tag ||
      values.archiveEvidence?.schemaVersion !== 'stackkit.modern-archive-live-proof-evidence/v3' ||
      values.archiveEvidence?.proofStatus !== 'pass' ||
      !digest.test(values.archiveEvidence?.release?.archive?.sha256 ?? '')) {
    throw new Error('archive evidence contract or release digest differs');
  }
  exactSource(values.runtimeReceipt?.source, 'runtime receipt');
  exactSource(values.haReceipt?.source, 'HA receipt');
  if (values.runtimeReceipt?.apiVersion !== 'stackkit.modern-runtime-live-receipt/v2' ||
      values.runtimeReceipt?.status !== 'pass' ||
      values.haReceipt?.apiVersion !== 'stackkit.modern-warm-standby-live-receipt/v2' ||
      values.haReceipt?.status !== 'pass') {
    throw new Error('runtime or HA receipt contract differs');
  }
  exactSource(values.transcript?.source, 'runtime transcript');
  const channels = Object.keys(values.inventory?.executionChannels ?? {}).sort();
  if (JSON.stringify(channels) !== JSON.stringify([
    'local-cloud-edge', 'local-home-main', 'local-home-standby',
  ]) || JSON.stringify(receipt.channels) !== JSON.stringify(channels) ||
      !channels.every((channel) =>
        values.transcript?.events?.some((event) => event.channelRef === channel))) {
    throw new Error('terminal channel set is not backed by Inventory and transcript');
  }
  const boundModules = (values.plan?.modules ?? [])
    .filter((module) => module.enforcementRequirement?.status === 'bound');
  const requiredOwners = [...new Set(
    boundModules.map((module) => module.enforcementRequirement.ownerRef),
  )].sort();
  const appliedOwners = [...new Set(boundModules
    .filter((module) => (values.apply?.runtime ?? []).some((item) =>
      item.status === 'applied' &&
      (item.requirementId === module.id || item.requirementId?.startsWith(`${module.id}/`))))
    .map((module) => module.enforcementRequirement.ownerRef))].sort();
  if (JSON.stringify(receipt.requiredRuntimeOwners) !== JSON.stringify(requiredOwners) ||
      JSON.stringify(receipt.appliedRuntimeOwners) !== JSON.stringify(appliedOwners) ||
      requiredOwners.length === 0 ||
      JSON.stringify(requiredOwners) !== JSON.stringify(appliedOwners)) {
    throw new Error('terminal receipt does not cover the exact runtime-owner set');
  }
  if (values.partition?.apiVersion !== 'stackkit.modern-partition-live-evidence/v1' ||
      values.partition?.failClosed !== true ||
      values.partition?.homeContinued !== true ||
      values.partition?.reconnected !== true ||
      receipt.partition?.failClosed !== values.partition.failClosed ||
      receipt.partition?.homeContinued !== values.partition.homeContinued ||
      receipt.partition?.reconnected !== values.partition.reconnected ||
      receipt.availability?.mode !== 'warm-standby' ||
      receipt.availability?.fencedBeforeFailover !== values.haReceipt?.fault?.fencedBeforeFailover ||
      receipt.availability?.recovered !== (values.haReceipt?.recovery?.status === 'pass') ||
      !Number.isFinite(receipt.availability?.rpoSeconds) ||
      !Number.isFinite(receipt.availability?.rtoSeconds) ||
      receipt.availability.rpoSeconds !== values.haReceipt?.metrics?.rpoSeconds ||
      receipt.availability.rtoSeconds !== values.haReceipt?.metrics?.rtoSeconds ||
      receipt.availability.rpoSeconds > values.plan?.availability?.rpoSeconds ||
      receipt.availability.rtoSeconds > values.plan?.availability?.rtoSeconds ||
      values.verify?.schemaVersion !== 'stackkit.command-result/v1' ||
      values.verify?.status !== 'success' ||
      receipt.finalVerify?.status !== values.verify.status ||
      receipt.finalVerify?.sha256 !== receipt.files.verify) {
    throw new Error('terminal runtime, partition, HA, or Verify closure is incomplete');
  }
}

const options = parse(process.argv.slice(2));
validate(options).catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
