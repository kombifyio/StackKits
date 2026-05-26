import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import test from 'node:test';
import assert from 'node:assert/strict';

const execFileAsync = promisify(execFile);

test('verify-release-attestations dry-run lists release subjects', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'stackkits-attest-'));
  const dist = path.join(dir, 'dist');
  await mkdir(dist);
  await writeFile(path.join(dist, 'stackkits_0.0.1_linux_amd64.tar.gz'), 'archive');
  await writeFile(path.join(dist, 'kombify-stackkits_0.0.1_linux_amd64.deb'), 'package');
  await writeFile(path.join(dist, 'checksums.txt'), 'checksum');
  await writeFile(path.join(dist, 'stackkits_0.0.1_linux_amd64.tar.gz.spdx.json'), '{}');
  await writeFile(path.join(dist, 'release-evidence.json'), '{}');
  await writeFile(path.join(dist, 'notes.txt'), 'ignore');

  const { stdout } = await execFileAsync(process.execPath, [
    'scripts/release/verify-release-attestations.mjs',
    '--repo',
    'kombifyio/stackKits',
    '--dist',
    dist,
    '--include-evidence',
    '--image',
    'ghcr.io/kombifyio/stackkits:v0.0.1',
    '--dry-run',
  ]);

  assert.match(stdout, /file:.*stackkits_0\.0\.1_linux_amd64\.tar\.gz/);
  assert.match(stdout, /file:.*kombify-stackkits_0\.0\.1_linux_amd64\.deb/);
  assert.match(stdout, /file:.*checksums\.txt/);
  assert.match(stdout, /file:.*release-evidence\.json/);
  assert.match(stdout, /file:.*\.spdx\.json/);
  assert.match(stdout, /image:oci:\/\/ghcr\.io\/kombifyio\/stackkits:v0\.0\.1/);
  assert.doesNotMatch(stdout, /notes\.txt/);
});
