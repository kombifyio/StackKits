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

test('validate-scenario-artifact accepts SK-S2 run-scoped kombify.me hosts with on-demand setup drops', () => {
  const errors = [];
  validateScenarioArtifact(errors, validDynamicSKS2Artifact(), canonicalScenario());
  assert.deepEqual(errors, []);
});

test('validate-scenario-artifact rejects rollout artifacts without security baseline evidence', () => {
  const artifact = validArtifact({ securityBaseline: undefined });
  delete artifact.securityBaseline;
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  assert.match(errors.join('\n'), /securityBaseline must be present/);
});

test('validate-scenario-artifact rejects failed security baseline evidence', () => {
  const artifact = validArtifact({
    securityBaseline: {
      ...validSecurityBaseline(),
      controls: {
        ...validSecurityBaseline().controls,
        sshPasswordAuthentication: 'enabled',
      },
    },
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  assert.match(errors.join('\n'), /securityBaseline\.controls\.sshPasswordAuthentication = enabled, want disabled/);
});

test('validate-scenario-artifact rejects security baseline evidence without measured public-beta metadata', () => {
  const artifact = validArtifact({
    securityBaseline: {
      ...validSecurityBaseline(),
      schemaVersion: '',
      mode: '',
      appliedAt: 'not-a-time',
    },
  });
  const errors = [];
  validateScenarioArtifact(errors, artifact, canonicalScenario());
  assert.match(errors.join('\n'), /securityBaseline\.schemaVersion = <missing>, want stackkit\.security-baseline\/v1/);
  assert.match(errors.join('\n'), /securityBaseline\.mode = <missing>, want public-beta/);
  assert.match(errors.join('\n'), /securityBaseline\.appliedAt must be RFC3339/);
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
    securityBaseline: validSecurityBaseline(),
    generatedAt: '2026-06-13T08:00:00.000Z',
    ...overrides,
  };
}

function validSecurityBaseline(overrides = {}) {
  return {
    schemaVersion: 'stackkit.security-baseline/v1',
    status: 'pass',
    mode: 'public-beta',
    appliedAt: '2026-06-22T08:00:00Z',
    controls: {
      firewall: 'enabled',
      sshPasswordAuthentication: 'disabled',
      sshRootLogin: 'key-only',
      fail2ban: 'enabled',
      unattendedUpgrades: 'security',
      sysctl: 'applied',
    },
    ...overrides,
  };
}

function validDynamicSKS2Artifact() {
  const prefix = 'sh-my-homelab-1fd1d2';
  const setupActions = canonicalScenario().expected.simulation.setupActions;
  const onDemandSetupActions = setupActions.filter((action) => action !== 'kuma-platform-bootstrap');
  return validArtifact({
    hubUrl: `https://${prefix}-base.kombify.me`,
    browserUrl: `https://${prefix}-base.kombify.me`,
    simulationStatus: {
      status: 'incomplete',
      observedSetupActions: ['kuma-platform-bootstrap'],
      missingSetupActions: onDemandSetupActions,
      observedHealthChecks: canonicalScenario().expected.simulation.healthChecks,
      missingHealthChecks: [],
    },
    platformApps: [
      {
        name: 'vaultwarden',
        platform: 'komodo',
        management: 'managed',
        externalId: 'vaultwarden-komodo-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-13T08:00:02.000Z',
        setupPolicy: 'on_demand',
        setupDrops: [{ name: 'vaultwarden-admin-handoff' }],
      },
      {
        name: 'immich',
        platform: 'komodo',
        management: 'managed',
        externalId: 'immich-komodo-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-13T08:00:03.000Z',
        setupPolicy: 'on_demand',
        setupDrops: [{ name: 'immich-owner-bootstrap' }],
      },
      {
        name: 'cloudreve',
        platform: 'komodo',
        management: 'managed',
        externalId: 'cloudreve-komodo-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-13T08:00:04.000Z',
        setupPolicy: 'on_demand',
        setupDrops: [{ name: 'cloudreve-owner-bootstrap' }],
      },
    ],
    services: [
      { key: 'base', url: `https://${prefix}-base.kombify.me`, host: `${prefix}-base.kombify.me` },
      { key: 'komodo', url: `https://${prefix}-komodo.kombify.me`, host: `${prefix}-komodo.kombify.me` },
      { key: 'auth', url: `https://${prefix}-auth.kombify.me`, host: `${prefix}-auth.kombify.me` },
      { key: 'id', url: `https://${prefix}-id.kombify.me`, host: `${prefix}-id.kombify.me` },
      { key: 'vault', url: `https://${prefix}-vault.kombify.me`, host: `${prefix}-vault.kombify.me` },
      { key: 'photos', url: `https://${prefix}-photos.kombify.me`, host: `${prefix}-photos.kombify.me` },
      { key: 'files', url: `https://${prefix}-files.kombify.me/stackkit/files/session`, host: `${prefix}-files.kombify.me` },
    ],
  });
}

function canonicalSKS3Scenario() {
  return {
    id: 'SK-S3',
    name: 'Bootstrapped Custom Domain',
    stage: 'gated-live',
    expected: {
      profile: {
        adminProfileKey: 'custom-domain-explicit-mail',
        domain: 'kombify.pro',
        mailMode: 'explicit',
        ownerMode: 'custom',
        ownerSource: 'local',
        paas: 'coolify',
        bootstrapMode: 'bootstrapped',
        demoDataEnabled: false,
      },
      generation: {
        paas: 'coolify',
        setupPolicies: {
          platform: 'automatic',
          applicationDefault: 'on_demand',
          kuma: 'automatic',
          whoami: 'automatic',
          vaultwarden: 'on_demand',
          immich: 'on_demand',
          files: 'on_demand',
        },
      },
      simulation: {
        setupActions: [],
        seededContent: [],
        healthChecks: ['base-route', 'auth-route', 'id-route', 'coolify-route', 'vault-route', 'photos-route', 'files-route'],
      },
      access: {
        hubUrl: 'https://base.kombify.pro',
        browserUrlMode: 'public',
        services: [
          { key: 'base', host: 'base.kombify.pro', scheme: 'https' },
          { key: 'home', host: 'home.kombify.pro', scheme: 'https' },
          { key: 'auth', host: 'auth.kombify.pro', scheme: 'https' },
          { key: 'id', host: 'id.kombify.pro', scheme: 'https' },
          { key: 'coolify', host: 'coolify.kombify.pro', scheme: 'https' },
          { key: 'kuma', host: 'kuma.kombify.pro', scheme: 'https' },
          { key: 'whoami', host: 'whoami.kombify.pro', scheme: 'https' },
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
    scenarioName: 'Bootstrapped Custom Domain',
    runId: 'run-123',
    status: 'passed',
    hubUrl: `https://base.${domain}`,
    browserUrl: `https://base.${domain}`,
    profile: {
      adminProfileKey: 'custom-domain-explicit-mail',
      domain,
      mailMode: 'explicit',
      ownerMode: 'custom',
      ownerSource: 'local',
      paas: 'coolify',
      bootstrapMode: 'bootstrapped',
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
    platformSystemApps: [
      {
        name: 'stackkit-hub',
        platform: 'coolify',
        management: 'managed',
        externalId: 'stackkit-hub-coolify-id',
        observedStatus: 'running',
        observedAt: '2026-06-22T08:00:00.000Z',
      },
      {
        name: 'stackkit-server',
        platform: 'coolify',
        management: 'managed',
        externalId: 'stackkit-server-coolify-id',
        observedStatus: 'running',
        observedAt: '2026-06-22T08:00:01.000Z',
      },
      {
        name: 'uptime-kuma',
        platform: 'coolify',
        management: 'managed',
        externalId: 'uptime-kuma-coolify-id',
        observedStatus: 'running',
        observedAt: '2026-06-22T08:00:02.000Z',
      },
      {
        name: 'whoami',
        platform: 'coolify',
        management: 'managed',
        externalId: 'whoami-coolify-id',
        observedStatus: 'running',
        observedAt: '2026-06-22T08:00:03.000Z',
      },
    ],
    platformApps: [
      {
        name: 'vaultwarden',
        platform: 'coolify',
        management: 'managed',
        externalId: 'vaultwarden-coolify-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-22T08:00:03.000Z',
        setupPolicy: 'on_demand',
      },
      {
        name: 'immich',
        platform: 'coolify',
        management: 'managed',
        externalId: 'immich-coolify-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-22T08:00:04.000Z',
        setupPolicy: 'on_demand',
      },
      {
        name: 'cloudreve',
        platform: 'coolify',
        management: 'managed',
        externalId: 'cloudreve-coolify-id',
        observedStatus: 'deploy:accepted',
        observedAt: '2026-06-22T08:00:05.000Z',
        setupPolicy: 'on_demand',
      },
    ],
    services: [
      { key: 'base', url: `https://base.${domain}`, host: `base.${domain}` },
      { key: 'home', url: `https://home.${domain}`, host: `home.${domain}` },
      { key: 'auth', url: `https://auth.${domain}`, host: `auth.${domain}` },
      { key: 'id', url: `https://id.${domain}`, host: `id.${domain}` },
      { key: 'coolify', url: `https://coolify.${domain}`, host: `coolify.${domain}` },
      { key: 'kuma', url: `https://kuma.${domain}`, host: `kuma.${domain}` },
      { key: 'whoami', url: `https://whoami.${domain}`, host: `whoami.${domain}` },
      { key: 'vault', url: `https://vault.${domain}`, host: `vault.${domain}` },
      { key: 'photos', url: `https://photos.${domain}`, host: `photos.${domain}` },
      { key: 'files', url: `https://files.${domain}/stackkit/files/session`, host: `files.${domain}` },
    ],
    target: {
      provider: 'centron-managed',
      publicIp: '203.0.113.11',
    },
    securityBaseline: validSecurityBaseline(),
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
