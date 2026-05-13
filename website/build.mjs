import { cp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { extractLatestReleaseNotes } from '../scripts/release/changelog.mjs';

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

function escapeHtml(value) {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

const changelog = await readFile(path.join(rootDir, '..', 'CHANGELOG.md'), 'utf8');
const latest = extractLatestReleaseNotes(changelog, { fallbackVersion: 'Unreleased' });
await writeFile(
  path.join(outputDir, 'changelog.json'),
  `${JSON.stringify(latest, null, 2)}\n`,
  'utf8',
);

const releaseItems = latest.notes
  .map((note) => `<li><strong>${escapeHtml(note.title)}</strong><span>${escapeHtml(note.body)}</span></li>`)
  .join('\n');
const releaseSummary = `<section class="release-summary">
    <h2>Latest Release Notes</h2>
    <p class="section-desc">StackKits ${escapeHtml(latest.version)}</p>
    <ul class="release-list">
      ${releaseItems || '<li><strong>Release notes</strong><span>Release notes will appear here once CHANGELOG.md has a published entry.</span></li>'}
    </ul>
    <p style="margin-top:1rem;"><a href="/changelog.json">Machine-readable changelog &rarr;</a></p>
  </section>`;

const indexPath = path.join(outputDir, 'index.html');
const indexHtml = await readFile(indexPath, 'utf8');
await writeFile(indexPath, indexHtml.replace('<!-- RELEASE_SUMMARY -->', releaseSummary), 'utf8');
