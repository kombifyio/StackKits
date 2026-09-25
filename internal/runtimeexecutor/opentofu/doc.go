// Package opentofu is the standard StackKits runtime executor (ADR-0045):
// the standard execution path. It realizes one module's CUE-generated
// OpenTofu root with the packaged tofu binary and an offline provider mirror,
// keeps OpenTofu state in that root, and verifies the result with the native
// Verify semantics of the module. The native Compose executor in
// internal/runtimeexecutor/nativehost stays the fallback behind the compose
// target (S-F, ADR-0045 §6).
package opentofu
