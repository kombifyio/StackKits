// Package base — CUE constraint checks for placement.cue.
// NOT named *_test.cue on purpose: CUE 0.15 excludes *_test.cue files from
// `cue vet ./base/...`, so this file uses a plain name to be vet-enforced.
// Run via: cue vet ./base/...
package base

// Intent: explicit valid instances.
_test_placement_local_only: #PlacementIntent & {mode: "local-only"}
_test_placement_standard: #PlacementIntent & {mode: "standard"}
_test_placement_ms: #PlacementIntent & {mode: "managed-serverless"}
_test_placement_default: #PlacementIntent & {}

// Assertions: forced presets must hold. `true & <expr>` => bottom (vet fails) if false.
_assert_local_only_private:   true & (_test_placement_local_only.exposure == "private")
_assert_local_only_cloudless: true & (_test_placement_local_only.coupling == "cloudless")
_assert_ms_public:            true & (_test_placement_ms.exposure == "public")
_assert_ms_coupled:           true & (_test_placement_ms.coupling == "coupled")
_assert_default_standard:     true & (_test_placement_default.mode == "standard")

// Standard permits non-default overrides (the "free" sub-dimensions).
_test_placement_standard_override: #PlacementIntent & {mode: "standard", exposure: "public", coupling: "coupled"}
_assert_standard_allows_override: true & (_test_placement_standard_override.exposure == "public")

// Eligibility (#PlacementSupport) — defaults + declared.
_test_support_default: #PlacementSupport & {}
_test_support_ms_eligible: #PlacementSupport & {managed_serverless: true}
_test_support_ms_rejected: #PlacementSupport & {
	managed_serverless: false
	missing_adapters: ["persistent-gpu-runtime"]
	rejection_reason: "needs on-device GPU"
}

// Eligibility gate (#ResolvedPlacement) — the gate must FORCE the support flag.
// The MS assert is the strong one: it proves the gate overrides the *false default.
_test_resolved_local_only: #ResolvedPlacement & {mode: "local-only"}
_test_resolved_ms: #ResolvedPlacement & {mode: "managed-serverless"}
_assert_resolved_local_only_forced: true & (_test_resolved_local_only.support.local_only == true)
_assert_resolved_ms_forced:         true & (_test_resolved_ms.support.managed_serverless == true)

// Assertions: defaults hold.
_assert_support_default_no_ms: true & (_test_support_default.managed_serverless == false)
_assert_support_default_local: true & (_test_support_default.local_only == true)
