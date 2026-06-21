import assert from 'node:assert/strict';
import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { validate } from './check-node24-actions.mjs';

async function fixture(files) {
  const root = await mkdtemp(path.join(tmpdir(), 'stackkits-node24-actions-'));
  for (const [relativePath, content] of Object.entries(files)) {
    const fullPath = path.join(root, relativePath);
    await mkdir(path.dirname(fullPath), { recursive: true });
    await writeFile(fullPath, content);
  }
  return root;
}

test('Node 24 action guard accepts StackKits workflow baselines', async () => {
  const root = await fixture({
    '.github/workflows/release.yml': `
env:
  NODE_VERSION: "24"
jobs:
  release:
    steps:
      - uses: actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6.0.3
      - uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c # v6.4.0
      - uses: actions/setup-node@48b55a011bda9f5d6aeb4c2d9c7362e8dae4041e # v6.4.0
        with:
          node-version: 24
      - uses: goreleaser/goreleaser-action@5daf1e915a5f0af01ddbcd89a43b8061ff4f1a89 # v7.2.2
      - uses: actions/upload-artifact@v7
      - uses: actions/download-artifact@v8
      - uses: actions/attest-build-provenance@a2bbfa25375fe432b6a289bc6b6cd05ecd0c4c32 # v4.1.0
`,
    'scripts/public/workflows/ci.yml': `
jobs:
  release-evidence:
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-node@v6
        with:
          node-version: \${{ env.NODE_VERSION }}
`,
  });

  assert.deepEqual(validate(root), []);
});

test('Node 24 action guard rejects old action majors and Node 20 runtime pins', async () => {
  const root = await fixture({
    '.github/workflows/release.yml': `
env:
  NODE_VERSION: "20"
jobs:
  release:
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@0123456789abcdef0123456789abcdef01234567
        with:
          node-version: 20
      - uses: goreleaser/goreleaser-action@abcdefabcdefabcdefabcdefabcdefabcdefabcd # v6.4.0
      - uses: actions/upload-artifact@v6
      - uses: actions/download-artifact@v7
      - uses: actions/attest-build-provenance@fedcbafedcbafedcbafedcbafedcbafedcbafedc # v3.0.0
`,
  });

  const failures = validate(root);
  assert.equal(failures.length, 8);
  assert.match(failures.join('\n'), /NODE_VERSION to 20/);
  assert.match(failures.join('\n'), /actions\/checkout@v4/);
  assert.match(failures.join('\n'), /without a trailing # vX\.Y\.Z comment/);
  assert.match(failures.join('\n'), /node-version to 20/);
  assert.match(failures.join('\n'), /goreleaser\/goreleaser-action@abcdef/);
  assert.match(failures.join('\n'), /actions\/upload-artifact@v6/);
  assert.match(failures.join('\n'), /actions\/download-artifact@v7/);
  assert.match(failures.join('\n'), /actions\/attest-build-provenance@fedcba/);
});
