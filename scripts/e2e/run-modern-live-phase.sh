#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 7 ]; then
  printf 'usage: %s <runtime|ha> <archive> <release-trust-dir> <tag> <source-commit> <source-digest> <output>\n' "$0" >&2
  exit 2
fi
phase="$1"
archive="$(realpath "$2")"
release_trust_dir="$(realpath "$3")"
tag="$4"
commit="$5"
source_digest="$6"
output="$(realpath -m "$7")"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source_root="$(cd "$script_dir/../.." && pwd)"
work="$(mktemp -d)"
extract="$work/extract"
project="$work/project"
home="$work/home"
fixture="$work/release-fixture"
internal_stackkit="$work/internal-helper/stackkit"
fixture_pid=
mkdir -p "$extract" "$project" "$home/.stackkits" "$fixture" "$(dirname "$internal_stackkit")" "$output"
state="$output/modern-runtime-state.json"

cleanup() {
  if [ -s "$project/.stackkit/evidence/modern-runtime-process/last-error.log" ]; then
    cp "$project/.stackkit/evidence/modern-runtime-process/last-error.log" \
      "$output/modern-runtime-process-error.log"
  fi
  if [ -n "${fixture_pid:-}" ]; then
    kill "$fixture_pid" >/dev/null 2>&1 || true
    wait "$fixture_pid" 2>/dev/null || true
  fi
  if [ -s "$state" ]; then
    docker rm -f \
      "$(jq -r .containers.homeMain "$state")" \
      "$(jq -r .containers.homeStandby "$state")" \
      "$(jq -r .containers.cloudEdge "$state")" >/dev/null 2>&1 || true
    docker network rm "$(jq -r .network "$state")" >/dev/null 2>&1 || true
    docker image rm "$(jq -r .image "$state")" >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT
test "$phase" = runtime || test "$phase" = ha
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$commit" =~ ^[0-9a-f]{40}$ ]]
[[ "$source_digest" =~ ^sha256:[0-9a-f]{64}$ ]]
test "$(git -C "$source_root" rev-parse --verify HEAD)" = "$commit"
git -C "$source_root" diff --quiet HEAD -- cmd internal go.mod go.sum
(
  cd "$source_root"
  CGO_ENABLED=0 GOFLAGS=-mod=readonly go build -trimpath -tags stackkit_e2e \
    -ldflags="-s -w -X main.Version=${tag#v} -X main.GitCommit=$commit" \
    -o "$internal_stackkit" ./cmd/stackkit
)
test -x "$internal_stackkit"

release_index="$release_trust_dir/stackkits-release-index-v1.json"
release_index_attestation="$release_trust_dir/stackkits-release-index-v1.json.intoto.jsonl"
test -s "$release_index"
test -s "$release_index_attestation"
archive_name="$(basename "$archive")"
asset="$(
  jq -cer --arg archive "$archive_name" --arg tag "$tag" '
    [
      .assets[]
      | select(
          .kit == "modern-homelab"
          and .version == $tag
          and .platform.os == "linux"
          and .platform.arch == "amd64"
          and .archive.name == $archive
        )
    ]
    | if length == 1 then .[0] else error("exact Modern release asset is ambiguous") end
  ' "$release_index"
)"
sbom_name="$(jq -er .sbom.name <<<"$asset")"
attestation_name="$(jq -er .attestation.name <<<"$asset")"
trusted_root_name="$(jq -er .release.trustedRoot.name "$release_index")"
for input in \
  "$archive" \
  "$release_trust_dir/$sbom_name" \
  "$release_trust_dir/$attestation_name" \
  "$release_trust_dir/$trusted_root_name"; do
  test -s "$input"
  expected="$(
    jq -er --arg name "$(basename "$input")" '
      [
        [.release.trustedRoot, .assets[].archive, .assets[].sbom, .assets[].attestation]
        | flatten | .[] | select(.name == $name) | .sha256
      ]
      | unique
      | if length == 1 then .[0] else error("release fixture asset digest is ambiguous") end
    ' "$release_index"
  )"
  test "$(sha256sum "$input" | cut -d' ' -f1)" = "$expected"
  cp "$input" "$fixture/"
done
cp "$release_index" "$fixture/stackkits-release-index-v1.json"
cp "$release_index_attestation" "$fixture/stackkits-release-index-v1.json.intoto.jsonl"
fixture_port="${STACKKIT_E2E_FIXTURE_PORT:-18080}"
node "$script_dir/github-release-fixture.mjs" "$fixture" "$fixture_port" \
  >"$output/fixture-url.txt" 2>"$output/fixture.log" &
fixture_pid=$!
for _ in $(seq 1 50); do
  [ -s "$output/fixture-url.txt" ] && break
  kill -0 "$fixture_pid" 2>/dev/null || {
    printf 'Modern release fixture exited before readiness\n' >&2
    exit 1
  }
  sleep 0.1
done
fixture_url="$(head -n1 "$output/fixture-url.txt")"
test -n "$fixture_url"

tar -xzf "$archive" -C "$extract"
release_stackkit="$(find "$extract" -maxdepth 2 -type f -name stackkit -print -quit)"
test -n "$release_stackkit"
chmod +x "$release_stackkit"
for directory in base modules cue.mod modern-homelab; do
  cp -R "$extract/$directory" "$home/.stackkits/"
done
cp -R "$extract/base" "$home/.stackkits/modern-homelab/"
export HOME="$home"
export PATH="$(dirname "$release_stackkit"):$PATH"
export STACKKIT_RELEASE_FIXTURE_URL="$fixture_url"
cd "$project"

timeout 30 "$release_stackkit" init modern-homelab --owner-source=local \
  --domain modern.live.test --non-interactive >"$output/init.log" 2>&1
timeout 30 "$release_stackkit" kit verify --json >"$output/release-bootstrap.json"
jq -e --arg version "$tag" '
  .schemaVersion == "stackkit.command-result/v1"
  and .status == "success"
  and (.data | length) == 1
  and .data[0].kit == "modern-homelab"
  and .data[0].version == $version
  and .data[0].platform.os == "linux"
  and .data[0].platform.arch == "amd64"
' "$output/release-bootstrap.json" >/dev/null
timeout 15 "$release_stackkit" validate >"$output/validate.log" 2>&1
timeout 30 "$release_stackkit" generate >"$output/generate-initial.log" 2>&1
plan="$project/deploy/.stackkit/resolved-plan.json"
test -s "$plan"
cp "$plan" "$output/resolved-plan-before-binding.json"
jq -e '
  .executionReadiness.apply.status == "blocked" and
  ([.executionReadiness.apply.blockers[].code] |
    index("external-federation-link-binding-missing")) != null
' "$plan" >/dev/null

export STACKKIT_HERMETIC_LIVE_PROOF=1
admission="$project/.stackkit/evidence/federation-binding/proof.json"
inventory="$project/.stackkit/inventory.json"
timeout 20 "$internal_stackkit" internal proof federation-binding issue \
  --resolved-plan "$plan" --candidate-digest "$source_digest" --valid-for 10m \
  --output "$admission" --allow-hermetic-proof >"$output/binding-issue.log" 2>&1
timeout 20 "$internal_stackkit" federation binding import \
  --admission "$admission" --inventory "$inventory" --resolved-plan "$plan" \
  --allow-hermetic-proof \
  >"$output/binding-import.log" 2>&1
cp "$admission" "$output/federation-binding-admission.json"

timeout 90 bash "$script_dir/prepare-modern-runtime-process.sh" \
  "$project" "$inventory" "$output" "$tag" "$commit" "$source_digest"
cp "$inventory" "$output/inventory.json"
timeout 30 "$release_stackkit" generate --inventory "$inventory" >"$output/generate.log" 2>&1
cp "$plan" "$output/resolved-plan.json"
jq -e '
  .executionReadiness.apply.status == "ready" and
  (.executionReadiness.apply.blockers | length) == 0 and
  .controlPlane.mode == "warm-standby" and
  .availability.mode == "warm-standby"
' "$plan" >/dev/null
timeout 120 "$release_stackkit" apply --inventory "$inventory" --auto-approve >"$output/apply.log" 2>&1
apply="$(find "$project/.stackkit/evidence/apply/results" -maxdepth 1 -type f -name '*.json' -print -quit)"
test -s "$apply"
cp "$apply" "$output/apply-result.json"
timeout 45 "$release_stackkit" verify --inventory "$inventory" --json >"$output/verify.json"
jq -e '.schemaVersion=="stackkit.command-result/v1" and .status=="success"' "$output/verify.json" >/dev/null
cp "$project/.stackkit/evidence/modern-runtime-process/transcript.json" \
  "$output/modern-runtime-process-transcript.json"

main_url="$(jq -r .urls.homeMain "$state")"
standby_url="$(jq -r .urls.homeStandby "$state")"
edge_container="$(jq -r .containers.cloudEdge "$state")"
network="$(jq -r .network "$state")"
if [ "$phase" = runtime ]; then
  docker network disconnect "$network" "$edge_container"
  home_status="$(curl -fsS "$main_url/healthz")"
  disconnected="$(docker network inspect "$network" \
    --format '{{json .Containers}}' | jq --arg id "$(docker inspect -f '{{.Id}}' "$edge_container")" \
    'has($id) | not')"
  docker network connect --alias cloud-edge "$network" "$edge_container"
  curl -fsS "$(jq -r .urls.cloudEdge "$state")/healthz" >/dev/null
  jq -n --argjson home "$home_status" --argjson denied "$disconnected" '{
    apiVersion:"stackkit.modern-partition-live-evidence/v1",
    failClosed:$denied,homeContinued:($home.site == "home"),reconnected:true
  }' >"$output/docker-partition-evidence.json"
  jq -n --arg tag "$tag" --arg commit "$commit" --arg digest "$source_digest" '{
    apiVersion:"stackkit.modern-runtime-live-receipt/v2",kind:"ModernRuntimeLiveReceipt",
    status:"pass",source:{tag:$tag,commit:$commit,digest:$digest}
  }' >"$output/modern-runtime-live-receipt.json"
  jq -n --arg tag "$tag" --arg commit "$commit" --arg digest "$source_digest" \
    --arg archive "sha256:$(sha256sum "$archive"|cut -d" " -f1)" '{
      schemaVersion:"stackkit.modern-archive-live-proof-evidence/v3",proofStatus:"pass",
      release:{tag:$tag,source:{commit:$commit,digest:$digest},archive:{sha256:$archive}},
      runtime:{binarySource:"published-release-archive"}
    }' >"$output/modern-archive-live-proof-evidence.json"
else
  main_container="$(jq -r .containers.homeMain "$state")"
  commit_started="$(date +%s%N)"
  committed="$(curl -fsS -X POST "$main_url/commit")"
  sequence="$(jq -r .sequence <<<"$committed")"
  standby_before="$(curl -fsS "$standby_url/state")"
  fault_started="$(date +%s%N)"
  docker stop --time 5 "$main_container" >/dev/null
  fenced_at="$(date +%s%N)"
  promoted="$(curl -fsS -X POST "$standby_url/promote")"
  ready_at="$(date +%s%N)"
  standby_sequence="$(jq -r .sequence <<<"$promoted")"
  test "$standby_sequence" -eq "$sequence"
  rpo=0
  rto="$(awk "BEGIN {print ($ready_at-$fault_started)/1000000000}")"
  image="$(jq -r .image "$state")"
  docker rm "$main_container" >/dev/null
  docker run -d --name "$main_container" --network "$network" \
    -p 127.0.0.1::8080 --read-only --cap-drop ALL --security-opt no-new-privileges \
    -e STACKKIT_SITE=home -e STACKKIT_ROLE=standby "$image" >/dev/null
  recovered_port="$(docker port "$main_container" 8080/tcp | sed -E 's/.*:([0-9]+)$/\1/' | tail -n1)"
  recovered_url="http://127.0.0.1:${recovered_port}"
  recovered=false
  for _ in $(seq 1 80); do
    if curl -fsS -X POST "${recovered_url}/replicate?sequence=${standby_sequence}" \
      >"$output/recovered-main.json"; then recovered=true; break; fi
    sleep .1
  done
  test "$recovered" = true
  jq -e --argjson sequence "$standby_sequence" \
    '.role=="standby" and .sequence==$sequence' "$output/recovered-main.json" >/dev/null
  jq -n --arg tag "$tag" --arg commit "$commit" --arg digest "$source_digest" \
    --argjson sequence "$sequence" --argjson standbySequence "$standby_sequence" \
    --argjson rpo "$rpo" --argjson rto "$rto" \
    --arg commitStarted "$commit_started" --arg faultStarted "$fault_started" \
    --arg fencedAt "$fenced_at" --arg readyAt "$ready_at" '{
      apiVersion:"stackkit.modern-warm-standby-live-receipt/v2",
      kind:"ModernWarmStandbyLiveReceipt",status:"pass",
      source:{tag:$tag,commit:$commit,digest:$digest},
      sequence:{active:$sequence,standby:$standbySequence},
      timeline:{commitStartedNs:$commitStarted,faultStartedNs:$faultStarted,fencedAtNs:$fencedAt,readyAtNs:$readyAt},
      fault:{member:"home-main",fencedBeforeFailover:true},
      metrics:{rpoSeconds:$rpo,rtoSeconds:$rto},
      recovery:{status:"pass"}
    }' >"$output/modern-warm-standby-live-receipt.json"
fi
