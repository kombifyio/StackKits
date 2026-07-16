import assert from 'node:assert/strict';
import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { validate } from './check-timeout-budget.mjs';

async function fixture(files) {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-timeout-budget-'));
  for (const [relativePath, content] of Object.entries(files)) {
    const fullPath = path.join(root, relativePath);
    await mkdir(path.dirname(fullPath), { recursive: true });
    await writeFile(fullPath, content);
  }
  return root;
}

test('timeout budget accepts fifteen minute gates', async () => {
  const root = await fixture({
    '.github/workflows/ci.yml': 'jobs:\n  test:\n    timeout-minutes: 15\n    steps:\n      - run: go test ./... -timeout 15m\n',
    'scripts/public/workflows/release.yml': 'jobs:\n  release:\n    timeout-minutes: 10\n',
    'tests/production/README.md': 'go test -timeout 14m ./tests/production\n',
    'basement-kit/templates/simple/main.tf': 'for i in $(seq 1 60); do sleep 5; done\n',
  });
  assert.deepEqual(validate(root), []);
});

test('timeout budget honors justified exempt marker per workflow file', async () => {
  const root = await fixture({
    '.github/workflows/os-matrix.yml':
      '# timeout-budget: exempt -- vm-matrix runs full rollouts on the self-hosted lab runner\njobs:\n  vm:\n    timeout-minutes: 300\n    steps:\n      - run: go test ./tests/production -timeout 280m\n',
    '.github/workflows/ci.yml': 'jobs:\n  test:\n    timeout-minutes: 120\n',
  });
  const failures = validate(root);
  // The exempt file passes; the non-exempt file still fails.
  assert.equal(failures.length, 1);
  assert.match(failures[0], /ci\.yml sets timeout-minutes: 120/);
});

test('timeout budget rejects exempt marker without a reason', async () => {
  const root = await fixture({
    '.github/workflows/os-matrix.yml':
      '# timeout-budget: exempt\njobs:\n  vm:\n    timeout-minutes: 300\n',
  });
  const failures = validate(root);
  assert.equal(failures.length, 2);
  assert.match(failures.join('\n'), /without a reason/);
  assert.match(failures.join('\n'), /timeout-minutes: 300/);
});

test('timeout budget rejects long workflow and readiness waits', async () => {
  const root = await fixture({
    '.github/workflows/production-tests.yml': 'jobs:\n  live:\n    timeout-minutes: 120\n    steps:\n      - run: go test ./tests/production -timeout 110m\n',
    'tests/production/README.md': 'go test -timeout 90m ./tests/production\n',
    'basement-kit/templates/simple/main.tf': 'for i in $(seq 1 360); do sleep 5; done\necho "after 30 minutes"\n',
  });
  const failures = validate(root);
  assert.equal(failures.length, 5);
  assert.match(failures.join('\n'), /timeout-minutes: 120/);
  assert.match(failures.join('\n'), /-timeout 110m/);
  assert.match(failures.join('\n'), /seq 1 360/);
  assert.match(failures.join('\n'), /30 minute wait/);
});
