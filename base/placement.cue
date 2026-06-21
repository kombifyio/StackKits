// Package base — Placement axis (PLACEMENT-MODE-STANDARD §4).
//
// Publishable vocabulary for the placement axis. Realization is LAYERED:
// StackKits-OSS realizes ONLY S1 (local-only, standard+cloudless). managed-
// serverless / coupled are NOT StackKit-native — their config lives in the
// Control-Plane catalog (kombify-DB sk_*), never in this OSS repo.
// See docs/placement/ and public StackKits standards/PRODUCT-SEGMENTATION.md.
//
// Run via: cue vet ./base/...
package base

#PlacementMode: "local-only" | "standard" | "managed-serverless"
#Exposure:      "private" | "public"
#Coupling:      "cloudless" | "coupled"

// #PlacementIntent is the USER INPUT (intent), not the resolved state.
// Sub-dimensions are forced in local-only / managed-serverless, free in standard.
#PlacementIntent: {
	mode: #PlacementMode | *"standard"

	exposure: #Exposure
	coupling: #Coupling

	if mode == "local-only" {
		exposure: "private"
		coupling: "cloudless"
	}
	if mode == "managed-serverless" {
		exposure: "public"
		coupling: "coupled"
	}
	if mode == "standard" {
		exposure: *"private" | "public"
		coupling: *"cloudless" | "coupled"
	}
}

// #PlacementSupport is module eligibility — PUBLISHABLE metadata.
// Eligibility != realization: managed_serverless says "this module COULD run MS";
// the MS config/realization lives in the Control-Plane catalog, never in OSS.
// cf_fit (in the snapshot) is derived from managed_serverless after lowering.
// Safe-open defaults: modules are eligible for the OSS modes (local_only/standard)
// unless restricted; managed_serverless is opt-in (*false) and needs explicit
// Control-Plane enablement — hence the asymmetric default.
#PlacementSupport: {
	local_only:         bool | *true
	standard:           bool | *true
	managed_serverless: bool | *false
	missing_adapters: [...string] | *[]
	rejection_reason?: string
}

// #ResolvedPlacement is the eligibility gate (catalog consistency, cue vet level).
#ResolvedPlacement: {
	mode:    #PlacementMode
	support: #PlacementSupport
	if mode == "managed-serverless" {support: managed_serverless: true}
	if mode == "local-only" {support: local_only: true}
}
