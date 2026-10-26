// Package workloadrelocation moves PaaS workloads (projects, applications,
// databases and their data) from one server's platform to another's.
//
// Each step runs on exactly one server, as every StackKit operation does: the
// source plans, the destination checks that plan, and Techstack carries the
// plan between them. A plan holds names, sizes and fingerprints only; data
// transfer keys and credentials travel separately through Wallet host
// delivery, never inside a plan, check or log.
package workloadrelocation

import "encoding/json"

// Platform names a Layer-2 PaaS platform with a relocation engine.
type Platform string

// PlatformCoolify is Coolify, relocated with the pinned coolify-mirror fork.
const PlatformCoolify Platform = "coolify"

// CheckFormat is the StackKits envelope of a destination check.
const CheckFormat = "kombify.io/stackkit-relocation-check/v1"

// Engine identifies the exact relocation engine build that produced a result.
type Engine struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	UpstreamCommit string `json:"upstream_commit"`
	ForkRevision   string `json:"fork_revision"`
}

// Check is the destination's verdict on a plan. Ready is false while any
// blocker remains; Detail is the engine's own check document.
type Check struct {
	RecoveryFormat       string          `json:"recoveryFormat"`
	RestoreReceiptFormat string          `json:"restoreReceiptFormat"`
	PlanSHA256           string          `json:"planSha256"`
	Format               string          `json:"format"`
	Platform             Platform        `json:"platform"`
	Engine               Engine          `json:"engine"`
	Ready                bool            `json:"ready"`
	Blockers             []string        `json:"blockers"`
	Warnings             []string        `json:"warnings"`
	Detail               json.RawMessage `json:"detail"`
}
