#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import { test } from 'node:test';

import { validateScenarioArtifact } from './validate-scenario-artifact.mjs';

const execFileAsync = promisify(execFile);

test('validate-scenario-artifact accepts a canonical passing SK-S2 artifact', () => {
  const errors = [];
  validateScenarioArtifact(errors, validArtifact(), canonicalScenario());
  assert.deepEqual(errors, []);
});

test('validate-scenario-artifact accepts a canonical passing SK-S5 negative guard artifact', () => {
  const errors = [];
  validateScenarioArtifact(errors, validSKS5Artifact(), canonicalSKS5Scenario());
  assert.deepEqual(errors, []);
});

test('validate-scenario-artifact accepts SK-S3 run-scoped custom domains under the expected zone', () => {
  const errors = [];
  validateScenarioArtifact(errors, validSKS3Artifact(), canonicalSKS3Scenario());
  assert.deepEqual(errors, []);
});

test('validate-scenario-artifact rejects SK-S3 custom domains outside the expected zone', () => {
  const artifact = validSKS3Artifact({
    profile: {
      ...validSKS3Artifact().profile,
      domain: 'e2e-cd-1782104835.example.net',
    },
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalSKS3Scenario());
  assert.match(errors.join('\n'), /profile\.domain = e2e-cd-1782104835\.example\.net, want kombify\.pro/);
});

test('validate-scenario-artifact rejects Admin profile drift', () => {
  const artifact = validArtifact({
    profile: {
      ...validArtifact().profile,
      ownerSource: 'local',
    },
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  assert.match(errors.join('\n'), /profile\.ownerSource = local, want cloud/);
});

test('validate-scenario-artifact rejects missing cloud target metadata', () => {
  const artifact = validArtifact({
    target: {},
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  assert.match(errors.join('\n'), /target\.publicIp must be present for managed-lease scenarios/);
});

test('validate-scenario-artifact rejects incomplete service-health proof', () => {
  const artifact = validArtifact({
    simulationStatus: {
      status: 'incomplete',
      observedSetupActions: canonicalScenario().expected.simulation.setupActions,
      missingSetupActions: [],
      observedHealthChecks: ['base-route', 'auth-route'],
      missingHealthChecks: ['photos-protected-route'],
    },
    services: validArtifact().services.filter((service) => service.key !== 'photos'),
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  assert.match(errors.join('\n'), /simulationStatus\.status = incomplete, want pass/);
  assert.match(errors.join('\n'), /services missing observed key photos/);
});

test('validate-scenario-artifact rejects missing setup-action proof', () => {
  const artifact = validArtifact({
    simulationStatus: {
      status: 'pass',
      observedSetupActions: canonicalScenario().expected.simulation.setupActions.filter(
        (action) => action !== 'vaultwarden-admin-handoff',
      ),
      missingSetupActions: ['vaultwarden-admin-handoff'],
      observedHealthChecks: canonicalScenario().expected.simulation.healthChecks,
      missingHealthChecks: [],
    },
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  const message = errors.join('\n');
  assert.match(message, /simulationStatus\.observedSetupActions missing vaultwarden-admin-handoff/);
  assert.match(message, /simulationStatus\.missingSetupActions must be empty, got vaultwarden-admin-handoff/);
});

test('validate-scenario-artifact rejects missing selected-PaaS platform app evidence', () => {
  const artifact = validArtifact({
    platformApps: validArtifact().platformApps.filter((app) => app.name !== 'cloudreve'),
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  assert.match(errors.join('\n'), /platformApps missing managed platform app cloudreve/);
});

test('validate-scenario-artifact rejects fallback or unobserved platform app evidence', () => {
  const artifact = validArtifact({
    platformApps: validArtifact().platformApps.map((app) => {
      if (app.name !== 'immich') return app;
      return {
        ...app,
        externalId: 'local-compose:immich',
        observedStatus: 'exited:unhealthy',
      };
    }),
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  const message = errors.join('\n');
  assert.match(message, /platformApps\[immich\]\.externalId must be a selected-PaaS id/);
  assert.match(message, /platformApps\[immich\]\.observedStatus = exited:unhealthy/);
});

test('validate-scenario-artifact rejects access host and route drift', () => {
  const artifact = validArtifact({
    hubUrl: 'https://wrong-base.kombify.me',
    browserUrl: 'https://wrong-base.kombify.me',
    services: validArtifact().services.map((service) => {
      if (service.key === 'auth') {
        return { ...service, host: 'auth.sh-scenario-s2.kombify.me', url: 'https://auth.sh-scenario-s2.kombify.me' };
      }
      if (service.key === 'files') {
        return { ...service, url: 'https://sh-scenario-s2-files.kombify.me' };
      }
      return service;
    }),
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  const message = errors.join('\n');
  assert.match(message, /hubUrl = https:\/\/wrong-base\.kombify\.me, want https:\/\/sh-scenario-s2-base\.kombify\.me/);
  assert.match(message, /browserUrl = https:\/\/wrong-base\.kombify\.me, want https:\/\/sh-scenario-s2-base\.kombify\.me/);
  assert.match(message, /services\[auth\]\.host = auth\.sh-scenario-s2\.kombify\.me, want sh-scenario-s2-auth\.kombify\.me/);
  assert.match(message, /services\[files\]\.url path = \/, want \/stackkit\/files\/session/);
});

test('validate-scenario-artifact CLI exits nonzero for malformed artifact', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-scenario-artifact-'));
  const artifactPath = path.join(dir, 'homelab.json');
  await writeFile(artifactPath, JSON.stringify({
    ...validArtifact(),
    simulation: {
      setupActions: [],
      seededContent: [],
      healthChecks: [],
    },
  }, null, 2));

  await assert.rejects(
    execFileAsync(process.execPath, ['scripts/release/validate-scenario-artifact.mjs', artifactPath]),
    /simulation\.setupActions/,
  );
});

function canonicalScenario() {
  return {
    id: 'SK-S2',
    expected: {
      profile: {
        adminProfileKey: 'kombify-me-cloud-owner',
        domain: 'kombify.me',
        mailMode: 'cloud-owner',
        ownerMode: 'auto',
        ownerSource: 'cloud',
        paas: 'komodo',
        bootstrapMode: 'guided',
        demoDataEnabled: true,
      },
      generation: {
        paas: 'komodo',
        setupPolicies: {
          platform: 'automatic',
          applicationDefault: 'on_demand',
          vaultwarden: 'on_demand',
          immich: 'on_demand',
          files: 'on_demand',
        },
      },
      simulation: {
        setupActions: [
          'kuma-platform-bootstrap',
          'cloudreve-owner-bootstrap',
          'vaultwarden-admin-handoff',
          'immich-owner-bootstrap',
        ],
        seededContent: [],
        healthChecks: [
          'base-route',
          'komodo-route',
          'auth-route',
          'id-route',
          'vault-protected-route',
          'photos-protected-route',
          'files-protected-route',
        ],
      },
      access: {
        hubUrl: 'https://sh-scenario-s2-base.kombify.me',
        browserUrlMode: 'public',
        services: [
          { key: 'base', host: 'sh-scenario-s2-base.kombify.me', scheme: 'https' },
          { key: 'komodo', host: 'sh-scenario-s2-komodo.kombify.me', scheme: 'https' },
          { key: 'auth', host: 'sh-scenario-s2-auth.kombify.me', scheme: 'https' },
          { key: 'id', host: 'sh-scenario-s2-id.kombify.me', scheme: 'https' },
          { key: 'vault', host: 'sh-scenario-s2-vault.kombify.me', scheme: 'https' },
          { key: 'photos', host: 'sh-scenario-s2-photos.kombify.me', scheme: 'https' },
          { key: 'files', host: 'sh-scenario-s2-files.kombify.me', scheme: 'https', path: '/stackkit/files/session' },
        ],
      },
      target: {
        lane: 'techstack-lease',
        provisioner: 'kombify-techstack',
        runtime: 'managed-lease',
        allowedProviders: ['centron-managed', 'ionos-managed'],
        hostSource: 'techstack-lease-api',
      },
    },
  };
}

function validArtifact(overrides = {}) {
  return {
    scenarioId: 'SK-S2',
    scenarioName: 'Cloud Advanced kombify.me',
    runId: 'run-123',
    status: 'passed',
    hubUrl: 'https://sh-scenario-s2-base.kombify.me',
    browserUrl: 'https://sh-scenario-s2-base.kombify.me',
    profile: {
      adminProfileKey: 'kombify-me-cloud-owner',
      domain: 'kombify.me',
      mailMode: 'cloud-owner',
      ownerMode: 'auto',
      ownerSource: 'cloud',
      paas: 'komodo',
      bootstrapMode: 'guided',
      demoDataEnabled: true,
    },
    simulation: canonicalScenario().expected.simulation,
    simulationStatus: {
      status: 'pass',
      observedSetupActions: canonicalScenario().expected.simulation.setupActions,
      missingSetupActions: [],
      observedHealthChecks: canonicalScenario().expected.simulation.healthChecks,
      missingHealthChecks: [],
    },
    platformSystemApps: [
      {
        name: 'stackkit-hub',
        platform: 'komodo',
        management: 'managed',
        externalId: 'stackkit-hub-komodo-id',
        observedStatus: 'running',
        observedAt: '2026-06-13T08:00:00.000Z',
      },
      {
        name: 'stackkit-server',
        platform: 'komodo',
        management: 'managed',
        externalId: 'stackkit-server-komodo-id',
        observedStatus: 'running',
        observedAt: '2026-06-13T08:00:01.000Z',
      },
    ],
    platformApps: [
      {
        name: 'vaultwarden',
        platform: 'komodo',
        management: 'managed',
        externalId: 'vaultwarden-komodo-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-13T08:00:02.000Z',
        setupPolicy: 'on_demand',
      },
      {
        name: 'immich',
        platform: 'komodo',
        management: 'managed',
        externalId: 'immich-komodo-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-13T08:00:03.000Z',
        setupPolicy: 'on_demand',
      },
      {
        name: 'cloudreve',
        platform: 'komodo',
        management: 'managed',
        externalId: 'cloudreve-komodo-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-13T08:00:04.000Z',
        setupPolicy: 'on_demand',
      },
    ],
    services: [
      { key: 'base', url: 'https://sh-scenario-s2-base.kombify.me', host: 'sh-scenario-s2-base.kombify.me' },
      { key: 'komodo', url: 'https://sh-scenario-s2-komodo.kombify.me', host: 'sh-scenario-s2-komodo.kombify.me' },
      { key: 'auth', url: 'https://sh-scenario-s2-auth.kombify.me', host: 'sh-scenario-s2-auth.kombify.me' },
      { key: 'id', url: 'https://sh-scenario-s2-id.kombify.me', host: 'sh-scenario-s2-id.kombify.me' },
      { key: 'vault', url: 'https://sh-scenario-s2-vault.kombify.me', host: 'sh-scenario-s2-vault.kombify.me' },
      { key: 'photos', url: 'https://sh-scenario-s2-photos.kombify.me', host: 'sh-scenario-s2-photos.kombify.me' },
      { key: 'files', url: 'https://sh-scenario-s2-files.kombify.me/stackkit/files/session', host: 'sh-scenario-s2-files.kombify.me' },
    ],
    target: {
      publicIp: '203.0.113.10',
    },
    generatedAt: '2026-06-13T08:00:00.000Z',
    ...overrides,
  };
}

function canonicalSKS3Scenario() {
  return {
    id: 'SK-S3',
    name: 'Bare Custom Domain',
    stage: 'gated-live',
    expected: {
      profile: {
        adminProfileKey: 'custom-domain-explicit-mail',
        domain: 'kombify.pro',
        mailMode: 'explicit',
        ownerMode: 'custom',
        ownerSource: 'local',
        paas: 'coolify',
        bootstrapMode: 'minimal',
        demoDataEnabled: false,
      },
      generation: {
        paas: 'coolify',
        setupPolicies: {
          platform: 'manual',
          applicationDefault: 'manual',
          vaultwarden: 'manual',
          immich: 'manual',
          files: 'manual',
        },
      },
      simulation: {
        setupActions: [],
        seededContent: [],
        healthChecks: ['coolify-route', 'auth-route', 'id-route', 'vault-route', 'photos-route', 'files-route'],
      },
      access: {
        browserUrlMode: 'public',
        services: [
          { key: 'auth', host: 'auth.kombify.pro', scheme: 'https' },
          { key: 'id', host: 'id.kombify.pro', scheme: 'https' },
          { key: 'coolify', host: 'coolify.kombify.pro', scheme: 'https' },
          { key: 'vault', host: 'vault.kombify.pro', scheme: 'https' },
          { key: 'photos', host: 'photos.kombify.pro', scheme: 'https' },
          { key: 'files', host: 'files.kombify.pro', scheme: 'https', path: '/stackkit/files/session' },
        ],
      },
      target: {
        lane: 'provider-lease',
        provisioner: 'kombify-sim-provider',
        runtime: 'managed-lease',
        allowedProviders: ['centron-managed', 'ionos-managed'],
        hostSource: 'simulation-lease-api',
      },
    },
  };
}

function validSKS3Artifact(overrides = {}) {
  const domain = 'e2e-cd-1782104835.kombify.pro';
  return {
    scenarioId: 'SK-S3',
    scenarioName: 'Bare Custom Domain',
    runId: 'run-123',
    status: 'passed',
    hubUrl: '',
    browserUrl: `https://auth.${domain}`,
    profile: {
      adminProfileKey: 'custom-domain-explicit-mail',
      domain,
      mailMode: 'explicit',
      ownerMode: 'custom',
      ownerSource: 'local',
      paas: 'coolify',
      bootstrapMode: 'minimal',
      demoDataEnabled: false,
    },
    simulation: canonicalSKS3Scenario().expected.simulation,
    simulationStatus: {
      status: 'pass',
      observedSetupActions: [],
      missingSetupActions: [],
      observedHealthChecks: canonicalSKS3Scenario().expected.simulation.healthChecks,
      missingHealthChecks: [],
    },
    services: [
      { key: 'auth', url: `https://auth.${domain}`, host: `auth.${domain}` },
      { key: 'id', url: `https://id.${domain}`, host: `id.${domain}` },
      { key: 'coolify', url: `https://coolify.${domain}`, host: `coolify.${domain}` },
      { key: 'vault', url: `https://vault.${domain}`, host: `vault.${domain}` },
      { key: 'photos', url: `https://photos.${domain}`, host: `photos.${domain}` },
      { key: 'files', url: `https://files.${domain}/stackkit/files/session`, host: `files.${domain}` },
    ],
    target: {
      provider: 'centron-managed',
      publicIp: '203.0.113.11',
    },
    generatedAt: '2026-06-22T08:00:00.000Z',
    ...overrides,
  };
}

function canonicalSKS5Scenario() {
  return {
    id: 'SK-S5',
    name: 'Missing Mail Contract',
    stage: 'negative',
    expected: {
      failure: {
        nonInteractive: true,
        messageContains: 'owner/admin email is required',
      },
      profile: {
        adminProfileKey: 'no-owner-byos',
        domain: 'kombify.pro',
        mailMode: 'none',
        ownerMode: 'none',
        bootstrapMode: 'full_auto',
        demoDataEnabled: true,
      },
      simulation: {
        setupActions: [],
        seededContent: [],
        healthChecks: [],
      },
    },
  };
}

function validSKS5Artifact(overrides = {}) {
  return {
    scenarioId: 'SK-S5',
    scenarioName: 'Missing Mail Contract',
    runId: 'run-123',
    status: 'passed',
    generatedAt: '2026-06-21T08:00:00.000Z',
    profile: canonicalSKS5Scenario().expected.profile,
    simulation: canonicalSKS5Scenario().expected.simulation,
    negativeGuard: {
      nonInteractive: true,
      messageContains: 'owner/admin email is required',
    },
    ...overrides,
  };
}
