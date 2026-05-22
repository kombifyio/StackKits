#!/usr/bin/env node
import { readFile, writeFile } from 'node:fs/promises';

const VALID_STATUS = new Set(['pass', 'fail', 'pending', 'not_applicable']);

function parseArgs(argv) {
  const opts = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    const next = argv[i + 1];
    switch (arg) {
      case '--file':
        opts.file = next;
        i += 1;
        break;
      case '--name':
        opts.name = next;
        i += 1;
        break;
      case '--status':
        opts.status = next;
        i += 1;
        break;
      case '--summary':
        opts.summary = next;
        i += 1;
        break;
      case '--url':
        opts.url = next;
        i += 1;
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
    }
  }

  for (const key of ['file', 'name', 'status']) {
    if (!opts[key]) throw new Error(`missing required --${key}`);
  }
  if (!VALID_STATUS.has(opts.status)) {
    throw new Error(`invalid status: ${opts.status}`);
  }
  return opts;
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const evidence = JSON.parse(await readFile(opts.file, 'utf8'));
  evidence.checks = evidence.checks || {};
  evidence.checks[opts.name] = {
    status: opts.status,
    ...(opts.summary ? { summary: opts.summary } : {}),
    ...(opts.url ? { url: opts.url } : {}),
  };
  await writeFile(opts.file, `${JSON.stringify(evidence, null, 2)}\n`);
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
