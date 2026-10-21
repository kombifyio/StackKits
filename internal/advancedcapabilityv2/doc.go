// Package advancedcapability is the generated wire contract for the
// short-lived, secret-free capabilities that authorize individual StackKits
// Advanced operations (stackkit.advanced-capability/v1) and for the
// Owner-imported trust bundle that verifies them offline
// (stackkit.advanced-trust-bundle/v1).
//
// The canonical source is contract.json in this directory; constants, the
// operation vocabulary, field patterns, document types, RFC 8785 (JCS)
// encoders, the JSON Schema and the signed conformance fixtures are generated
// from it by internal/cmd/advancedcapabilitygen and bound to it by
// manifest.json. Techstack issues and StackKits verifies against this package.
//
// The package validates and encodes values in memory only. Key custody,
// issuance records, trust import and approval, trusted time, local stack and
// Owner scope admission, and the reason codes a CLI reports stay with the
// issuer and verifier that consume it.
package advancedcapability
