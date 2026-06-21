import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { deflateSync } from 'node:zlib';
import test from 'node:test';
import assert from 'node:assert/strict';

const execFileAsync = promisify(execFile);
const BROWSER_EVIDENCE_RUN_ID = 'v04-browser-proof';

test('render-release-evidence writes artifact hashes and checks', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  await writeFile(path.join(dist, 'stackkits_0.0.1_linux_amd64.tar.gz'), 'archive');
  await writeFile(path.join(dist, 'stackkits_0.0.1_linux_amd64.tar.gz.spdx.json'), '{"SPDXID":"SPDXRef-DOCUMENT"}');

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--visibility',
    'internal',
    '--dist',
    dist,
    '--output',
    output,
    '--check',
    'publicExport=pass,exported tree passed leak checks',
    '--check',
    'archiveValidation=pass',
    '--scenario-evidence',
    'SK-S1=pass,local no-mail Coolify release archive smoke passed',
    '--pending-gate',
    'SK-S2 kombify.me cloud-owner Komodo release evidence is still pending',
    '--missing-alternative',
    'Photos alternative is not accepted for v0.4 beta',
    '--known-limitation',
    'Operator-supplied limitation',
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.schemaVersion, '1.0.0');
  assert.equal(evidence.release.tag, 'v0.0.1');
  assert.equal(evidence.checks.publicExport.status, 'pass');
  assert.equal(evidence.checks.archiveValidation.status, 'pass');
  assert.equal(evidence.checks.liveInstallerSmoke.status, 'pending');
  assert.equal(evidence.checks.browserPreflight.status, 'pending');
  assert.equal(evidence.checks.browserEvidence.status, 'pending');
  assert.equal(evidence.checks.defaultL3PaaSDelivery.status, 'pending');
  const scenarioByID = new Map(evidence.scenarioEvidence.map((item) => [item.scenarioId, item]));
  assert.equal(scenarioByID.get('SK-S1').status, 'pass');
  assert.equal(scenarioByID.get('SK-S2').status, 'pending');
  assert.equal(scenarioByID.get('SK-S3').status, 'pending');
  assert.equal(scenarioByID.get('SK-S5').status, 'pending');
  assert.deepEqual(evidence.pendingGates, [
    'SK-S2 kombify.me cloud-owner Komodo release evidence is still pending',
    'SK-S3 custom-domain explicit-owner Coolify beta scenario is pending released-archive evidence.',
    'SK-S5 missing-mail negative scenario is pending released-archive evidence.',
  ]);
  assert.deepEqual(evidence.missingAlternatives, [
    'Photos alternative is not accepted for v0.4 beta',
    'Vault has no accepted v0.4 alternative yet; Vaultwarden remains the beta default and the gap is release-blocking unless documented as a beta limitation.',
  ]);
  assert.deepEqual(evidence.knownLimitations, [
    'Operator-supplied limitation',
    'v0.4 browser evidence still must prove PocketID/passkey Owner login, TinyAuth ForwardAuth session acceptance, and default L3 app content; Immich StackKit demo photo and Cloudreve StackKit Demo/README.txt need live browser proof.',
  ]);
  assert.equal(evidence.artifacts.length, 2);
  assert.ok(evidence.artifacts.some((artifact) => artifact.kind === 'archive' && artifact.sha256.length === 64));
  assert.ok(evidence.artifacts.some((artifact) => artifact.kind === 'sbom'));
});

test('publish-oss renders public release evidence without private publisher URLs', async () => {
  const workflow = await readFile('.github/workflows/publish-oss.yml', 'utf8');
  const start = workflow.indexOf('node scripts/release/render-release-evidence.mjs');
  assert.notEqual(start, -1);
  const end = workflow.indexOf('shopt -s nullglob', start);
  assert.notEqual(end, -1);
  const renderBlock = workflow.slice(start, end);

  assert.match(renderBlock, /--source-repo "\$\{OSS_REPO\}"/);
  assert.match(renderBlock, /--release-repo "\$\{OSS_REPO\}"/);
  assert.match(renderBlock, /--visibility public/);
  assert.doesNotMatch(renderBlock, /--workflow-run-url/);
  assert.doesNotMatch(renderBlock, /GITHUB_REPOSITORY/);

  const attestationSummary = workflow.match(/--summary "([^"]+)"/)?.[1] || '';
  assert.match(attestationSummary, /\$\{OSS_REPO\}/);
  assert.doesNotMatch(attestationSummary, /GITHUB_REPOSITORY/);
  assert.doesNotMatch(attestationSummary, /GITHUB_RUN_ID/);
});

test('publish-oss can import production scenario evidence artifacts', async () => {
  const workflow = await readFile('.github/workflows/publish-oss.yml', 'utf8');

  assert.match(workflow, /scenario_evidence_run_id:/);
  assert.match(workflow, /Download scenario evidence artifacts/);
  assert.match(workflow, /stackkit-SK-S1-released-homelab/);
  assert.match(workflow, /stackkit-SK-S2-homelab/);
  assert.match(workflow, /stackkit-SK-S3-homelab/);
  assert.match(workflow, /stackkit-SK-S5-homelab/);
  assert.match(workflow, /artifacts\/scenarios\/\$\{scenario_id\}\/homelab\.json/);
});

test('public release workflow marks evidence as public', async () => {
  const workflow = await readFile('scripts/public/workflows/release.yml', 'utf8');
  const start = workflow.indexOf('node scripts/release/render-release-evidence.mjs');
  assert.notEqual(start, -1);
  const end = workflow.indexOf('--check "attestationVerification', start);
  assert.notEqual(end, -1);
  const renderBlock = workflow.slice(start, end);

  assert.match(renderBlock, /--source-repo "kombifyio\/stackKits"/);
  assert.match(renderBlock, /--release-repo "kombifyio\/stackKits"/);
  assert.match(renderBlock, /--visibility public/);
});

test('render-release-evidence emits canonical pending scenario rows when artifacts are absent', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-missing-scenarios-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.deepEqual(
    evidence.scenarioEvidence.map((item) => [item.scenarioId, item.status]),
    [
      ['SK-S1', 'pending'],
      ['SK-S2', 'pending'],
      ['SK-S3', 'pending'],
      ['SK-S5', 'pending'],
    ],
  );
  for (const scenarioId of ['SK-S1', 'SK-S2', 'SK-S3', 'SK-S5']) {
    assert.ok(
      evidence.pendingGates.some((gate) => gate.includes(scenarioId)),
      `pending gates should mention ${scenarioId}`,
    );
  }
});

test('render-release-evidence imports passing homelab scenario artifact', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-scenario-artifact-pass-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const artifactPath = path.join(dir, 'homelab.json');
  await writeFile(artifactPath, JSON.stringify(homelabArtifact()));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--scenario-artifact',
    artifactPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  const scenario = evidence.scenarioEvidence.find((item) => item.scenarioId === 'SK-S2');
  assert.equal(scenario.status, 'pass');
  assert.equal(scenario.url, artifactPath);
  assert.match(scenario.summary, /2 observed simulation health checks/);
  assert.match(scenario.summary, /4 observed setup actions/);
  assert.match(scenario.summary, /5 platform app refs/);
  assert.deepEqual(
    evidence.scenarioEvidence.map((item) => [item.scenarioId, item.status]),
    [
      ['SK-S1', 'pending'],
      ['SK-S2', 'pass'],
      ['SK-S3', 'pending'],
      ['SK-S5', 'pending'],
    ],
  );
  assert.ok(!evidence.pendingGates.some((gate) => gate.includes('SK-S2')));
});

test('render-release-evidence fails incomplete homelab simulation status', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-scenario-artifact-fail-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const artifactPath = path.join(dir, 'homelab.json');
  await writeFile(
    artifactPath,
    JSON.stringify(
      homelabArtifact({
        simulationStatus: {
          status: 'incomplete',
          observedSetupActions: homelabSetupActions(),
          missingSetupActions: [],
          observedHealthChecks: ['base-route'],
          missingHealthChecks: ['komodo-route'],
        },
      }),
    ),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--scenario-artifact',
    artifactPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  const scenario = evidence.scenarioEvidence.find((item) => item.scenarioId === 'SK-S2');
  assert.equal(scenario.status, 'fail');
  assert.match(scenario.summary, /simulationStatus is incomplete/);
  assert.match(scenario.summary, /missingHealthChecks=komodo-route/);
  assert.ok(evidence.pendingGates.some((gate) => gate.includes('SK-S2') && gate.includes('fail')));
});

test('render-release-evidence fails missing homelab setup-action proof', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-scenario-artifact-setup-fail-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const artifactPath = path.join(dir, 'homelab.json');
  await writeFile(
    artifactPath,
    JSON.stringify(
      homelabArtifact({
        simulationStatus: {
          status: 'pass',
          observedSetupActions: homelabSetupActions().filter((action) => action !== 'vaultwarden-admin-handoff'),
          missingSetupActions: ['vaultwarden-admin-handoff'],
          observedHealthChecks: ['base-route', 'komodo-route'],
          missingHealthChecks: [],
        },
      }),
    ),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--scenario-artifact',
    artifactPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  const scenario = evidence.scenarioEvidence.find((item) => item.scenarioId === 'SK-S2');
  assert.equal(scenario.status, 'fail');
  assert.match(scenario.summary, /observedSetupActions missing vaultwarden-admin-handoff/);
  assert.match(scenario.summary, /missingSetupActions=vaultwarden-admin-handoff/);
  assert.ok(evidence.pendingGates.some((gate) => gate.includes('SK-S2') && gate.includes('fail')));
});

test('render-release-evidence attaches passing browser preflight report', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-pass-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  await writeFile(preflightPath, JSON.stringify(browserPreflightReport('pass')));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'pass');
  assert.equal(evidence.checks.browserPreflight.url, preflightPath);
  assert.match(evidence.checks.browserPreflight.summary, /playwright-chromium launch prerequisites are ready/);
  assert.ok(evidence.scenarioEvidence.some((item) => item.scenarioId === 'SK-S1-browser-preflight' && item.status === 'pass'));
});

test('render-release-evidence attaches failed browser preflight report', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-fail-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  await writeFile(preflightPath, JSON.stringify(browserPreflightReport('fail')));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'fail');
  assert.match(evidence.checks.browserPreflight.summary, /failedChecks=Docker Desktop availability, Playwright Chromium availability/);
  assert.match(evidence.checks.browserPreflight.summary, /nativeCommands=Docker Desktop availability \(docker\) class=exit_nonzero timeout=60s/);
  assert.ok(evidence.scenarioEvidence.some((item) => item.scenarioId === 'SK-S1-browser-preflight' && item.status === 'fail'));
});

test('render-release-evidence surfaces Windows sandbox browser preflight host issue', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-host-issue-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const report = browserPreflightReport('fail');
  const dockerCheck = report.checks.find((check) => check.name === 'Docker Desktop availability');
  dockerCheck.error = "Docker Desktop availability failed to start 'docker': windows sandbox: runner error: CreateProcessAsUserW failed: 5";
  dockerCheck.nativeCommand.failureClass = 'start_failed';
  delete dockerCheck.nativeCommand.exitCode;
  dockerCheck.nativeCommand.hostIssue = 'windows-createprocessasuser-access-denied';
  await writeFile(preflightPath, JSON.stringify(report));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'fail');
  assert.match(evidence.checks.browserPreflight.summary, /class=start_failed hostIssue=windows-createprocessasuser-access-denied timeout=60s/);
});

test('render-release-evidence rejects invalid browser preflight failedChecks', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-invalid-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const report = browserPreflightReport('fail');
  report.failedChecks = ['Docker Desktop availability'];
  await writeFile(preflightPath, JSON.stringify(report));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'fail');
  assert.match(evidence.checks.browserPreflight.summary, /failedChecks = \[Docker Desktop availability\]/);
  assert.match(evidence.checks.browserPreflight.summary, /want \[Docker Desktop availability, Playwright Chromium availability\]/);
});

test('render-release-evidence allows Chromium install skip for installed browser channel preflight', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-msedge-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const report = browserPreflightReport('pass');
  report.browserChannel = 'msedge';
  skipBrowserPreflightCheck(report.checks, 'Install isolated Playwright Chromium');
  setBrowserPreflightCheckOutput(report.checks, 'Playwright Chromium availability', 'browser-channel=msedge');
  await writeFile(preflightPath, JSON.stringify(report));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'pass');
  assert.match(evidence.checks.browserPreflight.summary, /msedge launch prerequisites are ready/);
});

test('render-release-evidence rejects skipped critical browser preflight checks', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-skipped-critical-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const report = browserPreflightReport('pass');
  report.browserChannel = 'msedge';
  skipBrowserPreflightCheck(report.checks, 'Docker Desktop availability');
  await writeFile(preflightPath, JSON.stringify(report));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'fail');
  assert.match(evidence.checks.browserPreflight.summary, /Docker Desktop availability is skipped/);
});

test('render-release-evidence accepts browser preflight with default engine context', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-default-context-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const report = browserPreflightReport('pass');
  const defaultContextCheck = report.checks.find((item) => item.name === 'Docker Desktop context');
  defaultContextCheck.evidence.output = 'default';
  await writeFile(preflightPath, JSON.stringify(report));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'pass');
});

test('render-release-evidence rejects browser preflight without Docker Desktop context evidence', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-context-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const report = browserPreflightReport('pass');
  const contextCheck = report.checks.find((item) => item.name === 'Docker Desktop context');
  contextCheck.evidence.output = 'desktop-windows';
  await writeFile(preflightPath, JSON.stringify(report));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'fail');
  assert.match(evidence.checks.browserPreflight.summary, /Docker Desktop context output desktop-windows, want desktop-linux or default/);
});

test('render-release-evidence rejects browser preflight with mismatched Playwright launch channel evidence', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-preflight-launch-channel-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const report = browserPreflightReport('pass');
  report.browserChannel = 'msedge';
  skipBrowserPreflightCheck(report.checks, 'Install isolated Playwright Chromium');
  setBrowserPreflightCheckOutput(report.checks, 'Playwright Chromium availability', 'chromium=available');
  await writeFile(preflightPath, JSON.stringify(report));

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'fail');
  assert.match(evidence.checks.browserPreflight.summary, /Playwright Chromium availability output chromium=available, want browser-channel=msedge/);
});

test('render-release-evidence attaches passing browser evidence manifest', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'pass');
  assert.equal(evidence.checks.browserEvidence.url, browserEvidencePath);
  assert.ok(evidence.scenarioEvidence.some((item) => item.scenarioId === 'SK-S1-browser' && item.status === 'pass'));
  assert.ok(!evidence.knownLimitations.some((value) => value.includes('browser evidence still must prove')));
});

test('render-release-evidence rejects browser evidence from a non-Base Hub URL', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-url-drift-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://modern.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        { ...check('files-demo-content'), url: 'http://files.home.localhost/' },
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /browserUrl host is modern\.home\.localhost, want base\.home\.localhost/);
  assert.match(evidence.checks.browserEvidence.summary, /files-demo-content: url path is \/, want \/stackkit\/files\/session/);
});

test('render-release-evidence rejects browser screenshot URL route drift', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-screenshot-url-drift-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        { ...screenshot('photos-demo-content'), url: 'http://media.home.localhost/photos' },
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(
    evidence.checks.browserEvidence.summary,
    /photos-demo-content screenshot artifacts\/scenarios\/SK-S1\/screenshots\/photos-demo-content\.png: url host is media\.home\.localhost, want photos\.home\.localhost/,
  );
});

test('render-release-evidence rejects browser evidence without browser runtime diagnostics', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-runtime-missing-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  const diagnostics = browserDiagnostics();
  delete diagnostics.browser;
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics,
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /missing browser runtime diagnostics/);
});

test('render-release-evidence rejects browser evidence without runId', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-run-missing-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  const manifest = browserEvidenceReport({ runId: '' });
  await writeFile(browserEvidencePath, JSON.stringify(manifest));
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /runId must be recorded/);
});

test('render-release-evidence rejects browser evidence without generatedAt', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-generated-at-missing-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  const manifest = browserEvidenceReport({ generatedAt: '' });
  await writeFile(browserEvidencePath, JSON.stringify(manifest));
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /generatedAt must be RFC3339/);
});

test('render-release-evidence surfaces wrapper browser evidence failure', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-wrapper-fail-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'fail',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      ownerUsername: 'owner',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      error: 'SK-S1 browser screenshot capture failed with exit code 1',
      failurePhase: 'wrapper',
      checks: [],
      screenshots: [],
      diagnostics: {
        wrapper: {
          phase: 'wrapper',
          evidenceRoot: dir,
          preflightReportPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'browser-evidence-preflight.json'),
          homelabPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'homelab.json'),
          nativeCommand: {
            name: 'SK-S1 browser screenshot capture',
            filePath: 'node',
            arguments: ['scripts/evidence/capture-basekit-browser-evidence.mjs'],
            timeoutSeconds: 840,
            failureClass: 'exit_nonzero',
            exitCode: 1,
          },
        },
      },
    }),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /failed during wrapper/);
  assert.match(evidence.checks.browserEvidence.summary, /error: SK-S1 browser screenshot capture failed with exit code 1/);
  assert.match(evidence.checks.browserEvidence.summary, /nativeCommand=SK-S1 browser screenshot capture \(node\) class=exit_nonzero timeout=840s/);
  assert.doesNotMatch(evidence.checks.browserEvidence.summary, /missing check pocketid-owner-passkey/);
  assert.doesNotMatch(evidence.checks.browserEvidence.summary, /SetupRun diagnostics/);
});

test('render-release-evidence surfaces Windows sandbox browser failure host issue', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-wrapper-host-issue-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'fail',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      ownerUsername: 'owner',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      error: "SK-S1 browser screenshot capture failed to start 'node': windows sandbox: runner error: CreateProcessAsUserW failed: 5",
      failurePhase: 'browser-capture',
      checks: [],
      screenshots: [],
      diagnostics: {
        wrapper: {
          phase: 'browser-capture',
          evidenceRoot: dir,
          preflightReportPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'browser-evidence-preflight.json'),
          homelabPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'homelab.json'),
          nativeCommand: {
            name: 'SK-S1 browser screenshot capture',
            filePath: 'node',
            arguments: ['scripts/evidence/capture-basekit-browser-evidence.mjs'],
            timeoutSeconds: 840,
            failureClass: 'start_failed',
            hostIssue: 'windows-createprocessasuser-access-denied',
          },
        },
      },
    }),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /failed during browser-capture/);
  assert.match(
    evidence.checks.browserEvidence.summary,
    /nativeCommand=SK-S1 browser screenshot capture \(node\) class=start_failed hostIssue=windows-createprocessasuser-access-denied timeout=840s/,
  );
  assert.doesNotMatch(evidence.checks.browserEvidence.summary, /missing check pocketid-owner-passkey/);
});

test('render-release-evidence rejects browser failure native command diagnostics with env values', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-wrapper-native-env-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'fail',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      ownerUsername: 'owner',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      error: 'Docker Desktop context failed with exit code 1',
      failurePhase: 'browser-preflight',
      checks: [],
      screenshots: [],
      diagnostics: {
        wrapper: {
          phase: 'browser-preflight',
          evidenceRoot: dir,
          preflightReportPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'browser-evidence-preflight.json'),
          homelabPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'homelab.json'),
          nativeCommand: {
            name: 'Docker Desktop context',
            filePath: 'docker',
            arguments: ['context', 'show'],
            timeoutSeconds: 60,
            env: {
              STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON: 'must-not-be-serialized',
            },
          },
        },
      },
    }),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /failure manifest is incomplete or invalid/);
  assert.match(evidence.checks.browserEvidence.summary, /wrapper nativeCommand must not include environment values/);
});

test('render-release-evidence rejects browser failure native command diagnostics with unknown failure class', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-wrapper-native-class-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'fail',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      ownerUsername: 'owner',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      error: 'Docker Desktop context failed with exit code 1',
      failurePhase: 'browser-preflight',
      checks: [],
      screenshots: [],
      diagnostics: {
        wrapper: {
          phase: 'browser-preflight',
          evidenceRoot: dir,
          preflightReportPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'browser-evidence-preflight.json'),
          homelabPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'homelab.json'),
          nativeCommand: {
            name: 'Docker Desktop context',
            filePath: 'docker',
            arguments: ['context', 'show'],
            timeoutSeconds: 60,
            failureClass: 'manual-debug',
          },
        },
      },
    }),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /wrapper nativeCommand failureClass is manual-debug/);
});

test('render-release-evidence rejects invalid browser failure manifest without a cause', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-fail-invalid-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'fail',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      ownerUsername: 'owner',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      checks: [],
      screenshots: [],
    }),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /failure manifest is incomplete or invalid/);
  assert.match(evidence.checks.browserEvidence.summary, /must include error or at least one failed check/);
  assert.match(evidence.checks.browserEvidence.summary, /failurePhase must be recorded when checks are empty/);
  assert.doesNotMatch(evidence.checks.browserEvidence.summary, /missing check pocketid-owner-passkey/);
});

test('render-release-evidence rejects unknown browser failure phase', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-fail-phase-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'fail',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      ownerUsername: 'owner',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      error: 'Manual browser evidence debug failed',
      failurePhase: 'manual-debug',
      checks: [],
      screenshots: [],
      diagnostics: {
        wrapper: {
          phase: 'manual-debug',
          evidenceRoot: dir,
          preflightReportPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'browser-evidence-preflight.json'),
          homelabPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'homelab.json'),
        },
      },
    }),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /failurePhase is manual-debug/);
  assert.match(evidence.checks.browserEvidence.summary, /browser-capture/);
  assert.doesNotMatch(evidence.checks.browserEvidence.summary, /missing check pocketid-owner-passkey/);
});

test('render-release-evidence rejects browser failure evidence from a different run than preflight', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-failure-run-mismatch-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const preflight = browserPreflightReport('pass');
  preflight.evidenceRoot = dir;
  await writeFile(preflightPath, JSON.stringify(preflight));
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: 'other-browser-run',
      status: 'fail',
      generatedAt: '2026-05-28T12:00:00.000Z',
      ownerEmail: 'owner@example.com',
      ownerUsername: 'owner',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      error: 'SK-S1 browser screenshot capture failed with exit code 1',
      failurePhase: 'wrapper',
      checks: [],
      screenshots: [],
      diagnostics: {
        wrapper: {
          phase: 'wrapper',
          evidenceRoot: dir,
          preflightReportPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'browser-evidence-preflight.json'),
          homelabPath: path.join(dir, 'artifacts', 'scenarios', 'SK-S1', 'homelab.json'),
        },
      },
    }),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'pass');
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(
    evidence.checks.browserEvidence.summary,
    /browser evidence runId other-browser-run does not match preflight runId v04-browser-proof/,
  );
  assert.doesNotMatch(evidence.checks.browserEvidence.summary, /missing check pocketid-owner-passkey/);
});

test('render-release-evidence rejects browser evidence with a different browser channel than preflight', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-channel-mismatch-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const preflight = browserPreflightReport('pass');
  preflight.browserChannel = 'msedge';
  preflight.evidenceRoot = dir;
  skipBrowserPreflightCheck(preflight.checks, 'Install isolated Playwright Chromium');
  setBrowserPreflightCheckOutput(preflight.checks, 'Playwright Chromium availability', 'browser-channel=msedge');
  await writeFile(preflightPath, JSON.stringify(preflight));
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'pass');
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserPreflight.summary, /msedge launch prerequisites are ready/);
  assert.match(
    evidence.checks.browserEvidence.summary,
    /browserChannel playwright-chromium does not match preflight browserChannel msedge/,
  );
  assert.ok(evidence.scenarioEvidence.some((item) => item.scenarioId === 'SK-S1-browser' && item.status === 'fail'));
});

test('render-release-evidence rejects browser evidence from a different run than preflight', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-run-mismatch-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const preflight = browserPreflightReport('pass');
  preflight.evidenceRoot = dir;
  await writeFile(preflightPath, JSON.stringify(preflight));
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(browserEvidencePath, JSON.stringify(browserEvidenceReport({ runId: 'other-browser-run' })));
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'pass');
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(
    evidence.checks.browserEvidence.summary,
    /browser evidence runId other-browser-run does not match preflight runId v04-browser-proof/,
  );
});

test('render-release-evidence rejects browser evidence from a different evidence root than preflight', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-root-mismatch-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const preflightPath = path.join(dir, 'browser-evidence-preflight.json');
  const preflight = browserPreflightReport('pass');
  preflight.evidenceRoot = path.join(dir, 'other-run');
  await writeFile(preflightPath, JSON.stringify(preflight));
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-preflight',
    preflightPath,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserPreflight.status, 'pass');
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /does not match preflight evidenceRoot/);
});

test('render-release-evidence marks incomplete browser evidence as failed', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-fail-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [check('pocketid-owner-passkey')],
      screenshots: [screenshot('pocketid-owner-passkey')],
    }),
  );
  await writeScreenshotFiles(dir, ['pocketid-owner-passkey']);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /missing check/);
  assert.ok(evidence.knownLimitations.some((value) => value.includes('browser evidence still must prove')));
});

test('render-release-evidence rejects browser evidence when screenshot files are missing', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-missing-screenshot-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /file missing/);
});

test('render-release-evidence rejects browser evidence with tiny screenshots', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-tiny-screenshot-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ], { width: 1, height: 1 });
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /dimensions = 1x1, want at least 320x240/);
});

test('render-release-evidence rejects browser evidence with truncated PNG placeholders', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-truncated-screenshot-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ], { content: pngHeaderOnly(320, 240) });
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /PNG missing IDAT image data/);
});

test('render-release-evidence rejects browser evidence without seeded app content proof', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-missing-content-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content', {}),
        check('files-demo-content', { demoContent: 'cloudreve' }),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /Immich seeded demo asset evidence/);
  assert.match(evidence.checks.browserEvidence.summary, /StackKit Demo\/README.txt/);
});

test('render-release-evidence rejects Photos evidence for the wrong Owner or non-StackKit demo asset', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-wrong-photos-owner-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content', {
          ...defaultBrowserEvidenceForCheck('photos-demo-content'),
          immichOwnerEmail: 'someone@example.com',
          demoAssetDeviceAssetId: 'personal-photo',
        }),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /immichOwnerEmail someone@example.com must match Owner owner@example.com/);
});

test('render-release-evidence rejects Vault evidence without anonymous TinyAuth boundary proof', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-vault-boundary-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary', {
          verification: 'anonymous-vault-route-check',
          authBoundary: 'tinyauth-pocketid',
          anonymousAccess: 'rejected',
          anonymousStatus: '200',
          anonymousUrl: 'http://vault.home.localhost',
          anonymousBoundarySignal: 'vaultwarden-login',
        }),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /anonymous Vault boundary signal vaultwarden-login/);
});

test('render-release-evidence rejects browser evidence without WebAuthn passkey credential proof', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-passkey-credential-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey', {
          verification: 'page-text-only',
          passkeyCredentials: '0',
        }),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /WebAuthn passkey credential evidence/);
});

test('render-release-evidence rejects browser evidence without TinyAuth ForwardAuth session proof', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-tinyauth-session-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session', {
          verification: 'page-text-only',
          authBoundary: 'tinyauth-pocketid',
          sessionCookieCount: '0',
          forwardAuthStatus: '302',
        }),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /TinyAuth\/PocketID ForwardAuth Owner-session evidence/);
});

test('render-release-evidence rejects browser evidence with stale setup-state artifact', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-stale-setup-state-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir, { immichStatus: 'waiting', immichPhase: 'owner_activated' });

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /immich-owner-bootstrap status is waiting, want completed/);
});

test('render-release-evidence rejects browser evidence with invalid owner or timing contract', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-invalid-contract-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  const checks = [
    check('pocketid-owner-passkey'),
    check('tinyauth-owner-session'),
    check('photos-demo-content'),
    check('files-demo-content'),
    check('vault-auth-boundary'),
  ];
  checks[2].durationSeconds = 901;
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner',
      ownerUsername: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'ftp://base.home.localhost',
      diagnostics: browserDiagnostics(),
      checks,
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /ownerEmail must be email-shaped/);
  assert.match(evidence.checks.browserEvidence.summary, /ownerUsername must be a username/);
  assert.match(evidence.checks.browserEvidence.summary, /browserUrl scheme is ftp, want http or https/);
  assert.match(evidence.checks.browserEvidence.summary, /durationSeconds 901 exceeds 15 minute budget/);
});

test('render-release-evidence rejects browser evidence without owner setup action proof', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-missing-setup-actions-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  const diagnostics = browserDiagnostics();
  diagnostics.setupActions = diagnostics.setupActions.filter((action) => action.service !== 'vault');
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics,
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /setupActions missing owner-activated service vault/);
});

test('render-release-evidence rejects owner setup action proof without expected drop', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-browser-missing-action-drop-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  const diagnostics = browserDiagnostics();
  diagnostics.setupActions.find((action) => action.service === 'photos').dropStatus = 'waiting';
  diagnostics.setupActions.find((action) => action.service === 'photos').dropPhase = 'owner_activated';
  await writeFile(
    browserEvidencePath,
    JSON.stringify({
      scenarioId: 'SK-S1',
      runId: BROWSER_EVIDENCE_RUN_ID,
      status: 'pass',
      ownerEmail: 'owner@example.com',
      browserChannel: 'playwright-chromium',
      browserUrl: 'http://base.home.localhost',
      diagnostics,
      checks: [
        check('pocketid-owner-passkey'),
        check('tinyauth-owner-session'),
        check('photos-demo-content'),
        check('files-demo-content'),
        check('vault-auth-boundary'),
      ],
      screenshots: [
        screenshot('pocketid-owner-passkey'),
        screenshot('tinyauth-owner-session'),
        screenshot('photos-demo-content'),
        screenshot('files-demo-content'),
        screenshot('vault-auth-boundary'),
      ],
    }),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /setupAction photos dropStatus is waiting, want completed/);
});

test('render-release-evidence rejects Vaultwarden Owner provisioning claims in setup-state', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-vault-owner-claim-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  const browserEvidencePath = path.join(dir, 'browser-evidence.json');
  await writeFile(
    browserEvidencePath,
    JSON.stringify(browserEvidenceReport()),
  );
  await writeScreenshotFiles(dir, [
    'pocketid-owner-passkey',
    'tinyauth-owner-session',
    'photos-demo-content',
    'files-demo-content',
    'vault-auth-boundary',
  ]);
  await writeSetupStateFile(dir);
  const setupStatePath = path.join(dir, 'artifacts/scenarios/SK-S1/setup-state.yaml');
  const setupState = await readFile(setupStatePath, 'utf8');
  await writeFile(
    setupStatePath,
    setupState.replace(
      '    outerAuthBoundary: tinyauth-pocketid\n- runId: setup-photos',
      '    outerAuthBoundary: tinyauth-pocketid\n    appLocalOwner: pocketid-owner-preprovisioned\n- runId: setup-photos',
    ),
  );

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
    '--browser-evidence',
    browserEvidencePath,
    '--browser-evidence-root',
    dir,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.equal(evidence.checks.browserEvidence.status, 'fail');
  assert.match(evidence.checks.browserEvidence.summary, /vaultwarden-admin-handoff evidence\[appLocalOwner\] must be absent/);
});

test('render-release-evidence keeps required v0.4 missing alternatives by default', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-required-alternatives-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);

  const output = path.join(dist, 'release-evidence.json');
  await execFileAsync(process.execPath, [
    'scripts/release/render-release-evidence.mjs',
    '--tag',
    'v0.0.1',
    '--commit',
    'abcdef123456',
    '--source-repo',
    'kombifyio/stackKits',
    '--release-repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--output',
    output,
  ]);

  const evidence = JSON.parse(await readFile(output, 'utf8'));
  assert.deepEqual(evidence.missingAlternatives, [
    'Photos has no accepted v0.4 alternative yet; Immich remains the beta default and the gap is release-blocking unless documented as a beta limitation.',
    'Vault has no accepted v0.4 alternative yet; Vaultwarden remains the beta default and the gap is release-blocking unless documented as a beta limitation.',
  ]);
  assert.deepEqual(evidence.knownLimitations, [
    'v0.4 is a BaseKit beta-hardening release and does not claim production readiness.',
    'Unreleased kit definitions remain out of v0.4 scope.',
    'Dokploy remains draft/non-beta until its full bootstrap path has evidence.',
    'v0.4 browser evidence still must prove PocketID/passkey Owner login, TinyAuth ForwardAuth session acceptance, and default L3 app content; Immich StackKit demo photo and Cloudreve StackKit Demo/README.txt need live browser proof.',
  ]);
});

function check(name, evidence = defaultBrowserEvidenceForCheck(name)) {
  const result = {
    name,
    status: 'pass',
    url: browserCheckURL(name),
    expectedText: name,
    observedText: name,
    screenshot: `artifacts/scenarios/SK-S1/screenshots/${name}.png`,
    durationSeconds: 1,
  };
  if (evidence) result.evidence = evidence;
  return result;
}

function screenshot(name) {
  return {
    name,
    path: `artifacts/scenarios/SK-S1/screenshots/${name}.png`,
    url: browserCheckURL(name),
  };
}

function browserCheckURL(name) {
  switch (name) {
    case 'pocketid-owner-passkey':
      return 'http://id.home.localhost/setup';
    case 'tinyauth-owner-session':
      return 'http://auth.home.localhost';
    case 'photos-demo-content':
      return 'http://photos.home.localhost/photos';
    case 'files-demo-content':
      return 'http://files.home.localhost/stackkit/files/session';
    case 'vault-auth-boundary':
      return 'http://vault.home.localhost';
    default:
      return 'http://base.home.localhost';
  }
}

function browserDiagnostics() {
  const drops = {};
  for (const [dropName, serviceKey] of [
    ['kuma-platform-bootstrap', 'kuma'],
    ['cloudreve-owner-bootstrap', 'files'],
    ['vaultwarden-admin-handoff', 'vault'],
    ['immich-owner-bootstrap', 'photos'],
  ]) {
    drops[dropName] = {
      runId: `setup-${serviceKey}`,
      status: 'completed',
      phase: 'verified',
      serviceKey,
      attempts: '1',
      lastRequested: '2026-05-28T12:00:00Z',
      lastStarted: '2026-05-28T12:00:01Z',
      lastFinished: '2026-05-28T12:00:02Z',
      logCount: '1',
      rollbackNoteCount: '1',
      evidence: setupRunEvidenceForDrop(dropName),
    };
  }
  return {
    browser: {
      channel: 'playwright-chromium',
      requestedChannel: 'playwright-chromium',
      headless: 'true',
      viewport: '1440x1000',
      userAgent: 'Mozilla/5.0 HeadlessChrome',
      browserVersion: '120.0.0.0',
      webAuthnVirtualAuthenticator: 'enabled',
    },
    setupState: {
      status: 'present',
      sourcePath: 'artifacts/scenarios/SK-S1/setup-state.yaml',
      setupRunCount: '4',
      drops,
    },
    setupActions: [
      setupAction('photos'),
      setupAction('files'),
      setupAction('vault'),
    ],
  };
}

function setupAction(service) {
  const dropNames = {
    photos: 'immich-owner-bootstrap',
    files: 'cloudreve-owner-bootstrap',
    vault: 'vaultwarden-admin-handoff',
  };
  return {
    service,
    httpStatus: '200',
    ok: 'true',
    durationSeconds: '2',
    runId: `setup-${service}`,
    attempts: '1',
    status: 'completed',
    dropName: dropNames[service],
    dropStatus: 'completed',
    dropPhase: 'verified',
    lastRequested: '2026-05-28T12:00:00Z',
    lastStarted: '2026-05-28T12:00:01Z',
    lastFinished: '2026-05-28T12:00:02Z',
    logCount: '3',
    rollbackNoteCount: '1',
    message: 'Setup completed.',
  };
}

function setupRunEvidenceForDrop(dropName) {
  if (dropName === 'cloudreve-owner-bootstrap') {
    return {
      credentialRole: 'technical-admin-bootstrap',
      appLocalAccount: 'stackkit-admin-created',
      demoData: 'seeded-when-enabled',
      outerAuthBoundary: 'tinyauth-pocketid',
      ownerLogin: 'pocketid-passkey',
      identityBridge: 'stackkit-cloudreve-local-session',
      appLocalSessionHandoff: 'stackkit-session-bridge-prepared',
      readyToUseContentStatus: 'pending-browser-evidence',
    };
  }
  if (dropName === 'vaultwarden-admin-handoff') {
    return {
      credentialRole: 'break-glass-admin-token',
      ownerLogin: 'pocketid-passkey',
      adminTokenPosture: 'verified-break-glass',
      adminTokenStorage: 'argon2id-phc-runtime',
      appLocalSignups: 'disabled',
      plaintextAdminTokenEnv: 'absent',
      outerAuthBoundary: 'tinyauth-pocketid',
    };
  }
  if (dropName === 'immich-owner-bootstrap') {
    return {
      credentialRole: 'technical-admin-bootstrap',
      technicalAdmin: 'stackkit-admin-created',
      appLocalOwner: 'pocketid-owner-preprovisioned',
      ownerEmail: 'owner@example.com',
      ownerProvisioning: 'created-pocketid-owner',
      demoData: 'seeded-when-enabled',
      outerAuthBoundary: 'tinyauth-pocketid',
      ownerLogin: 'pocketid-passkey',
      pocketidOAuth: 'enabled',
      oidcClientId: 'stackkit-immich',
      oidcIssuer: 'http://id.home.localhost',
      autoRegister: 'false',
      autoLaunch: 'true',
      appLocalSessionHandoff: 'oidc-email-link-prepared',
    };
  }
  return {};
}

function homelabArtifact(overrides = {}) {
  return {
    scenarioId: 'SK-S2',
    scenarioName: 'Cloud Advanced kombify.me',
    runId: 'run-123',
    status: 'passed',
    hubUrl: 'https://sh-scenario-s2-base.kombify.me',
    browserUrl: 'https://sh-scenario-s2-base.kombify.me',
    simulation: {
      setupActions: homelabSetupActions(),
      healthChecks: ['base-route', 'komodo-route'],
    },
    simulationStatus: {
      status: 'pass',
      observedSetupActions: homelabSetupActions(),
      missingSetupActions: [],
      observedHealthChecks: ['base-route', 'komodo-route'],
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
      {
        key: 'base',
        url: 'https://sh-scenario-s2-base.kombify.me',
        host: 'sh-scenario-s2-base.kombify.me',
      },
      {
        key: 'komodo',
        url: 'https://komodo.sh-scenario-s2.kombify.me',
        host: 'komodo.sh-scenario-s2.kombify.me',
      },
    ],
    target: {
      publicIp: '203.0.113.10',
    },
    generatedAt: '2026-06-13T08:00:00.000Z',
    ...overrides,
  };
}

function homelabSetupActions() {
  return [
    'kuma-platform-bootstrap',
    'cloudreve-owner-bootstrap',
    'vaultwarden-admin-handoff',
    'immich-owner-bootstrap',
  ];
}

function browserEvidenceReport(overrides = {}) {
  return {
    scenarioId: 'SK-S1',
    runId: BROWSER_EVIDENCE_RUN_ID,
    status: 'pass',
    generatedAt: '2026-05-28T12:00:00.000Z',
    ownerEmail: 'owner@example.com',
    browserChannel: 'playwright-chromium',
    browserUrl: 'http://base.home.localhost',
    diagnostics: browserDiagnostics(),
    checks: [
      check('pocketid-owner-passkey'),
      check('tinyauth-owner-session'),
      check('photos-demo-content'),
      check('files-demo-content'),
      check('vault-auth-boundary'),
    ],
    screenshots: [
      screenshot('pocketid-owner-passkey'),
      screenshot('tinyauth-owner-session'),
      screenshot('photos-demo-content'),
      screenshot('files-demo-content'),
      screenshot('vault-auth-boundary'),
    ],
    ...overrides,
  };
}

function browserPreflightReport(status) {
  const checks = browserPreflightChecks().map((name) => ({
    name,
    status: 'pass',
    timeoutSeconds: name.startsWith('Required command:') ? 0 : 60,
    evidence: browserPreflightEvidenceForCheck(name),
  }));
  if (status === 'fail') {
    failBrowserPreflightCheck(checks, 'Docker Desktop availability', 'docker daemon unavailable');
    failBrowserPreflightCheck(checks, 'Playwright Chromium availability', 'chromium launch failed');
  }
  const report = {
    scenarioId: 'SK-S1',
    runId: BROWSER_EVIDENCE_RUN_ID,
    kind: 'browser-evidence-preflight',
    status,
    generatedAt: '2026-05-28T12:00:00.000Z',
    evidenceRoot: 'D:/Github/kombify/kombify-StackKits',
    playwrightModuleDir: 'D:/Github/kombify/kombify-StackKits/.stackkit/tools/browser-evidence',
    browserChannel: 'playwright-chromium',
    phaseTimeoutSeconds: 14 * 60,
    checks,
  };
  if (status === 'fail') {
    report.failedChecks = ['Docker Desktop availability', 'Playwright Chromium availability'];
    report.error = 'BaseKit browser evidence preflight failed: Docker Desktop availability: docker daemon unavailable; Playwright Chromium availability: chromium launch failed';
  }
  return report;
}

function browserPreflightChecks() {
  return [
    'Required command: go',
    'Required command: node',
    'Required command: npm',
    'Required command: docker',
    'Docker Desktop availability',
    'Docker Desktop context',
    'Install isolated Playwright package',
    'Install isolated Playwright Chromium',
    'Playwright package availability',
    'Playwright Chromium availability',
  ];
}

function browserPreflightEvidenceForCheck(name) {
  if (name.startsWith('Required command:')) {
    return { source: `C:/tools/${name.slice('Required command:'.length).trim()}.exe` };
  }
  if (name === 'Docker Desktop context') {
    return { output: 'desktop-linux', expected: 'desktop-linux' };
  }
  if (name === 'Playwright package availability') {
    return { output: 'playwright=available' };
  }
  if (name === 'Playwright Chromium availability') {
    return { output: 'chromium=available' };
  }
  return {};
}

function setBrowserPreflightCheckOutput(checks, name, output) {
  const check = checks.find((item) => item.name === name);
  check.evidence = check.evidence && typeof check.evidence === 'object' ? check.evidence : {};
  check.evidence.output = output;
}

function failBrowserPreflightCheck(checks, name, error) {
  const check = checks.find((item) => item.name === name);
  check.status = 'fail';
  check.error = error;
  const command = name.startsWith('Docker Desktop') ? 'docker' : 'node';
  check.nativeCommand = {
    name,
    filePath: command,
    arguments: name.startsWith('Docker Desktop') ? ['version'] : ['-e', 'playwright launch check'],
    timeoutSeconds: 60,
    failureClass: 'exit_nonzero',
    exitCode: 1,
  };
}

function skipBrowserPreflightCheck(checks, name) {
  const check = checks.find((item) => item.name === name);
  check.status = 'skipped';
  delete check.error;
}

function defaultBrowserEvidenceForCheck(name) {
  if (name === 'pocketid-owner-passkey') {
    return {
      verification: 'webauthn-virtual-authenticator',
      authenticatorProtocol: 'ctap2',
      authenticatorTransport: 'usb',
      passkeyCredentials: '1',
      residentCredentials: '1',
      relyingParties: 'id.home.localhost',
    };
  }
  if (name === 'tinyauth-owner-session') {
    return {
      verification: 'tinyauth-forwardauth-session',
      authBoundary: 'tinyauth-pocketid',
      ownerSessionSignal: 'forwardauth-2xx',
      sessionUrl: 'http://auth.home.localhost',
      authUrl: 'http://auth.home.localhost',
      forwardAuthEndpoint: 'http://auth.home.localhost/api/auth/traefik',
      forwardAuthStatus: '200',
      sessionCookieCount: '1',
      sessionCookieNames: 'tinyauth',
      sessionCookieDomains: '.home.localhost',
    };
  }
  if (name === 'photos-demo-content') {
    return {
      demoContent: 'immich-demo-assets',
      immichDemoAssets: '1',
      verification: 'immich-search-metadata',
      ownerVerification: 'immich-users-me',
      immichOwnerEmail: 'owner@example.com',
      immichOwnerId: 'immich-owner-id',
      demoAssetDeviceId: 'stackkit-demo',
      demoAssetDeviceAssetId: 'stackkit-demo-photo-1',
      demoAssetFile: 'stackkit-demo-photo.png',
    };
  }
  if (name === 'files-demo-content') {
    return {
      demoContent: 'cloudreve-demo-file',
      seededFolder: 'StackKit Demo',
      seededFile: 'README.txt',
      verification: 'cloudreve-browser-session-api',
      identityBridge: 'stackkit-cloudreve-local-session',
      bridgeVerification: 'stackkit-cloudreve-session-bridge',
      bridgeCurrentUser: 'owner-user',
      cloudreveSessionUser: 'owner-user',
    };
  }
  if (name === 'vault-auth-boundary') {
    return {
      verification: 'anonymous-vault-route-check',
      authBoundary: 'tinyauth-pocketid',
      anonymousAccess: 'rejected',
      anonymousStatus: '200',
      anonymousUrl: 'http://auth.home.localhost/login',
      anonymousBoundarySignal: 'tinyauth',
    };
  }
  return null;
}

async function writeScreenshotFiles(root, names, options = {}) {
  const width = options.width || 320;
  const height = options.height || 240;
  const content = options.content || pngFixture(width, height);
  for (const name of names) {
    const file = path.join(root, 'artifacts', 'scenarios', 'SK-S1', 'screenshots', `${name}.png`);
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, content);
  }
}

function pngFixture(width, height) {
  const row = Buffer.alloc(1 + width * 3);
  const rawRows = [];
  for (let y = 0; y < height; y += 1) {
    rawRows.push(row);
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    pngChunk('IHDR', pngIHDR(width, height)),
    pngChunk('IDAT', deflateSync(Buffer.concat(rawRows))),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
}

function pngHeaderOnly(width, height) {
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    pngChunk('IHDR', pngIHDR(width, height)),
  ]);
}

function pngIHDR(width, height) {
  const data = Buffer.alloc(13);
  data.writeUInt32BE(width, 0);
  data.writeUInt32BE(height, 4);
  data[8] = 8;
  data[9] = 2;
  return data;
}

function pngChunk(type, data) {
  const typeBuffer = Buffer.from(type, 'ascii');
  const header = Buffer.alloc(8);
  header.writeUInt32BE(data.length, 0);
  typeBuffer.copy(header, 4);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(Buffer.concat([typeBuffer, data])), 0);
  return Buffer.concat([header, data, crc]);
}

function crc32(buffer) {
  let crc = 0xffffffff;
  for (const byte of buffer) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) {
      crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

async function writeSetupStateFile(root, options = {}) {
  const immichStatus = options.immichStatus || 'completed';
  const immichPhase = options.immichPhase || 'verified';
  const file = path.join(root, 'artifacts', 'scenarios', 'SK-S1', 'setup-state.yaml');
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(
    file,
    [
      'setupRuns:',
      '- runId: setup-kuma',
      '  serviceKey: kuma',
      '  appName: uptime-kuma',
      '  dropName: kuma-platform-bootstrap',
      '  status: completed',
      '  phase: verified',
      '  attempts: 1',
      '  lastRequested: 2026-05-28T12:00:00Z',
      '  lastStarted: 2026-05-28T12:00:01Z',
      '  lastFinished: 2026-05-28T12:00:02Z',
      '  evidence:',
      '    status: nested-value-that-must-not-be-read',
      '  logs:',
      '  - timestamp: 2026-05-28T12:00:02Z',
      '    phase: verified',
      '    level: info',
      '    message: Setup drop completed.',
      '  rollbackNotes:',
      '  - Re-run the setup drop after removing generated demo state if ownership changes.',
      '- runId: setup-files',
      '  serviceKey: files',
      '  appName: cloudreve',
      '  dropName: cloudreve-owner-bootstrap',
      '  status: completed',
      '  phase: verified',
      '  attempts: 1',
      '  lastRequested: 2026-05-28T12:00:00Z',
      '  lastStarted: 2026-05-28T12:00:01Z',
      '  lastFinished: 2026-05-28T12:00:02Z',
      '  logs:',
      '  - timestamp: 2026-05-28T12:00:02Z',
      '    phase: verified',
      '    level: info',
      '    message: Setup drop completed.',
      '  rollbackNotes:',
      '  - Re-run the setup drop after removing generated demo state if ownership changes.',
      '  evidence:',
      '    credentialRole: technical-admin-bootstrap',
      '    appLocalAccount: stackkit-admin-created',
      '    demoData: seeded-when-enabled',
      '    outerAuthBoundary: tinyauth-pocketid',
      '    ownerLogin: pocketid-passkey',
      '    identityBridge: stackkit-cloudreve-local-session',
      '    appLocalSessionHandoff: stackkit-session-bridge-prepared',
      '    readyToUseContentStatus: pending-browser-evidence',
      '- runId: setup-vault',
      '  serviceKey: vault',
      '  appName: vaultwarden',
      '  dropName: vaultwarden-admin-handoff',
      '  status: completed',
      '  phase: verified',
      '  attempts: 1',
      '  lastRequested: 2026-05-28T12:00:00Z',
      '  lastStarted: 2026-05-28T12:00:01Z',
      '  lastFinished: 2026-05-28T12:00:02Z',
      '  logs:',
      '  - timestamp: 2026-05-28T12:00:02Z',
      '    phase: verified',
      '    level: info',
      '    message: Setup drop completed.',
      '  rollbackNotes:',
      '  - Re-run the setup drop after removing generated demo state if ownership changes.',
      '  evidence:',
      '    credentialRole: break-glass-admin-token',
      '    ownerLogin: pocketid-passkey',
      '    adminTokenPosture: verified-break-glass',
      '    adminTokenStorage: argon2id-phc-runtime',
      '    appLocalSignups: disabled',
      '    plaintextAdminTokenEnv: absent',
      '    outerAuthBoundary: tinyauth-pocketid',
      '- runId: setup-photos',
      '  serviceKey: photos',
      '  appName: immich',
      '  dropName: immich-owner-bootstrap',
      `  status: ${immichStatus}`,
      `  phase: ${immichPhase}`,
      '  attempts: 1',
      '  lastRequested: 2026-05-28T12:00:00Z',
      '  lastStarted: 2026-05-28T12:00:01Z',
      '  lastFinished: 2026-05-28T12:00:02Z',
      '  logs:',
      '  - timestamp: 2026-05-28T12:00:02Z',
      `    phase: ${immichPhase}`,
      '    level: info',
      '    message: Setup drop completed.',
      '  rollbackNotes:',
      '  - Re-run the setup drop after removing generated demo state if ownership changes.',
      '  evidence:',
      '    credentialRole: technical-admin-bootstrap',
      '    technicalAdmin: stackkit-admin-created',
      '    appLocalOwner: pocketid-owner-preprovisioned',
      '    ownerEmail: owner@example.com',
      '    ownerProvisioning: created-pocketid-owner',
      '    demoData: seeded-when-enabled',
      '    outerAuthBoundary: tinyauth-pocketid',
      '    ownerLogin: pocketid-passkey',
      '    pocketidOAuth: enabled',
      '    oidcClientId: stackkit-immich',
      '    oidcIssuer: http://id.home.localhost',
      '    autoRegister: false',
      '    autoLaunch: true',
      '    appLocalSessionHandoff: oidc-email-link-prepared',
      '',
    ].join('\n'),
  );
}

test('update-release-evidence-check updates one check without rebuilding artifacts', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-evidence-update-'));
  const evidencePath = path.join(dir, 'release-evidence.json');
  await writeFile(
    evidencePath,
    JSON.stringify({
      schemaVersion: '1.0.0',
      generatedAt: '2026-05-18T00:00:00.000Z',
      release: {
        tag: 'v0.0.1',
        commit: 'abcdef123456',
        sourceRepository: 'kombifyio/stackKits',
        releaseRepository: 'kombifyio/stackKits',
        visibility: 'internal',
      },
      artifacts: [{ name: 'archive.tar.gz', kind: 'archive', sha256: 'a'.repeat(64), sizeBytes: 7 }],
      checks: { attestationVerification: { status: 'pending' } },
      scenarioEvidence: [],
      pendingGates: ['SK-S1 pending'],
      knownLimitations: ['BaseKit only'],
      missingAlternatives: ['Photos alternative pending'],
    }),
  );

  await execFileAsync(process.execPath, [
    'scripts/release/update-release-evidence-check.mjs',
    '--file',
    evidencePath,
    '--name',
    'attestationVerification',
    '--status',
    'pass',
    '--summary',
    'verified',
  ]);

  const evidence = JSON.parse(await readFile(evidencePath, 'utf8'));
  assert.equal(evidence.checks.attestationVerification.status, 'pass');
  assert.equal(evidence.checks.attestationVerification.summary, 'verified');
  assert.equal(evidence.artifacts.length, 1);
});
