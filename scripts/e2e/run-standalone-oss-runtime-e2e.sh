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
stable_day2_proof="${STACKKIT_E2E_STABLE_DAY2_PROOF:-0}"
previous_tag="${STACKKIT_E2E_PREVIOUS_TAG:-v0.8.0-beta.5}"
candidate_archive="$archive"
candidate_sbom="$sbom"
candidate_attestation="$attestation"
candidate_trusted_root="$trusted_root"
candidate_release_index="$release_index"
candidate_release_index_attestation="$release_index_attestation"
candidate_source_commit="$source_commit"
candidate_source_digest="$source_digest"

if [ "$stable_day2_proof" = "1" ]; then
  [[ "$previous_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.[0-9]+)?$ ]] || {
    printf 'STACKKIT_E2E_PREVIOUS_TAG must be an exact v-prefixed release tag\n' >&2
    exit 1
  }
  for variable in \
    STACKKIT_E2E_PREVIOUS_ARCHIVE \
    STACKKIT_E2E_PREVIOUS_SBOM \
    STACKKIT_E2E_PREVIOUS_ATTESTATION \
    STACKKIT_E2E_PREVIOUS_TRUSTED_ROOT \
    STACKKIT_E2E_PREVIOUS_RELEASE_INDEX \
    STACKKIT_E2E_PREVIOUS_RELEASE_INDEX_ATTESTATION \
    STACKKIT_E2E_PREVIOUS_SOURCE_COMMIT \
    STACKKIT_E2E_PREVIOUS_SOURCE_DIGEST; do
    [ -n "${!variable:-}" ] || {
      printf 'stable Day-2 proof requires %s\n' "$variable" >&2
      exit 1
    }
  done
  archive="$(realpath "$STACKKIT_E2E_PREVIOUS_ARCHIVE")"
  sbom="$(realpath "$STACKKIT_E2E_PREVIOUS_SBOM")"
  attestation="$(realpath "$STACKKIT_E2E_PREVIOUS_ATTESTATION")"
  trusted_root="$(realpath "$STACKKIT_E2E_PREVIOUS_TRUSTED_ROOT")"
  release_index="$(realpath "$STACKKIT_E2E_PREVIOUS_RELEASE_INDEX")"
  release_index_attestation="$(realpath "$STACKKIT_E2E_PREVIOUS_RELEASE_INDEX_ATTESTATION")"
  source_commit="$STACKKIT_E2E_PREVIOUS_SOURCE_COMMIT"
  source_digest="$STACKKIT_E2E_PREVIOUS_SOURCE_DIGEST"
fi

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
restore_activation_pid=
restore_helper_name=
project_dir="$work_root/project"
extract_dir="$work_root/archive"
home_dir="$work_root/home"
fixture_dir="$work_root/release-fixture"
raw_traffic="$output_dir/tcpdump.log"
traffic_events="$output_dir/network-events.jsonl"
network_scope="$output_dir/compose-origin-scope.json"

cleanup() {
  if [ -n "${restore_activation_pid:-}" ]; then
    kill -KILL -- "-$restore_activation_pid" 2>/dev/null || true
    wait "$restore_activation_pid" 2>/dev/null || true
  fi
  if [ -n "${restore_helper_name:-}" ]; then
    docker unpause "$restore_helper_name" >/dev/null 2>&1 || true
    docker rm -f "$restore_helper_name" >/dev/null 2>&1 || true
  fi
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
    [
      [.release.trustedRoot, .assets[].archive, .assets[].sbom, .assets[].attestation]
      | flatten | .[] | select(.name == $name) | .sha256
    ]
    | unique
    | if length == 1 then .[0]
      else error("release fixture must resolve to exactly one unique digest")
      end
  ' "$release_index")"
  actual="$(sha256sum "$input" | cut -d' ' -f1)"
  [ "$actual" = "$expected" ] || {
    printf 'release fixture digest mismatch for %s\n' "$(basename "$input")" >&2
    exit 1
  }
done

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

export HOME="$home_dir"
export PATH="$extract_dir:$PATH"
export STACKKIT_RELEASE_FIXTURE_URL="$fixture_url"

# Capture begins before init because released init establishes release authority
# through the fixture. Host DNS is retained as a negative Kombify-domain gate;
# Compose-origin TCP is bound later to the inspected runtime scope.
capture_filter='(udp port 53 or (tcp[tcpflags] & (tcp-syn|tcp-ack) == tcp-syn))'
tcpdump -ddd "$capture_filter" >/dev/null
tcpdump -tttt -i any -nn -l "$capture_filter" >"$raw_traffic" 2>"$output_dir/tcpdump-init.stderr.log" &
capture_pid=$!
for _ in $(seq 1 50); do
  grep -q 'listening on' "$output_dir/tcpdump-init.stderr.log" && break
  kill -0 "$capture_pid" 2>/dev/null || {
    printf 'authoring traffic capture exited before readiness\n' >&2
    exit 1
  }
  sleep 0.1
done
grep -q 'listening on' "$output_dir/tcpdump-init.stderr.log" || {
  printf 'authoring traffic capture did not become ready\n' >&2
  exit 1
}

cd "$project_dir"
timeout 600 stackkit init --owner-source=local --non-interactive >"$output_dir/init.log"
timeout 600 stackkit kit verify --json >"$output_dir/release-bootstrap.json"
jq -e --arg version "$version" '
  .schemaVersion == "stackkit.command-result/v1"
  and .status == "success"
  and (.data | length) == 1
  and .data[0].version == $version
' "$output_dir/release-bootstrap.json" >/dev/null || {
  printf 'standalone init did not establish the exact release receipt\n' >&2
  exit 1
}
before_validate="$(find . -type f -print0 | sort -z | xargs -0 sha256sum)"
timeout 600 stackkit validate >"$output_dir/validate.log"
after_validate="$(find . -type f -print0 | sort -z | xargs -0 sha256sum)"
[ "$before_validate" = "$after_validate" ] || {
  printf 'standalone validate mutated the workspace\n' >&2
  exit 1
}
timeout 600 stackkit generate >"$output_dir/generate.log"

# End the authoring segment before optional image downloads. The second
# segment appends runtime traffic after all images are present locally.
kill -INT "$capture_pid"
wait "$capture_pid" || true
capture_pid=

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

tcpdump -ddd "$capture_filter" >/dev/null
tcpdump -tttt -i any -nn -l "$capture_filter" >>"$raw_traffic" 2>"$output_dir/tcpdump.stderr.log" &
capture_pid=$!
for _ in $(seq 1 50); do
  grep -q 'listening on' "$output_dir/tcpdump.stderr.log" && break
  kill -0 "$capture_pid" 2>/dev/null || {
    printf 'runtime traffic capture exited before readiness\n' >&2
    exit 1
  }
  sleep 0.1
done
grep -q 'listening on' "$output_dir/tcpdump.stderr.log" || {
  printf 'runtime traffic capture did not become ready\n' >&2
  exit 1
}

runtime_started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
runtime_started_epoch="$(date +%s)"
timeout 600 stackkit apply >"$output_dir/apply.log" 2>&1

runtime_compose="$project_dir/.stackkit/runtime/basement-core/compose.yaml"
[ -s "$runtime_compose" ] || {
  printf 'standalone Apply did not persist the Basement runtime Compose artifact\n' >&2
  exit 1
}
container_lines="$work_root/origin-scope-containers.jsonl"
network_lines="$work_root/origin-scope-networks.jsonl"
runtime_compose_config="$work_root/runtime-compose-config.json"
STACKKIT_CUSTODY_DIR="$project_dir/.stackkit/custody" \
  docker compose --project-name stackkit-basement-core -f "$runtime_compose" \
  config --format json >"$runtime_compose_config"
jq -e '
  (.services.coolify.extra_hosts // []) as $extraHosts
  | ($extraHosts | type) == "array"
    and ($extraHosts | index("host.docker.internal=host-gateway")) != null
' "$runtime_compose_config" >/dev/null || {
  printf 'Coolify Compose config must declare host.docker.internal=host-gateway\n' >&2
  exit 1
}
mapfile -t container_ids < <(
  STACKKIT_CUSTODY_DIR="$project_dir/.stackkit/custody" \
    docker compose --project-name stackkit-basement-core -f "$runtime_compose" ps --all --quiet --no-trunc \
    | sort -u
)
[ "${#container_ids[@]}" -gt 0 ] || {
  printf 'Compose project stackkit-basement-core has no containers after Apply\n' >&2
  exit 1
}
mapfile -t labelled_project_container_ids < <(
  docker ps --all --quiet --no-trunc --filter label=com.docker.compose.project=stackkit-basement-core \
    | sort -u
)
diff -u \
  <(printf '%s\n' "${container_ids[@]}") \
  <(printf '%s\n' "${labelled_project_container_ids[@]}") || {
    printf 'Compose ps and exact project-labelled container sets differ\n' >&2
    exit 1
  }
for container_id in "${container_ids[@]}"; do
  docker inspect "$container_id" | jq -ec --arg project stackkit-basement-core '
    .[0]
    | if .Config.Labels["com.docker.compose.project"] != $project
      then error("container is outside the exact Compose project")
      else .
      end
    | if (.HostConfig.NetworkMode == "host"
          or .HostConfig.NetworkMode == "none"
          or (.HostConfig.NetworkMode | startswith("container:")))
      then error("host, none, and container network modes are forbidden")
      else .
      end
    | {
        id: .Id,
        service: .Config.Labels["com.docker.compose.service"],
        networks: (
          [.NetworkSettings.Networks // {} | to_entries[] | {
            id: .value.NetworkID,
            ips: ([.value.IPAddress, .value.GlobalIPv6Address] | map(select(length > 0)) | sort | unique)
          }]
          | sort_by(.id)
        )
      }
  ' >>"$container_lines"
done
mapfile -t expected_services < <(
  STACKKIT_CUSTODY_DIR="$project_dir/.stackkit/custody" \
    docker compose --project-name stackkit-basement-core -f "$runtime_compose" config --services \
    | sort -u
)
mapfile -t observed_services < <(jq -rs '[.[].service] | unique[]' "$container_lines")
[ "$(wc -l <"$container_lines" | tr -d ' ')" -eq "${#observed_services[@]}" ] || {
  printf 'Compose project must have exactly one container for every service\n' >&2
  exit 1
}
[ "${#expected_services[@]}" -eq "${#observed_services[@]}" ] &&
  diff -u <(printf '%s\n' "${expected_services[@]}") <(printf '%s\n' "${observed_services[@]}") || {
    printf 'Compose project service set differs from the inspected container service set\n' >&2
    exit 1
  }
coolify_container_id="$(jq -ers '
  [.[] | select(.service == "coolify")]
  | if length == 1 then .[0].id
    else error("Compose project must contain exactly one Coolify container")
    end
' "$container_lines")"
mapfile -t resolved_host_gateways < <(
  docker exec "$coolify_container_id" cat /etc/hosts \
    | awk '{
        for (field = 2; field <= NF; field++) {
          if ($field == "host.docker.internal") print $1
        }
      }' \
    | sort -u
)
[ "${#resolved_host_gateways[@]}" -eq 1 ] || {
  printf 'running Coolify container must resolve exactly one host.docker.internal address\n' >&2
  exit 1
}
resolved_host_gateway="${resolved_host_gateways[0]}"
node -e '
  const {isIP} = require("node:net")
  process.exit(isIP(process.argv[1]) !== 0 && process.argv[1] === process.argv[1].toLowerCase() ? 0 : 1)
' "$resolved_host_gateway" || {
  printf 'running Coolify host.docker.internal resolution is not a canonical IP address\n' >&2
  exit 1
}
mapfile -t network_ids < <(jq -rs '[.[].networks[].id] | unique[]' "$container_lines")
[ "${#network_ids[@]}" -gt 0 ] || {
  printf 'Compose project stackkit-basement-core has no inspectable bridge network\n' >&2
  exit 1
}
for network_id in "${network_ids[@]}"; do
  docker network inspect "$network_id" | jq -ec --arg id "$network_id" '
    .[0]
    | if .Id != $id then error("network ID changed during inspection") else . end
    | {
        id: .Id,
        name: .Name,
        subnets: ([.IPAM.Config[]?.Subnet | select(type == "string" and length > 0)] | sort | unique)
      }
  ' >>"$network_lines"
done
jq -n \
  --arg schemaVersion "stackkit.compose-origin-scope/v2" \
  --arg project "stackkit-basement-core" \
  --arg resolvedHostGateway "$resolved_host_gateway" \
  --arg coolifyContainerId "$coolify_container_id" \
  --slurpfile containers <(jq -s 'sort_by(.id)' "$container_lines") \
  --slurpfile networks <(jq -s 'sort_by(.id)' "$network_lines") \
  '{
    schemaVersion: $schemaVersion,
    project: $project,
    networks: $networks[0],
    containers: $containers[0],
    localHostGateways: [{
      host: $resolvedHostGateway,
      sourceContainerId: $coolifyContainerId,
      sourceService: "coolify"
    }]
  }' >"$network_scope"
node -e '
  const {readComposeOriginScope} = await import(process.argv[1])
  readComposeOriginScope(process.argv[2])
' "$script_dir/compose-origin-scope.mjs" "$network_scope"

timeout 600 stackkit verify --json >"$output_dir/verify.json"
base_runtime_finished_epoch="$(date +%s)"
base_runtime_finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [ "$stable_day2_proof" = "1" ]; then
  stable_day2_dir="$output_dir/stable-day2"
  candidate_fixture_dir="$work_root/candidate-release-fixture"
  candidate_extract_dir="$work_root/candidate-archive"
  mkdir -p "$stable_day2_dir" "$candidate_fixture_dir" "$candidate_extract_dir"
  [ "$version" = "$previous_tag" ] || {
    printf 'stable Day-2 previous index resolved %s, expected %s\n' "$version" "$previous_tag" >&2
    exit 1
  }

  cp \
    "$candidate_archive" \
    "$candidate_sbom" \
    "$candidate_attestation" \
    "$candidate_trusted_root" \
    "$candidate_fixture_dir/"
  cp "$candidate_release_index" "$candidate_fixture_dir/stackkits-release-index-v1.json"
  cp "$candidate_release_index_attestation" \
    "$candidate_fixture_dir/stackkits-release-index-v1.json.intoto.jsonl"
  candidate_archive_name="$(basename "$candidate_archive")"
  candidate_version="$(jq -er --arg name "$candidate_archive_name" '
    .assets[]
    | select(.kit == "basement-kit" and .platform.os == "linux" and .archive.name == $name)
    | .version
  ' "$candidate_release_index")"

  if timeout 60 stackkit advanced change-set create \
    --capability "$project_dir/missing-advanced-capability.json" \
    --candidate-spec "$project_dir/stack.yaml" \
    --json \
    >"$stable_day2_dir/advanced-denial.json" \
    2>"$stable_day2_dir/advanced-denial.stderr.log"; then
    printf 'advanced change-set unexpectedly succeeded without a capability\n' >&2
    exit 1
  fi
  jq -e '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "denied"
    and .data.schemaVersion == "stackkit.operation-denial/v1"
    and .data.operation == "terramate.change-set.create"
    and .data.mode == "advanced"
    and (.data.reasonCode | type == "string" and length > 0)
  ' "$stable_day2_dir/advanced-denial.json" >/dev/null || {
    printf 'advanced change-set denial was not structured and fail-closed\n' >&2
    exit 1
  }

  kill "$fixture_pid" 2>/dev/null || true
  wait "$fixture_pid" 2>/dev/null || true
  fixture_pid=
  candidate_fixture_port="${STACKKIT_E2E_CANDIDATE_FIXTURE_PORT:-18081}"
  node "$script_dir/github-release-fixture.mjs" \
    "$candidate_fixture_dir" "$candidate_fixture_port" \
    >"$stable_day2_dir/fixture-url.txt" \
    2>"$stable_day2_dir/fixture.log" &
  fixture_pid=$!
  for _ in $(seq 1 50); do
    [ -s "$stable_day2_dir/fixture-url.txt" ] && break
    kill -0 "$fixture_pid" 2>/dev/null || {
      printf 'candidate GitHub release fixture exited before readiness\n' >&2
      exit 1
    }
    sleep 0.1
  done
  candidate_fixture_url="$(head -n1 "$stable_day2_dir/fixture-url.txt")"
  [ -n "$candidate_fixture_url" ] || {
    printf 'candidate GitHub release fixture did not become ready\n' >&2
    exit 1
  }
  export STACKKIT_RELEASE_FIXTURE_URL="$candidate_fixture_url"

  timeout 600 stackkit upgrade --to "$candidate_version" --dry-run --json \
    >"$stable_day2_dir/upgrade-dry-run.json"
  jq -e --arg version "$candidate_version" '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "success"
    and .data.version == $version
    and .data.dryRun == true
    and .data.applyInvoked == false
    and .data.inspection.plan != null
  ' "$stable_day2_dir/upgrade-dry-run.json" >/dev/null || {
    printf 'candidate upgrade dry-run did not inspect the exact target\n' >&2
    exit 1
  }

  timeout 600 stackkit upgrade --to "$candidate_version" --json \
    >"$stable_day2_dir/upgrade.json"
  jq -e --arg version "$candidate_version" '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "success"
    and .data.version == $version
    and .data.dryRun == false
    and .data.applyInvoked == true
    and (.data.checkpoint.kopiaAnchorId | test("^sha256:[0-9a-f]{64}$"))
    and (.data.checkpoint.executorStateSnapshotId | test("^sha256:[0-9a-f]{64}$"))
    and .data.transaction.status == "succeeded"
    and .data.transaction.target.applyInvoked == true
    and .data.transaction.target.verifyInvoked == true
    and .data.transaction.target.verified == true
  ' "$stable_day2_dir/upgrade.json" >/dev/null || {
    printf 'candidate upgrade did not prove snapshot, Apply, and Verify\n' >&2
    exit 1
  }

  tar xzf "$candidate_archive" -C "$candidate_extract_dir"
  candidate_stackkit="$candidate_extract_dir/stackkit"
  [ -x "$candidate_stackkit" ] || {
    printf 'candidate archive does not contain an executable stackkit binary\n' >&2
    exit 1
  }
  timeout 120 "$candidate_stackkit" drift detect --json >"$stable_day2_dir/drift.json"
  jq -e '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "success"
    and .data.schemaVersion == "stackkit.drift-report/v1"
    and .data.hasDrift == false
  ' "$stable_day2_dir/drift.json" >/dev/null || {
    printf 'candidate drift report did not prove the upgraded runtime in sync\n' >&2
    exit 1
  }
  timeout 120 "$candidate_stackkit" verify --json >"$stable_day2_dir/final-verify.json"
  jq -e '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "success"
  ' "$stable_day2_dir/final-verify.json" >/dev/null || {
    printf 'candidate final verification failed\n' >&2
    exit 1
  }

  stable_day2_evidence="$output_dir/stable-day2-live-evidence.json"
  upgrade_snapshot_anchor="$(
    jq -er '.data.checkpoint.kopiaAnchorId | select(test("^sha256:[0-9a-f]{64}$"))' \
      "$stable_day2_dir/upgrade.json"
  )"
  capability_denial_reason="$(
    jq -er '.data.reasonCode | select(type == "string" and length > 0)' \
      "$stable_day2_dir/advanced-denial.json"
  )"
  jq -n \
    --arg sourceCommit "$candidate_source_commit" \
    --arg sourceDigest "$candidate_source_digest" \
    --arg previousTag "$previous_tag" \
    --arg previousArchiveSha256 "$(sha256sum "$archive" | cut -d' ' -f1)" \
    --arg candidateArchiveSha256 "$(sha256sum "$candidate_archive" | cut -d' ' -f1)" \
    --arg snapshotAnchorId "$upgrade_snapshot_anchor" \
    --arg denialReasonCode "$capability_denial_reason" \
    '{
      schemaVersion: "stackkit.stable-day2-live-e2e-evidence/v1",
      status: "pass",
      source: {
        commit: $sourceCommit,
        digest: $sourceDigest
      },
      previous: {
        tag: $previousTag,
        archiveSha256: $previousArchiveSha256
      },
      candidate: {
        archiveSha256: $candidateArchiveSha256
      },
      upgrade: {
        status: "pass",
        snapshotAnchorId: $snapshotAnchorId,
        verified: true
      },
      drift: {status: "pass"},
      capabilityDenial: {reasonCode: $denialReasonCode},
      finalVerify: true
    }' >"$stable_day2_evidence"
  jq -e \
    --arg sourceCommit "$candidate_source_commit" \
    --arg sourceDigest "$candidate_source_digest" \
    --arg previousTag "$previous_tag" \
    --arg previousArchiveSha256 "$(sha256sum "$archive" | cut -d' ' -f1)" \
    --arg candidateArchiveSha256 "$(sha256sum "$candidate_archive" | cut -d' ' -f1)" \
    --arg snapshotAnchorId "$upgrade_snapshot_anchor" \
    --arg denialReasonCode "$capability_denial_reason" '
      .schemaVersion == "stackkit.stable-day2-live-e2e-evidence/v1"
      and .status == "pass"
      and .source == {commit: $sourceCommit, digest: $sourceDigest}
      and .previous == {tag: $previousTag, archiveSha256: $previousArchiveSha256}
      and .candidate == {archiveSha256: $candidateArchiveSha256}
      and .upgrade == {
        status: "pass",
        snapshotAnchorId: $snapshotAnchorId,
        verified: true
      }
      and .drift == {status: "pass"}
      and .capabilityDenial == {reasonCode: $denialReasonCode}
      and .finalVerify == true
    ' "$stable_day2_evidence" >/dev/null
  printf 'stable Day-2 live proof passed: %s\n' "$stable_day2_evidence"
fi

if [ "${STACKKIT_E2E_RESTORE_PROOF:-0}" = "1" ]; then
  command -v setsid >/dev/null || {
    printf 'standalone restore proof requires setsid\n' >&2
    exit 1
  }

  restore_proof_dir="$output_dir/restore-proof"
  mkdir -p "$restore_proof_dir"
  kopia_helper_image="$(jq -er '.services["kopia-agent"].image' "$runtime_compose_config")"
  first_volume="$(
    jq -er '
      [
        .volumes
        | keys[]
        | select(startswith("kopia-") | not)
        | "stackkit-basement-core_" + .
      ]
      | sort
      | if length > 0 then .[0]
        else error("Basement runtime has no managed backup volumes")
        end
    ' "$runtime_compose_config"
  )"
  sentinel_path="stackkit-restore-proof/sentinel"
  before_activation="before-activation"
  current_live="current-live"

  timeout 60 docker run --rm --network none --entrypoint /bin/sh \
    --mount "type=volume,src=$first_volume,dst=/target" \
    "$kopia_helper_image" -ceu '
      sentinel=$1
      expected=$2
      mkdir -p "/target/$(dirname "$sentinel")" "/target/stackkit-restore-proof/files"
      printf "%s\n" "$expected" >"/target/$sentinel"
      i=0
      while [ "$i" -lt 12000 ]; do
        printf "restore-proof-%05d\n" "$i" >"/target/stackkit-restore-proof/files/file-$(printf "%05d" "$i")"
        i=$((i + 1))
      done
    ' -- "$sentinel_path" "$before_activation" >"$restore_proof_dir/seed.log"

  timeout 120 stackkit backup configure --json >"$restore_proof_dir/configure.json"
  jq -e '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "success"
    and .data.apiVersion == "stackkit.local-backup-configuration/v1"
  ' "$restore_proof_dir/configure.json" >/dev/null

  timeout 180 stackkit backup run \
    --operation-id restore-proof-anchor-a --json \
    >"$restore_proof_dir/anchor-a.json"
  anchor_a="$(
    jq -er '
      select(
        .schemaVersion == "stackkit.command-result/v1"
        and .status == "success"
        and .data.apiVersion == "stackkit.local-backup-snapshot-anchor/v1"
      )
      | .data.id
      | select(test("^sha256:[0-9a-f]{64}$"))
    ' "$restore_proof_dir/anchor-a.json"
  )"

  timeout 30 docker run --rm --network none --entrypoint /bin/sh \
    --mount "type=volume,src=$first_volume,dst=/target" \
    "$kopia_helper_image" -ceu '
      printf "%s\n" "$2" >"/target/$1"
    ' -- "$sentinel_path" "$current_live" >"$restore_proof_dir/mutate.log"

  timeout 180 stackkit backup restore "$anchor_a" \
    --operation-id restore-proof-stage-a --owner-approve --json \
    >"$restore_proof_dir/stage-a.json"
  restore_result_a="$(
    jq -er '
      select(
        .schemaVersion == "stackkit.command-result/v1"
        and .status == "success"
        and .data.apiVersion == "stackkit.local-backup-restore-result/v1"
      )
      | .data.id
      | select(test("^sha256:[0-9a-f]{64}$"))
    ' "$restore_proof_dir/stage-a.json"
  )"

  activation_operation_a="restore-proof-activate-a"
  setsid timeout 300 stackkit backup restore activate "$restore_result_a" \
    --operation-id "$activation_operation_a" --owner-approve --json \
    >"$restore_proof_dir/activate-a-interrupted.json" 2>&1 &
  restore_activation_pid=$!

  activation_journal="$project_dir/.stackkit/lifecycle-mutations/active.json"
  activation_ready=0
  for _ in $(seq 1 300); do
    if [ -s "$activation_journal" ] &&
      jq -e --arg operation "$activation_operation_a" --arg volume "$first_volume" '
        .apiVersion == "stackkit.lifecycle-mutation/v1"
        and .operationId == $operation
        and .kind == "restore-activation"
        and .status == "active"
        and .phase == "activation-copy-started"
        and .restoreActivation.authority.volumes[0] == $volume
        and .restoreActivation.inFlight.volume == $volume
      ' "$activation_journal" >/dev/null 2>&1; then
      restore_helper_name="$(
        printf '%s\0%s' "$activation_operation_a" "$first_volume" |
          sha256sum | cut -c1-16
      )"
      restore_helper_name="stackkit-restore-$restore_helper_name"
      if docker ps --quiet --filter "name=^/${restore_helper_name}$" | grep -q .; then
        activation_ready=1
        break
      fi
    fi
    kill -0 "$restore_activation_pid" 2>/dev/null || break
    sleep 0.1
  done
  [ "$activation_ready" = "1" ] || {
    printf 'restore proof did not observe the first activation copy and helper\n' >&2
    exit 1
  }

  docker pause "$restore_helper_name" >"$restore_proof_dir/pause-helper.log"
  kill -KILL -- "-$restore_activation_pid"
  wait "$restore_activation_pid" 2>/dev/null || true
  restore_activation_pid=
  docker unpause "$restore_helper_name" >"$restore_proof_dir/unpause-helper.log"
  docker rm -f "$restore_helper_name" >"$restore_proof_dir/remove-helper.log"
  restore_helper_name=

  if timeout 30 stackkit generate >"$restore_proof_dir/mutation-denial.log" 2>&1; then
    printf 'ordinary mutation unexpectedly succeeded during interrupted restore activation\n' >&2
    exit 1
  fi
  denial_count="$(grep -c 'ordinary mutation is denied' "$restore_proof_dir/mutation-denial.log" || true)"
  [ "$denial_count" -eq 1 ] || {
    printf 'interrupted restore activation did not produce exactly one ordinary mutation denial\n' >&2
    exit 1
  }

  timeout 300 stackkit backup restore recover "$activation_operation_a" \
    --owner-approve --rollback --json \
    >"$restore_proof_dir/recover-a.json"
  jq -e '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "success"
    and .data.apiVersion == "stackkit.restore-activation-result/v1"
    and .data.status == "recovered"
    and .data.verification.servicesVerified == true
  ' "$restore_proof_dir/recover-a.json" >/dev/null
  recovered_sentinel="$(
    timeout 30 docker run --rm --network none --entrypoint /bin/sh \
      --mount "type=volume,src=$first_volume,dst=/target,readonly" \
      "$kopia_helper_image" -ceu 'cat "/target/$1"' -- "$sentinel_path"
  )"
  [ "$recovered_sentinel" = "$current_live" ] || {
    printf 'restore recovery did not retain the current-live sentinel\n' >&2
    exit 1
  }

  activation_operation_b="restore-proof-activate-b"
  timeout 300 stackkit backup restore activate "$restore_result_a" \
    --operation-id "$activation_operation_b" --owner-approve --json \
    >"$restore_proof_dir/activate-b.json"
  jq -e '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "success"
    and .data.apiVersion == "stackkit.restore-activation-result/v1"
    and .data.status == "activated"
    and .data.verification.servicesVerified == true
  ' "$restore_proof_dir/activate-b.json" >/dev/null
  activated_sentinel="$(
    timeout 30 docker run --rm --network none --entrypoint /bin/sh \
      --mount "type=volume,src=$first_volume,dst=/target,readonly" \
      "$kopia_helper_image" -ceu 'cat "/target/$1"' -- "$sentinel_path"
  )"
  [ "$activated_sentinel" = "$before_activation" ] || {
    printf 'successful restore activation did not restore the Anchor A sentinel\n' >&2
    exit 1
  }
  [ -z "$(
    docker volume ls --quiet \
      --filter "label=io.stackkit.restore.role=rollback" \
      --filter "label=io.stackkit.restore.operation=$activation_operation_a"
  )" ] && [ -z "$(
    docker volume ls --quiet \
      --filter "label=io.stackkit.restore.role=rollback" \
      --filter "label=io.stackkit.restore.operation=$activation_operation_b"
  )" ] || {
    printf 'restore proof left rollback volumes behind\n' >&2
    exit 1
  }

  timeout 120 stackkit verify --json >"$restore_proof_dir/final-verify.json"
  jq -e '
    .schemaVersion == "stackkit.command-result/v1"
    and .status == "success"
  ' "$restore_proof_dir/final-verify.json" >/dev/null
  restore_completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  restore_live_evidence="$output_dir/restore-live-evidence.json"
  restore_live_evidence_pending="$work_root/restore-live-evidence.pending.json"
  jq -n \
    --arg sourceCommit "$source_commit" \
    --arg sourceDigest "$source_digest" \
    --arg archiveName "$archive_name" \
    --arg archiveSha256 "$(sha256sum "$archive" | cut -d' ' -f1)" \
    --arg sbomSha256 "$(sha256sum "$sbom" | cut -d' ' -f1)" \
    --arg snapshotAnchorId "$anchor_a" \
    --arg restoreResultId "$restore_result_a" \
    --arg interruptedActivationId "$activation_operation_a" \
    --arg recoveredActivationId "$activation_operation_a" \
    --arg completedActivationId "$activation_operation_b" \
    --arg firstVolume "$first_volume" \
    --arg recoveredSentinel "$recovered_sentinel" \
    --arg activatedSentinel "$activated_sentinel" \
    --arg completedAt "$restore_completed_at" \
    --arg applySha256 "$(sha256sum "$output_dir/apply.log" | cut -d' ' -f1)" \
    --arg verifySha256 "$(sha256sum "$output_dir/verify.json" | cut -d' ' -f1)" \
    --arg seedSha256 "$(sha256sum "$restore_proof_dir/seed.log" | cut -d' ' -f1)" \
    --arg configureSha256 "$(sha256sum "$restore_proof_dir/configure.json" | cut -d' ' -f1)" \
    --arg anchorSha256 "$(sha256sum "$restore_proof_dir/anchor-a.json" | cut -d' ' -f1)" \
    --arg mutateSha256 "$(sha256sum "$restore_proof_dir/mutate.log" | cut -d' ' -f1)" \
    --arg stageSha256 "$(sha256sum "$restore_proof_dir/stage-a.json" | cut -d' ' -f1)" \
    --arg interruptedSha256 "$(sha256sum "$restore_proof_dir/activate-a-interrupted.json" | cut -d' ' -f1)" \
    --arg pauseSha256 "$(sha256sum "$restore_proof_dir/pause-helper.log" | cut -d' ' -f1)" \
    --arg unpauseSha256 "$(sha256sum "$restore_proof_dir/unpause-helper.log" | cut -d' ' -f1)" \
    --arg removeSha256 "$(sha256sum "$restore_proof_dir/remove-helper.log" | cut -d' ' -f1)" \
    --arg denialSha256 "$(sha256sum "$restore_proof_dir/mutation-denial.log" | cut -d' ' -f1)" \
    --arg recoverSha256 "$(sha256sum "$restore_proof_dir/recover-a.json" | cut -d' ' -f1)" \
    --arg activatedSha256 "$(sha256sum "$restore_proof_dir/activate-b.json" | cut -d' ' -f1)" \
    --arg finalVerifySha256 "$(sha256sum "$restore_proof_dir/final-verify.json" | cut -d' ' -f1)" \
    '{
      schemaVersion: "stackkit.restore-live-e2e-evidence/v1",
      status: "pass",
      source: {
        repository: "kombifyio/stackKits",
        commit: $sourceCommit,
        digest: $sourceDigest
      },
      archive: {
        name: $archiveName,
        sha256: $archiveSha256,
        sbomSha256: $sbomSha256
      },
      authority: {
        snapshotAnchorId: $snapshotAnchorId,
        restoreResultId: $restoreResultId,
        interruptedActivationId: $interruptedActivationId,
        recoveredActivationId: $recoveredActivationId,
        completedActivationId: $completedActivationId,
        firstActivatedVolume: $firstVolume
      },
      proof: {
        recoveredSentinel: $recoveredSentinel,
        activatedSentinel: $activatedSentinel,
        mutationDenied: true,
        rollbackVolumesRemaining: 0,
        servicesVerified: true,
        finalVerifySucceeded: true,
        completedAt: $completedAt
      },
      runtimeEvidenceInputs: {
        applySha256: $applySha256,
        verifySha256: $verifySha256
      },
      outputs: [
        {name: "restore-proof/seed.log", sha256: $seedSha256},
        {name: "restore-proof/configure.json", sha256: $configureSha256},
        {name: "restore-proof/anchor-a.json", sha256: $anchorSha256},
        {name: "restore-proof/mutate.log", sha256: $mutateSha256},
        {name: "restore-proof/stage-a.json", sha256: $stageSha256},
        {name: "restore-proof/activate-a-interrupted.json", sha256: $interruptedSha256},
        {name: "restore-proof/pause-helper.log", sha256: $pauseSha256},
        {name: "restore-proof/unpause-helper.log", sha256: $unpauseSha256},
        {name: "restore-proof/remove-helper.log", sha256: $removeSha256},
        {name: "restore-proof/mutation-denial.log", sha256: $denialSha256},
        {name: "restore-proof/recover-a.json", sha256: $recoverSha256},
        {name: "restore-proof/activate-b.json", sha256: $activatedSha256},
        {name: "restore-proof/final-verify.json", sha256: $finalVerifySha256}
      ]
    }' >"$restore_live_evidence_pending"
  jq -e \
    --arg sourceCommit "$source_commit" \
    --arg sourceDigest "$source_digest" \
    --arg archiveName "$archive_name" \
    --arg archiveSha256 "$(sha256sum "$archive" | cut -d' ' -f1)" \
    --arg sbomSha256 "$(sha256sum "$sbom" | cut -d' ' -f1)" \
    --arg snapshotAnchorId "$anchor_a" \
    --arg restoreResultId "$restore_result_a" \
    --arg interruptedActivationId "$activation_operation_a" \
    --arg completedActivationId "$activation_operation_b" \
    --arg recoveredSentinel "$current_live" \
    --arg activatedSentinel "$before_activation" '
      .schemaVersion == "stackkit.restore-live-e2e-evidence/v1"
      and .status == "pass"
      and .source == {
        repository: "kombifyio/stackKits",
        commit: $sourceCommit,
        digest: $sourceDigest
      }
      and .archive.name == $archiveName
      and .archive.sha256 == $archiveSha256
      and .archive.sbomSha256 == $sbomSha256
      and .authority.snapshotAnchorId == $snapshotAnchorId
      and .authority.restoreResultId == $restoreResultId
      and .authority.interruptedActivationId == $interruptedActivationId
      and .authority.recoveredActivationId == $interruptedActivationId
      and .authority.completedActivationId == $completedActivationId
      and .proof.recoveredSentinel == $recoveredSentinel
      and .proof.activatedSentinel == $activatedSentinel
      and .proof.mutationDenied == true
      and .proof.rollbackVolumesRemaining == 0
      and .proof.servicesVerified == true
      and .proof.finalVerifySucceeded == true
      and (.proof.completedAt | type == "string" and endswith("Z"))
      and (.runtimeEvidenceInputs.applySha256 | test("^[0-9a-f]{64}$"))
      and (.runtimeEvidenceInputs.verifySha256 | test("^[0-9a-f]{64}$"))
      and (.outputs | length == 13)
      and all(.outputs[];
        (.name | test("^restore-proof/[a-z0-9.-]+$"))
        and (.sha256 | test("^[0-9a-f]{64}$"))
      )
    ' "$restore_live_evidence_pending" >/dev/null
  while IFS=$'\t' read -r evidence_name evidence_sha256; do
    [ "$(sha256sum "$output_dir/$evidence_name" | cut -d' ' -f1)" = "$evidence_sha256" ] || {
      printf 'restore proof output differs from restore-live evidence: %s\n' "$evidence_name" >&2
      exit 1
    }
  done < <(jq -r '.outputs[] | [.name, .sha256] | @tsv' "$restore_live_evidence_pending")
  printf 'standalone restore proof passed\n'
fi

if [ "$stable_day2_proof" = "1" ]; then
  runtime_finished_epoch="$base_runtime_finished_epoch"
  runtime_finished="$base_runtime_finished"
else
  runtime_finished_epoch="$(date +%s)"
  runtime_finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi
runtime_duration=$((runtime_finished_epoch - runtime_started_epoch))
[ "$runtime_duration" -le 600 ] || {
  printf 'standalone runtime phase exceeded 600 seconds\n' >&2
  exit 1
}

kill -INT "$capture_pid"
wait "$capture_pid" || true
capture_pid=
node "$script_dir/parse-standalone-traffic.mjs" "$raw_traffic" "$network_scope" "$traffic_events"

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
  --arg releaseIndexAttestationSha256 "$(evidence_digest "$release_index_attestation")" \
  --arg trustedRootSha256 "$(evidence_digest "$trusted_root")" \
  --arg releaseBootstrapSha256 "$(evidence_digest "$output_dir/release-bootstrap.json")" \
  --arg sourceCommit "$source_commit" \
  --arg sourceDigest "$source_digest" \
  --arg startedAt "$runtime_started" \
  --arg finishedAt "$runtime_finished" \
  --argjson durationSeconds "$runtime_duration" \
  --arg trafficSha256 "$(evidence_digest "$traffic_events")" \
  --arg originScopeSha256 "$(evidence_digest "$network_scope")" \
  --argjson eventCount "$event_count" \
  --arg applySha256 "$(evidence_digest "$output_dir/apply.log")" \
  --arg verifySha256 "$(evidence_digest "$output_dir/verify.json")" \
  '{
    schemaVersion: "stackkit.oss-runtime-e2e-evidence/v3",
    source: {repository: "kombifyio/stackKits", commit: $sourceCommit, digest: $sourceDigest},
    archive: {
      name: $archiveName, sha256: $archiveSha256, sbomSha256: $sbomSha256,
      attestationSha256: $attestationSha256, releaseIndexSha256: $releaseIndexSha256,
      releaseIndexAttestationSha256: $releaseIndexAttestationSha256,
      trustedRootSha256: $trustedRootSha256,
      releaseBootstrapSha256: $releaseBootstrapSha256
    },
    network: {
      recorder: "stackkit.hermetic-network-log/v2",
      captureMode: "host-forbidden-dns+compose-origin-initial-syn/v1",
      eventsSha256: $trafficSha256,
      originScopeSha256: $originScopeSha256,
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
  "$output_dir/runtime-evidence.json" "$traffic_events" "$network_scope"
if [ "${STACKKIT_E2E_RESTORE_PROOF:-0}" = "1" ]; then
  runtime_evidence_sha256="$(evidence_digest "$output_dir/runtime-evidence.json")"
  restore_evidence_next="$work_root/restore-live-evidence.json"
  jq --arg runtimeSha256 "$runtime_evidence_sha256" '
    . + {
      runtimeEvidence: {
        name: "runtime-evidence.json",
        sha256: $runtimeSha256
      }
    }
  ' "$restore_live_evidence_pending" >"$restore_evidence_next"
  mv "$restore_evidence_next" "$restore_live_evidence"
  jq -e --arg runtimeSha256 "$runtime_evidence_sha256" '
    .schemaVersion == "stackkit.restore-live-e2e-evidence/v1"
    and .status == "pass"
    and .runtimeEvidence == {
      name: "runtime-evidence.json",
      sha256: $runtimeSha256
    }
  ' "$restore_live_evidence" >/dev/null
  [ "$(evidence_digest "$output_dir/runtime-evidence.json")" = "$runtime_evidence_sha256" ] || {
    printf 'runtime evidence changed while binding restore-live evidence\n' >&2
    exit 1
  }
fi
printf 'standalone OSS runtime E2E passed: %s\n' "$output_dir/runtime-evidence.json"
