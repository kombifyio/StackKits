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
    'base-kit/templates/simple/main.tf': 'for i in $(seq 1 60); do sleep 5; done\n',
  });
  assert.deepEqual(validate(root), []);
});

test('timeout budget rejects long workflow and readiness waits', async () => {
  const root = await fixture({
    '.github/workflows/production-tests.yml': 'jobs:\n  live:\n    timeout-minutes: 120\n    steps:\n      - run: go test ./tests/production -timeout 110m\n',
    'tests/production/README.md': 'go test -timeout 90m ./tests/production\n',
    'base-kit/templates/simple/main.tf': 'for i in $(seq 1 360); do sleep 5; done\necho "after 30 minutes"\n',
  });
  const failures = validate(root);
  assert.equal(failures.length, 5);
  assert.match(failures.join('\n'), /timeout-minutes: 120/);
  assert.match(failures.join('\n'), /-timeout 110m/);
  assert.match(failures.join('\n'), /seq 1 360/);
  assert.match(failures.join('\n'), /30 minute wait/);
});
