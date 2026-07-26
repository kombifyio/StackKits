#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 9 ]; then
  printf 'usage: %s <archive> <sbom> <attestation> <trusted-root> <release-index> <release-index-attestation> <source-commit> <source-digest> <output-dir>\n' "$0" >&2
  exit 2
fi

archive="$(realpath "$1")"
sbom="$(realpath "$2")"
attestation="$(realpath "$3")"
trusted_root="$(realpath "$4")"
release_index="$(realpath "$5")"
release_index_attestation="$(realpath "$6")"
source_commit="$7"
source_digest="$8"
output_dir="$(realpath -m "$9")"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for executable in docker jq node sha256sum tar tcpdump timeout; do
  command -v "$executable" >/dev/null || {
    printf 'standalone runtime E2E requires %s\n' "$executable" >&2
    exit 1
  }
done
[[ "$source_commit" =~ ^[0-9a-f]{40}$ ]] || {
  printf 'source commit must be an exact 40-character Git digest\n' >&2
  exit 1
}
[[ "$source_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || {
  printf 'source digest must be sha256:<64 lowercase hex>\n' >&2
  exit 1
}

mkdir -p "$output_dir"
work_root="$(mktemp -d)"
fixture_pid=
capture_pid=
project_dir="$work_root/project"
extract_dir="$work_root/archive"
home_dir="$work_root/home"
fixture_dir="$work_root/release-fixture"
raw_traffic="$output_dir/tcpdump.log"
traffic_events="$output_dir/network-events.jsonl"

cleanup() {
  if [ -n "${capture_pid:-}" ]; then
    kill -INT "$capture_pid" 2>/dev/null || true
    wait "$capture_pid" 2>/dev/null || true
  fi
  if [ -n "${fixture_pid:-}" ]; then
    kill "$fixture_pid" 2>/dev/null || true
    wait "$fixture_pid" 2>/dev/null || true
  fi
  if [ -f "$project_dir/.stackkit/runtime/basement-core/compose.yaml" ]; then
    STACKKIT_CUSTODY_DIR="$project_dir/.stackkit/custody" \
      docker compose --project-name stackkit-basement-core \
      -f "$project_dir/.stackkit/runtime/basement-core/compose.yaml" \
      down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$work_root"
}
trap cleanup EXIT

mkdir -p "$project_dir" "$extract_dir" "$home_dir/.stackkits" "$fixture_dir"
cp "$archive" "$sbom" "$attestation" "$trusted_root" "$fixture_dir/"
cp "$release_index" "$fixture_dir/stackkits-release-index-v1.json"
cp "$release_index_attestation" "$fixture_dir/stackkits-release-index-v1.json.intoto.jsonl"
tar xzf "$archive" -C "$extract_dir"
for directory in base modules cue.mod basement-kit; do
  if [ -e "$extract_dir/$directory" ]; then
    cp -R "$extract_dir/$directory" "$home_dir/.stackkits/"
  fi
done
cp -R "$extract_dir/base" "$home_dir/.stackkits/basement-kit/"

archive_name="$(basename "$archive")"
version="$(jq -er --arg name "$archive_name" '.assets[] | select(.kit == "basement-kit" and .platform.os == "linux" and .archive.name == $name) | .version' "$release_index")"
for input in "$archive" "$sbom" "$attestation" "$trusted_root"; do
  expected="$(jq -er --arg name "$(basename "$input")" '
    [.release.trustedRoot, .assets[].archive, .assets[].sbom, .assets[].attestation]
    | flatten | .[] | select(.name == $name) | .sha256
  ' "$release_index")"
  actual="$(sha256sum "$input" | cut -d' ' -f1)"
  [ "$actual" = "$expected" ] || {
    printf 'release fixture digest mismatch for %s\n' "$(basename "$input")" >&2
    exit 1
  }
done

export HOME="$home_dir"
export PATH="$extract_dir:$PATH"
cd "$project_dir"
timeout 600 stackkit init --owner-source=local --non-interactive >"$output_dir/init.log"
before_validate="$(find . -type f -print0 | sort -z | xargs -0 sha256sum)"
timeout 600 stackkit validate >"$output_dir/validate.log"
after_validate="$(find . -type f -print0 | sort -z | xargs -0 sha256sum)"
[ "$before_validate" = "$after_validate" ] || {
  printf 'standalone validate mutated the workspace\n' >&2
  exit 1
}
timeout 600 stackkit generate >"$output_dir/generate.log"

compose="$project_dir/deploy/instances/stackkits-basement-core-runtime/compose-node-main/platform/basement-core/compose.yaml"
[ -s "$compose" ] || {
  printf 'standalone generate did not produce the Basement Compose artifact\n' >&2
  exit 1
}
while IFS= read -r image; do
  if ! docker image inspect "$image" >/dev/null; then
    if [ "${STACKKIT_E2E_PRELOAD_IMAGES:-0}" != "1" ]; then
      printf 'recorded runtime phase requires preloaded image %s\n' "$image" >&2
      exit 1
    fi
    printf 'preloading runtime image before traffic capture: %s\n' "$image"
    docker pull "$image"
  fi
done < <(STACKKIT_CUSTODY_DIR="$project_dir/.stackkit/custody" docker compose -f "$compose" config --images | sort -u)

fixture_port="${STACKKIT_E2E_FIXTURE_PORT:-18080}"
node "$script_dir/github-release-fixture.mjs" "$fixture_dir" "$fixture_port" >"$output_dir/fixture-url.txt" 2>"$output_dir/fixture.log" &
fixture_pid=$!
for _ in $(seq 1 50); do
  [ -s "$output_dir/fixture-url.txt" ] && break
  kill -0 "$fixture_pid" 2>/dev/null || {
    printf 'GitHub release fixture exited before readiness\n' >&2
    exit 1
  }
  sleep 0.1
done
fixture_url="$(head -n1 "$output_dir/fixture-url.txt")"
[ -n "$fixture_url" ] || {
  printf 'GitHub release fixture did not become ready\n' >&2
  exit 1
}

tcpdump -i any -nn -l '(udp port 53 or (tcp and ip))' >"$raw_traffic" 2>"$output_dir/tcpdump.stderr.log" &
capture_pid=$!
export STACKKIT_RELEASE_FIXTURE_URL="$fixture_url"
timeout 600 stackkit upgrade --to "$version" --json >"$output_dir/release-install.json"

runtime_started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
runtime_started_epoch="$(date +%s)"
timeout 600 sh -ec '
  stackkit apply >"$1"
  stackkit verify --json >"$2"
' sh "$output_dir/apply.log" "$output_dir/verify.json"
runtime_finished_epoch="$(date +%s)"
runtime_finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
runtime_duration=$((runtime_finished_epoch - runtime_started_epoch))
[ "$runtime_duration" -le 600 ] || {
  printf 'standalone runtime phase exceeded 600 seconds\n' >&2
  exit 1
}

kill -INT "$capture_pid"
wait "$capture_pid" || true
capture_pid=
node "$script_dir/parse-standalone-traffic.mjs" "$raw_traffic" "$traffic_events"

evidence_digest() {
  sha256sum "$1" | cut -d' ' -f1
}
event_count="$(wc -l <"$traffic_events" | tr -d ' ')"
jq -n \
  --arg archiveName "$archive_name" \
  --arg archiveSha256 "$(evidence_digest "$archive")" \
  --arg sbomSha256 "$(evidence_digest "$sbom")" \
  --arg attestationSha256 "$(evidence_digest "$attestation")" \
  --arg releaseIndexSha256 "$(evidence_digest "$release_index")" \
  --arg sourceCommit "$source_commit" \
  --arg sourceDigest "$source_digest" \
  --arg startedAt "$runtime_started" \
  --arg finishedAt "$runtime_finished" \
  --argjson durationSeconds "$runtime_duration" \
  --arg trafficSha256 "$(evidence_digest "$traffic_events")" \
  --argjson eventCount "$event_count" \
  --arg applySha256 "$(evidence_digest "$output_dir/apply.log")" \
  --arg verifySha256 "$(evidence_digest "$output_dir/verify.json")" \
  '{
    schemaVersion: "stackkit.oss-runtime-e2e-evidence/v1",
    source: {repository: "kombifyio/stackKits", commit: $sourceCommit, digest: $sourceDigest},
    archive: {
      name: $archiveName, sha256: $archiveSha256, sbomSha256: $sbomSha256,
      attestationSha256: $attestationSha256, releaseIndexSha256: $releaseIndexSha256
    },
    network: {
      recorder: "stackkit.hermetic-network-log/v1",
      eventsSha256: $trafficSha256,
      eventCount: $eventCount
    },
    phase: {
      id: "runtime", status: "pass", startedAt: $startedAt, finishedAt: $finishedAt,
      durationSeconds: $durationSeconds,
      commands: ["stackkit apply", "stackkit verify --json"],
      evidence: [
        {name: "apply.log", sha256: $applySha256},
        {name: "verify.json", sha256: $verifySha256}
      ]
    }
  }' >"$output_dir/runtime-evidence.json"

node "$script_dir/validate-standalone-runtime-e2e.mjs" \
  "$output_dir/runtime-evidence.json" "$traffic_events"
printf 'standalone OSS runtime E2E passed: %s\n' "$output_dir/runtime-evidence.json"
