#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 6 ]; then
  printf 'usage: %s <runtime|ha> <archive> <tag> <source-commit> <source-digest> <output>\n' "$0" >&2
  exit 2
fi
phase="$1"
archive="$(realpath "$2")"
tag="$3"
commit="$4"
source_digest="$5"
output="$(realpath -m "$6")"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d)"
extract="$work/extract"
project="$work/project"
home="$work/home"
mkdir -p "$extract" "$project" "$home/.stackkits" "$output"
state="$output/modern-runtime-state.json"

cleanup() {
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

tar -xzf "$archive" -C "$extract"
stackkit="$(find "$extract" -maxdepth 2 -type f -name stackkit -print -quit)"
test -n "$stackkit"
chmod +x "$stackkit"
for directory in base modules cue.mod modern-homelab; do
  cp -R "$extract/$directory" "$home/.stackkits/"
done
cp -R "$extract/base" "$home/.stackkits/modern-homelab/"
export HOME="$home"
export PATH="$(dirname "$stackkit"):$PATH"
cd "$project"

timeout 30 stackkit init modern-homelab --owner-source=local \
  --domain modern.live.test --non-interactive >"$output/init.log" 2>&1
timeout 15 stackkit validate >"$output/validate.log" 2>&1
timeout 30 stackkit generate >"$output/generate-initial.log" 2>&1
plan="$project/deploy/.stackkit/resolved-plan.json"
test -s "$plan"
cp "$plan" "$output/resolved-plan-before-binding.json"
jq -e '
  .executionReadiness.apply.status == "blocked" and
  ([.executionReadiness.apply.blockers[].code] |
    index("external-federation-link-binding-missing")) != null
' "$plan" >/dev/null

export STACKKIT_HERMETIC_LIVE_PROOF=1
admission="$project/.stackkit/custody/federation-link-proof.json"
inventory="$project/.stackkit/inventory.json"
timeout 20 stackkit internal proof federation-binding issue \
  --resolved-plan "$plan" --candidate-digest "$source_digest" --valid-for 10m \
  --output "$admission" --allow-hermetic-proof >"$output/binding-issue.log" 2>&1
timeout 20 stackkit federation binding import \
  --admission "$admission" --inventory "$inventory" --allow-hermetic-proof \
  >"$output/binding-import.log" 2>&1
cp "$admission" "$output/federation-binding-admission.json"

timeout 90 bash "$script_dir/prepare-modern-runtime-process.sh" \
  "$project" "$inventory" "$output" "$tag" "$commit" "$source_digest"
cp "$inventory" "$output/inventory.json"
timeout 30 stackkit generate --inventory "$inventory" >"$output/generate.log" 2>&1
cp "$plan" "$output/resolved-plan.json"
jq -e '
  .executionReadiness.apply.status == "ready" and
  (.executionReadiness.apply.blockers | length) == 0 and
  .controlPlane.mode == "warm-standby" and
  .availability.mode == "warm-standby"
' "$plan" >/dev/null
timeout 120 stackkit apply --inventory "$inventory" --auto-approve >"$output/apply.log" 2>&1
apply="$(find "$project/.stackkit/evidence/apply/results" -maxdepth 1 -type f -name '*.json' -print -quit)"
test -s "$apply"
cp "$apply" "$output/apply-result.json"
timeout 45 stackkit verify --inventory "$inventory" --json >"$output/verify.json"
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
