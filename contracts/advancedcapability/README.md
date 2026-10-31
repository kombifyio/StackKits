# advancedcapability

Generated wire contract for `stackkit.advanced-capability/v1` (short-lived,
secret-free capability authorizing one StackKits Advanced operation) and
`stackkit.advanced-trust-bundle/v1` (Owner-imported trust bundle verified
offline).

## Generation

`contract.json` is the only hand-edited source (constants, limits, the operation identifiers, field patterns, document field model in RFC 8785 (JCS)
member order, fixture inputs). `mise run contracts:advancedcapability:generate`
writes:

- `advancedcapability_gen.go`: constants, `Operations`/`IsOperation`,
  `Capability`, `BackupRenewal`, `TransferRetirement`, `TrustBundle`, `TrustBundleKey`, JCS encoders,
  constant/pattern checks;
- `schema.json`: JSON Schema 2020-12 projection;
- `testdata/*.json`: signed conformance capabilities and trust bundles from
  fixed public test seeds, via an encoder independent of the Go one;
- `manifest.json`: generator version, wire version, source and output SHA-256.

`contracts:advancedcapability:verify` (in `check` and `ci:fast`) regenerates in
memory and fails on any difference. `wire.go` holds the hand-written rules:
strict exact-bytes decoding, time and lifetime rules, logical-reference rules,
signing digest `SHA-256(schemaVersion || 0x00 || unsigned JCS)`, signature
checks. The existing conformance capabilities are byte-identical to the Techstack
(`internal/advancedissuer/testdata`) and StackKits
(`internal/advancedcapability/testdata`) vectors.

## Issuer pin

Advanced Mode is Techstack-only. A capability's `issuerId` and every
trust-bundle key's `issuerId` must be `techstack` or `techstack.<suffix>`
(`IssuerClass`, `IsTechstackIssuerID`); `ValidateClaims`, `DecodeCapability`,
`TrustBundle.Validate` and `DecodeTrustBundle` enforce it, so a bundle naming
another issuer is rejected before its digest can be approved. The verifier also
keeps the Owner-approved bundle digest, issuer/key binding, trusted time and
local stack, Owner and operation scope.

## Ownership

In-memory validation and encoding only. Techstack owns key custody, issuance and
trust-binding records; StackKits owns trust import, Owner approval, trusted
time, scope admission and CLI reason codes. Both consume this package.

## Held application transfer retirement

`application-transfer.retire` requires exactly one `transferRetirement` scope
and a lifetime of at most 15 minutes. It cannot share a capability with another
operation or a backup renewal. The scope binds the original snapshot operation,
transfer ID, signed peer outcome digest, source or destination role, and exact
baseline and candidate StackSpec hashes. A consumer reconstructs all fields
from its authenticated local journal and canonical removal candidate before
committing under the existing snapshot lease. The grant permits no runtime
activation, restart, volume deletion or provider operation. Older consumers
reject the new operation; existing conformance bytes remain unchanged.
