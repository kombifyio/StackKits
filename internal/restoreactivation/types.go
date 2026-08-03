// Package restoreactivation derives and executes the fail-closed authority for
// promoting an owner-verified staged restore into the live Basement runtime.
package restoreactivation

import (
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/generationartifact"
)

// Authority is the immutable, plan-derived boundary for one restore
// activation. Volumes is the canonical sorted LiveName set used by the shared
// lifecycle journal; VolumeDetails carries the corresponding cutover paths.
type Authority struct {
	OperationID          string           `json:"operationId"`
	OwnerRef             string           `json:"ownerRef"`
	RestoreResultID      string           `json:"restoreResultId"`
	PlanHash             string           `json:"planHash"`
	ManifestHash         string           `json:"manifestHash"`
	ApplyResultHash      string           `json:"applyResultHash"`
	ManagedVolumeSetHash string           `json:"managedVolumeSetHash"`
	StackID              string           `json:"stackId"`
	ComposeProject       string           `json:"composeProject"`
	ComposePath          string           `json:"composePath"`
	ComposeDigest        string           `json:"composeDigest"`
	ComposeRuntimes      []ComposeRuntime `json:"composeRuntimes"`
	KopiaHelperImage     string           `json:"kopiaHelperImage"`
	StagingVolume        string           `json:"stagingVolume"`
	StagingPath          string           `json:"stagingPath"`
	Volumes              []string         `json:"volumes"`
	VolumeDetails        []Volume         `json:"volumeDetails"`
}

// ComposeRuntime binds one local Compose project to the exact owner-custody
// bytes that were verified before restore mutation. The Basement runtime and
// every selected standalone-compose Application runtime are represented
// independently; no PaaS or remote runtime is admitted here.
type ComposeRuntime struct {
	Project           string `json:"project"`
	Path              string `json:"path"`
	Digest            string `json:"digest"`
	EnvironmentPath   string `json:"environmentPath,omitempty"`
	EnvironmentDigest string `json:"environmentDigest,omitempty"`
}

// Volume is one exact persistent backup volume selected by the Basement core
// runtime. LogicalName comes from the component graph; LiveName is the
// Compose-qualified Docker volume name. RollbackName is deterministic for the
// activation operation and never caller supplied.
type Volume struct {
	ComponentRef   string `json:"componentRef"`
	LogicalName    string `json:"logicalName"`
	ComposeProject string `json:"composeProject"`
	LiveName       string `json:"liveName"`
	StagingPath    string `json:"stagingPath"`
	RollbackName   string `json:"rollbackName"`
}

// DeriveAuthority binds a staged, owner-verified restore to the exact Basement
// core plan and generation manifest without consulting mutable policy defaults.
func DeriveAuthority(
	workspaceRoot string,
	plan generationartifact.VerifiedPlan,
	manifest generationartifact.ArtifactManifest,
	restoreResult backuplifecycle.RestoreResult,
	operationID string,
) (Authority, error) {
	return deriveAuthority(workspaceRoot, plan, manifest, restoreResult, operationID)
}
