import { chmod, mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import test from 'node:test';
import assert from 'node:assert/strict';

const execFileAsync = promisify(execFile);
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const installPath = path.join(repoRoot, 'install.sh');

test('installer falls back from an unusable authenticated latest API response to the public API', async (t) => {
  const result = await runInstallerResolutionSmoke(t, 'unauth-api');
  if (!result) return;

  assert.match(result.stdout, /Authenticated latest-release lookup did not return a tag/);
  assert.match(result.stdout, /v9\.8\.7 \(linux\/amd64\)/);
  assert.match(result.stdout, /Authenticated download failed; retrying without GitHub token/);
});

test('installer falls back to the releases/latest redirect when API tag parsing fails', async (t) => {
  const result = await runInstallerResolutionSmoke(t, 'redirect');
  if (!result) return;

  assert.match(result.stdout, /GitHub API latest-release lookup did not return a tag/);
  assert.match(result.stdout, /v9\.8\.7 \(linux\/amd64\)/);
});

test('installer only uses prerelease versions when explicitly pinned', async (t) => {
  const result = await runInstallerResolutionSmoke(t, 'pinned-prerelease', {
    STACKKIT_RELEASE_VERSION: 'v0.4.0-beta.1',
  });
  if (!result) return;

  assert.match(result.stdout, /v0\.4\.0-beta\.1 \(linux\/amd64\)/);
  assert.doesNotMatch(result.stdout, /latest-release lookup did not return a tag/);
  assert.doesNotMatch(result.stdout, /v9\.8\.7 \(linux\/amd64\)/);
});

async function runInstallerResolutionSmoke(t, mode, extraEnv = {}) {
  try {
    await execFileAsync('sh', ['-c', 'true']);
  } catch {
    t.skip('POSIX sh is not available on this host');
    return null;
  }

  const root = await mkdtemp(path.join(os.tmpdir(), 'stackkit-install-latest-'));
  const bin = path.join(root, 'bin');
  const home = path.join(root, 'home');
  await mkdir(bin, { recursive: true });
  await mkdir(home, { recursive: true });
  await writeExecutable(path.join(bin, 'uname'), `#!/bin/sh
if [ "$1" = "-s" ]; then
  echo Linux
else
  echo x86_64
fi
`);
  await writeExecutable(path.join(bin, 'id'), `#!/bin/sh
if [ "$1" = "-u" ]; then
  echo 0
else
  echo 1000
fi
`);
  await writeExecutable(path.join(bin, 'curl'), fakeCurlScript());
  await writeExecutable(path.join(bin, 'tar'), `#!/bin/sh
dest=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-C" ]; then
    dest="$2"
    shift 2
    continue
  fi
  shift
done
mkdir -p "$dest/base" "$dest/cue.mod" "$dest/modules" "$dest/base-kit"
printf '#!/bin/sh\\necho stackkit test\\n' > "$dest/stackkit"
printf '#!/bin/sh\\necho tofu test\\n' > "$dest/tofu"
chmod +x "$dest/stackkit" "$dest/tofu"
`);
  await writeExecutable(path.join(bin, 'install'), `#!/bin/sh
exit 0
`);
  await writeExecutable(path.join(bin, 'stackkit'), `#!/bin/sh
echo stackkit test
`);

  return execFileAsync('sh', [installPath], {
    env: {
      ...process.env,
      HOME: home,
      PATH: `${bin}${path.delimiter}${process.env.PATH || ''}`,
      STACKKIT_FAKE_LATEST_MODE: mode,
      STACKKIT_GITHUB_TOKEN: 'bad-token-for-public-mirror',
      STACKKIT_NO_BANNER: '1',
      STACKKIT_RELEASE_VERSION: '',
      STACKKIT_RELEASE_REPO: 'kombifyio/stackKits',
      ...extraEnv,
    },
    timeout: 30_000,
    maxBuffer: 1024 * 1024,
  });
}

async function writeExecutable(file, content) {
  await writeFile(file, content.replaceAll('\r\n', '\n'));
  await chmod(file, 0o755);
}

function fakeCurlScript() {
  return `#!/bin/sh
set -eu
auth=0
out=""
write=""
head=0
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -H)
      case "$2" in
        Authorization:*) auth=1 ;;
      esac
      shift 2
      ;;
    -o)
      out="$2"
      shift 2
      ;;
    -w)
      write="$2"
      shift 2
      ;;
    -I)
      head=1
      shift
      ;;
    -*)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

if [ "$head" = "1" ] && [ "$write" = "%{url_effective}" ]; then
  printf '%s' 'https://github.com/kombifyio/stackKits/releases/tag/v9.8.7'
  exit 0
fi

case "$url" in
  *api.github.com*/releases/latest)
    if [ "$auth" = "1" ] || [ "$STACKKIT_FAKE_LATEST_MODE" = "redirect" ]; then
      printf '%s' '{"message":"Not Found"}'
    else
      printf '%s' '{"tag_name":"v9.8.7"}'
    fi
    ;;
  *releases/download/v0.4.0-beta.1/*)
    if [ "$auth" = "1" ]; then
      exit 22
    fi
    : > "$out"
    ;;
  *releases/download/v9.8.7/*)
    if [ "$auth" = "1" ]; then
      exit 22
    fi
    : > "$out"
    ;;
  *)
    printf 'unexpected curl URL: %s\\n' "$url" >&2
    exit 2
    ;;
esac
`;
}
