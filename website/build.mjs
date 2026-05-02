import { cp, mkdir, rm } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const rootDir = path.dirname(fileURLToPath(import.meta.url));
const outputDir = path.join(rootDir, 'build');
const entries = [
  'index.html',
  'impressum.html',
  'favicon.svg',
  'icon.png',
  'CNAME',
  'install',
  'base',
  'modern',
  'ha',
  'cli',
];

await rm(outputDir, { force: true, recursive: true });
await mkdir(outputDir, { recursive: true });

for (const entry of entries) {
  await cp(path.join(rootDir, entry), path.join(outputDir, entry), { recursive: true });
}
