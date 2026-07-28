#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 6 ]; then
  printf 'usage: %s <project> <inventory> <output> <tag> <source-commit> <source-digest>\n' "$0" >&2
  exit 2
fi
project="$(realpath "$1")"
inventory="$(realpath "$2")"
output="$(realpath -m "$3")"
tag="$4"
commit="$5"
source_digest="$6"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
mkdir -p "$output" "$project/.stackkit/custody" "$project/.stackkit/evidence/modern-runtime-process"

suffix="${GITHUB_RUN_ID:-local}-${RANDOM}"
image="stackkits-modern-live:${commit:0:12}"
network="stackkits-modern-${suffix}"
main="stackkits-modern-main-${suffix}"
standby="stackkits-modern-standby-${suffix}"
edge="stackkits-modern-edge-${suffix}"
build="$(mktemp -d)"
trap 'rm -rf "$build"' EXIT

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$build/site" \
  "$repo_root/scripts/e2e/modern-live-site"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$output/modern-runtime-process" \
  "$repo_root/scripts/e2e/modern-runtime-process"
chmod 0755 "$output/modern-runtime-process"
process_sha="sha256:$(sha256sum "$output/modern-runtime-process" | cut -d' ' -f1)"
printf 'FROM scratch\nCOPY site /site\nUSER 65532:65532\nENTRYPOINT ["/site"]\n' >"$build/Dockerfile"
docker build --quiet -t "$image" "$build" >/dev/null
docker network create "$network" >/dev/null
docker run -d --name "$standby" --network "$network" --network-alias home-standby \
  -p 127.0.0.1::8080 --read-only --cap-drop ALL --security-opt no-new-privileges \
  -e STACKKIT_SITE=home -e STACKKIT_ROLE=standby "$image" >/dev/null
docker run -d --name "$main" --network "$network" \
  -p 127.0.0.1::8080 --read-only --cap-drop ALL --security-opt no-new-privileges \
  -e STACKKIT_SITE=home -e STACKKIT_ROLE=active \
  -e STACKKIT_STANDBY_URL=http://home-standby:8080 "$image" >/dev/null
docker run -d --name "$edge" --network "$network" --network-alias cloud-edge \
  -p 127.0.0.1::8080 --read-only --cap-drop ALL --security-opt no-new-privileges \
  -e STACKKIT_SITE=cloud -e STACKKIT_ROLE=edge "$image" >/dev/null

port() { docker port "$1" 8080/tcp | sed -E 's/.*:([0-9]+)$/\1/' | tail -n1; }
main_url="http://127.0.0.1:$(port "$main")"
standby_url="http://127.0.0.1:$(port "$standby")"
edge_url="http://127.0.0.1:$(port "$edge")"
for url in "$main_url" "$standby_url" "$edge_url"; do
  ready=false
  for _ in $(seq 1 80); do curl -fsS "$url/healthz" >/dev/null && ready=true && break; sleep .1; done
  test "$ready" = true
done

jq -n --arg tag "$tag" --arg commit "$commit" --arg digest "$source_digest" \
  --arg root "$project/.stackkit/evidence/modern-runtime-process" \
  --arg main "$main_url" --arg standby "$standby_url" --arg edge "$edge_url" '{
    apiVersion:"stackkit.modern-runtime-process-config/v1",
    source:{tag:$tag,commit:$commit,digest:$digest},
    evidenceRoot:$root,
    channels:{
      "local-home-main":{siteRef:"home",nodeRef:"home-main",url:$main},
      "local-home-standby":{siteRef:"home",nodeRef:"home-standby",url:$standby},
      "local-cloud-edge":{siteRef:"cloud",nodeRef:"cloud-edge",url:$edge}
    }
  }' >"$project/.stackkit/custody/modern-runtime-process.json"
chmod 0600 "$project/.stackkit/custody/modern-runtime-process.json"

tmp="${inventory}.tmp"
jq --arg exe "$(realpath "$output/modern-runtime-process")" --arg sha "$process_sha" '
  .executionChannels = {
    "local-home-main":{apiVersion:"stackkit.standard-execution-channel/v1",kind:"StandardExecutionChannel",channelRef:"local-home-main",siteRef:"home",nodeRef:"home-main",operationClass:"standard",operationsProcess:{executable:$exe,executableSha256:$sha}},
    "local-home-standby":{apiVersion:"stackkit.standard-execution-channel/v1",kind:"StandardExecutionChannel",channelRef:"local-home-standby",siteRef:"home",nodeRef:"home-standby",operationClass:"standard",operationsProcess:{executable:$exe,executableSha256:$sha}},
    "local-cloud-edge":{apiVersion:"stackkit.standard-execution-channel/v1",kind:"StandardExecutionChannel",channelRef:"local-cloud-edge",siteRef:"cloud",nodeRef:"cloud-edge",operationClass:"standard",operationsProcess:{executable:$exe,executableSha256:$sha}}
  }' "$inventory" >"$tmp"
mv "$tmp" "$inventory"
jq -n --arg image "$image" --arg network "$network" --arg main "$main" \
  --arg standby "$standby" --arg edge "$edge" --arg mainURL "$main_url" \
  --arg standbyURL "$standby_url" --arg edgeURL "$edge_url" '{
    image:$image,network:$network,
    containers:{homeMain:$main,homeStandby:$standby,cloudEdge:$edge},
    urls:{homeMain:$mainURL,homeStandby:$standbyURL,cloudEdge:$edgeURL}
  }' >"$output/modern-runtime-state.json"
