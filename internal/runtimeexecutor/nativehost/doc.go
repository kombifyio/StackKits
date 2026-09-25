// Package nativehost holds the native host executors and operations.
//
// Under the compose target it is the fallback executor per ADR-0045 §6
// (S-F): native Compose `up` for the Basement and Cloud cores and for
// standalone workloads. It is retained until parity evidence exists;
// retirement is a P4 slice. The same package also provides the shared halves
// the standard executor in internal/runtimeexecutor/opentofu calls under the
// opentofu and terramate targets: the Compose prepare and complete halves,
// native Verify observation, project naming, generation-target reading, and
// the native host owners (for example internal PKI, public TLS, host
// security, identity policy and federation link) that have no OpenTofu
// counterpart and run under every target.
//
// The fallback-only paths are methods on the same OS operations types as the
// shared halves and share unexported helpers, so they cannot move to a
// separate package without code changes. Splitting them is part of the P4
// retirement slice, not of the structure rule.
package nativehost
