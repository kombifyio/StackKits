#!/usr/bin/env node
import { readFile, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

function parseArgs(argv) {
  const opts = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = argv[i + 1];
    switch (arg) {
      case '--homelab':
        if (!next || next.startsWith('--')) throw new Error('--homelab requires a value');
        opts.homelab = next;
        i += 1;
        break;
      case '--browser-evidence':
        if (!next || next.startsWith('--')) throw new Error('--browser-evidence requires a value');
        opts.browserEvidence = next;
        i += 1;
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
    }
  }
  if (!opts.homelab) throw new Error('missing --homelab');
  if (!opts.browserEvidence) throw new Error('missing --browser-evidence');
  return opts;
}

function normalizeStringArray(value) {
  if (!Array.isArray(value)) return [];
  return value.map((item) => String(item || '').trim()).filter(Boolean);
}

function addCompletedVerifiedDropsFromActions(observed, actions) {
  if (!Array.isArray(actions)) return;
  for (const action of actions) {
    if (!action || typeof action !== 'object') continue;
    const dropName = String(action.dropName || '').trim();
    const dropStatus = String(action.dropStatus || '').trim();
    const dropPhase = String(action.dropPhase || '').trim();
    if (dropName && dropStatus === 'completed' && dropPhase === 'verified') {
      observed.add(dropName);
    }
  }
}

function addCompletedVerifiedDropsFromSetupState(observed, setupState) {
  const drops = setupState?.drops;
  if (!drops || typeof drops !== 'object' || Array.isArray(drops)) return;
  for (const [dropName, drop] of Object.entries(drops)) {
    if (!drop || typeof drop !== 'object') continue;
    const name = String(dropName || '').trim();
    const status = String(drop.status || '').trim();
    const phase = String(drop.phase || '').trim();
    if (name && status === 'completed' && phase === 'verified') {
      observed.add(name);
    }
  }
}

export async function updateHomelabSetupActions(homelabPath, browserEvidencePath) {
  const artifact = JSON.parse(await readFile(homelabPath, 'utf8'));
  const evidence = JSON.parse(await readFile(browserEvidencePath, 'utf8'));
  const expected = normalizeStringArray(artifact?.simulation?.setupActions);
  if (expected.length === 0) {
    return { observed: 0, missing: 0, skipped: true };
  }

  artifact.simulationStatus = artifact.simulationStatus && typeof artifact.simulationStatus === 'object'
    ? artifact.simulationStatus
    : {};

  const observedSet = new Set(normalizeStringArray(artifact.simulationStatus.observedSetupActions));
  addCompletedVerifiedDropsFromActions(observedSet, evidence?.diagnostics?.setupActions);
  addCompletedVerifiedDropsFromSetupState(observedSet, evidence?.diagnostics?.setupState);

  const observed = [];
  const missing = [];
  for (const action of expected) {
    if (observedSet.has(action)) {
      observed.push(action);
    } else {
      missing.push(action);
    }
  }

  artifact.simulationStatus.observedSetupActions = observed;
  artifact.simulationStatus.missingSetupActions = missing;
  const missingHealthChecks = normalizeStringArray(artifact.simulationStatus.missingHealthChecks);
  artifact.simulationStatus.status = missing.length === 0 && missingHealthChecks.length === 0 ? 'pass' : 'incomplete';

  await writeFile(homelabPath, `${JSON.stringify(artifact, null, 2)}\n`, 'utf8');
  return { observed: observed.length, missing: missing.length, skipped: false };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const opts = parseArgs(process.argv.slice(2));
    const result = await updateHomelabSetupActions(opts.homelab, opts.browserEvidence);
    if (result.skipped) {
      console.log('homelab artifact has no simulation.setupActions; skipped setup-action backfill');
    } else {
      console.log(`updated homelab setup-action status: observed=${result.observed} missing=${result.missing}`);
    }
  } catch (error) {
    console.error(error.message || error);
    process.exitCode = 1;
  }
}
