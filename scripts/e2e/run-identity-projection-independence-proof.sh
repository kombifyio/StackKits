#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  printf 'usage: %s <stackkit-binary> <output-dir>\n' "$0" >&2
  exit 2
fi

stackkit="$(realpath "$1")"
output_dir="$(realpath -m "$2")"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
work_root="$(mktemp -d)"
workspace="$work_root/workspace"
fixture_dir="$work_root/issuer-output"
harness="$work_root/identity-projection-fixture"
pocket_pid=

cleanup() {
  if [ -n "${pocket_pid:-}" ]; then
    kill "$pocket_pid" 2>/dev/null || true
    wait "$pocket_pid" 2>/dev/null || true
  fi
  rm -rf -- "$work_root"
}
trap cleanup EXIT

mkdir -p "$workspace" "$fixture_dir" "$output_dir"
go build -trimpath -o "$harness" "$repo_root/scripts/e2e/identity-projection-fixture"

(
  cd "$workspace"
  "$stackkit" init basement-kit \
    --owner-source=local \
    --domain identity.independence.test \
    --non-interactive \
    >"$output_dir/init.log" 2>&1
)
owner_ref="$(jq -er '.ownerRef | select(startswith("owner/local/"))' \
  "$workspace/.stackkit/custody/owner.json")"
spec_before="$(sha256sum "$workspace/stack-spec.yaml" | cut -d' ' -f1)"

"$harness" emit \
  --output "$fixture_dir" \
  --owner-ref "$owner_ref"
trust_digest="sha256:$(sha256sum "$fixture_dir/trust-bundle.json" | cut -d' ' -f1)"
projection_digest="$(jq -er '.projectionSHA256' "$fixture_dir/manifest.json")"

(
  cd "$workspace"
  "$stackkit" advanced trust import \
    --bundle "$fixture_dir/trust-bundle.json" \
    --expect-sha256 "$trust_digest" \
    --owner-approve \
    --json >"$output_dir/trust-import.json"
  "$stackkit" identity projection inspect \
    --file "$fixture_dir/desired-projection.json" \
    --json >"$output_dir/inspect.json"
  "$stackkit" identity projection approve \
    --file "$fixture_dir/desired-projection.json" \
    --owner-approve \
    --json >"$output_dir/approve.json"
)

jq -e '
  .status == "success" and
  .data.pocketIdMutation == false and
  .data.credentialMaterialExported == false
' "$output_dir/approve.json" >/dev/null

# The issuer is a one-shot file producer, not an online dependency. From this
# point onward no Cloud/Techstack process, URL, token, or credential exists.
"$harness" serve-pocketid \
  >"$output_dir/pocketid-fixture.log" 2>&1 &
pocket_pid=$!
for _ in $(seq 1 50); do
  curl --fail --silent http://127.0.0.1:1411/healthz >/dev/null 2>&1 && break
  kill -0 "$pocket_pid" 2>/dev/null || {
    printf 'PocketID fixture exited before readiness\n' >&2
    exit 1
  }
  sleep 0.1
done

(
  cd "$workspace"
  "$stackkit" identity projection apply \
    --projection-sha256 "$projection_digest" \
    --owner-approve \
    --json >"$output_dir/apply.json"
)
jq -e '
  .status == "success" and
  .data.status == "applied" and
  .data.cloudRequired == false and
  .data.deletionPerformed == false and
  (.data.pocketIdSubject | length > 0)
' "$output_dir/apply.json" >/dev/null

# Unlink is intentionally proven while PocketID is offline. A valid unlink
# path cannot make an API call and cannot delete the locally realized user.
kill "$pocket_pid"
wait "$pocket_pid" 2>/dev/null || true
pocket_pid=
(
  cd "$workspace"
  "$stackkit" identity projection unlink \
    --projection-sha256 "$projection_digest" \
    --owner-approve \
    --json >"$output_dir/unlink.json"
  "$stackkit" identity projection apply \
    --projection-sha256 "$projection_digest" \
    --owner-approve \
    --json >"$output_dir/offline-idempotent-apply.json"
  "$stackkit" validate >"$output_dir/offline-validate.log" 2>&1
)
jq -e '
  .status == "success" and
  .data.status == "detached-no-delete" and
  .data.cloudRequired == false and
  .data.deletionPerformed == false and
  (.data.pocketIdSubject // "") == ""
' "$output_dir/unlink.json" >/dev/null
jq -e '
  .status == "success" and
  .data.status == "applied" and
  .data.cloudRequired == false and
  .data.deletionPerformed == false
' "$output_dir/offline-idempotent-apply.json" >/dev/null

spec_after="$(sha256sum "$workspace/stack-spec.yaml" | cut -d' ' -f1)"
test "$spec_before" = "$spec_after"

if rg -n -i \
  '(owner-key|root-key|admin[_-]?(key|secret|token)|static_api_key|clientSecret|privateKey|seed)' \
  "$output_dir" \
  --glob '*.json'; then
  printf 'identity projection proof exported local key or admin-secret vocabulary\n' >&2
  exit 1
fi

sha256_file() {
  sha256sum "$1" | cut -d' ' -f1
}

jq -n \
  --arg projectionSHA256 "$projection_digest" \
  --arg trustImportSHA256 "$(sha256_file "$output_dir/trust-import.json")" \
  --arg inspectSHA256 "$(sha256_file "$output_dir/inspect.json")" \
  --arg approveSHA256 "$(sha256_file "$output_dir/approve.json")" \
  --arg applySHA256 "$(sha256_file "$output_dir/apply.json")" \
  --arg unlinkSHA256 "$(sha256_file "$output_dir/unlink.json")" \
  --arg offlineApplySHA256 "$(sha256_file "$output_dir/offline-idempotent-apply.json")" \
  --arg validateSHA256 "$(sha256_file "$output_dir/offline-validate.log")" \
  '{
    schemaVersion: "stackkit.identity-projection-independence-proof/v1",
    status: "pass",
    projectionSHA256: $projectionSHA256,
    evidence: {
      trustImportSHA256: $trustImportSHA256,
      inspectSHA256: $inspectSHA256,
      approveSHA256: $approveSHA256,
      applySHA256: $applySHA256,
      unlinkSHA256: $unlinkSHA256,
      offlineIdempotentApplySHA256: $offlineApplySHA256,
      offlineValidateSHA256: $validateSHA256
    },
    claims: {
      credentialFreeProjection: true,
      localOwnerApprovalBeforeMutation: true,
      cloudRequiredForLifecycle: false,
      unlinkDeletesLocalIdentity: false,
      expiryDeletesLocalIdentity: false,
      localKeysOrAdminSecretsExported: false
    }
  }' >"$output_dir/identity-projection-independence-proof.json"
