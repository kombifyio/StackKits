#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';

import { updateHomelabSetupActions } from './update-homelab-setup-actions.mjs';

const execFileAsync = promisify(execFile);

test('updateHomelabSetupActions merges completed browser setup drops into homelab artifact', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-homelab-setup-actions-'));
  const homelabPath = path.join(dir, 'homelab.json');
  const browserPath = path.join(dir, 'browser-evidence.json');
  await writeFile(homelabPath, JSON.stringify(homelabArtifact(), null, 2));
  await writeFile(browserPath, JSON.stringify(browserEvidence(), null, 2));

  const result = await updateHomelabSetupActions(homelabPath, browserPath);
  const updated = JSON.parse(await readFile(homelabPath, 'utf8'));

  assert.deepEqual(result, { observed: 4, missing: 0, skipped: false });
  assert.equal(updated.simulationStatus.status, 'pass');
  assert.deepEqual(updated.simulationStatus.observedSetupActions, homelabArtifact().simulation.setupActions);
  assert.deepEqual(updated.simulationStatus.missingSetupActions, []);
});

test('updateHomelabSetupActions keeps artifact incomplete when a required drop is not verified', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-homelab-setup-actions-missing-'));
  const homelabPath = path.join(dir, 'homelab.json');
  const browserPath = path.join(dir, 'browser-evidence.json');
  const evidence = browserEvidence();
  evidence.diagnostics.setupActions = evidence.diagnostics.setupActions.filter(
    (action) => action.dropName !== 'vaultwarden-admin-handoff',
  );
  evidence.diagnostics.setupState.drops['vaultwarden-admin-handoff'].status = 'waiting';
  evidence.diagnostics.setupState.drops['vaultwarden-admin-handoff'].phase = 'owner_activated';
  await writeFile(homelabPath, JSON.stringify(homelabArtifact(), null, 2));
  await writeFile(browserPath, JSON.stringify(evidence, null, 2));

  const result = await updateHomelabSetupActions(homelabPath, browserPath);
  const updated = JSON.parse(await readFile(homelabPath, 'utf8'));

  assert.deepEqual(result, { observed: 3, missing: 1, skipped: false });
  assert.equal(updated.simulationStatus.status, 'incomplete');
  assert.deepEqual(updated.simulationStatus.missingSetupActions, ['vaultwarden-admin-handoff']);
});

test('update-homelab-setup-actions CLI validates required arguments', async () => {
  await assert.rejects(
    execFileAsync(process.execPath, ['scripts/evidence/update-homelab-setup-actions.mjs', '--homelab', 'x']),
    /missing --browser-evidence/,
  );
});

function homelabArtifact() {
  return {
    scenarioId: 'SK-S1',
    simulation: {
      setupActions: [
        'kuma-platform-bootstrap',
        'cloudreve-owner-bootstrap',
        'vaultwarden-admin-handoff',
        'immich-owner-bootstrap',
      ],
      healthChecks: ['base-route'],
    },
    simulationStatus: {
      status: 'incomplete',
      observedSetupActions: ['kuma-platform-bootstrap'],
      missingSetupActions: [
        'cloudreve-owner-bootstrap',
        'vaultwarden-admin-handoff',
        'immich-owner-bootstrap',
      ],
      observedHealthChecks: ['base-route'],
      missingHealthChecks: [],
    },
  };
}

function browserEvidence() {
  return {
    diagnostics: {
      setupActions: [
        {
          dropName: 'vaultwarden-admin-handoff',
          dropStatus: 'completed',
          dropPhase: 'verified',
        },
        {
          dropName: 'immich-owner-bootstrap',
          dropStatus: 'completed',
          dropPhase: 'verified',
        },
      ],
      setupState: {
        drops: {
          'cloudreve-owner-bootstrap': {
            status: 'completed',
            phase: 'verified',
          },
          'vaultwarden-admin-handoff': {
            status: 'completed',
            phase: 'verified',
          },
          'immich-owner-bootstrap': {
            status: 'completed',
            phase: 'verified',
          },
        },
      },
    },
  };
}
