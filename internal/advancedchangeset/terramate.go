package advancedchangeset

import (
	"regexp"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/terramatehost"
	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

var terramateStackIDPattern = regexp.MustCompile(`^stackkit-[0-9a-f]{20}$`)

// TerramateScope is the Terramate part of a change set: the stacks the
// artifact diff touches, in graph run order, and the local host project the
// candidate materializes.
type TerramateScope struct {
	AffectedStacks     []string
	HostManifestSHA256 string
}

// DeriveTerramateScope maps an artifact diff to Terramate stacks through the
// candidate's stack graph. Git-based `terramate list --changed` is not
// available in the runtime tree, so the graph is the only authority.
//
// A changed artifact affects every stack whose module owns it: added and
// modified paths resolve their owner in the candidate, removed paths in the
// baseline. Plan-owned artifacts (the graph itself) and modules that are not
// stacks (host bootstrap, security baseline) affect no stack. A module
// rendered on several nodes affects each of its node stacks; stacks that
// exist only in the baseline (a removed workload) are not part of the
// candidate graph and are not listed.
func DeriveTerramateScope(
	baseline, candidate []architecturev2renderer.Artifact,
	changes []ArtifactChange,
	siteRef, nodeRef string,
) (TerramateScope, error) {
	layout, err := terramatehost.PlanFromArtifacts(candidate, siteRef, nodeRef)
	if err != nil {
		return TerramateScope{}, wrap(ErrInvalid, "terramate", "candidate has no usable Terramate host project", err)
	}
	order, err := terramatestackgraph.RunOrder(layout.Graph)
	if err != nil {
		return TerramateScope{}, wrap(ErrInvalid, "terramate", "candidate stack graph has no run order", err)
	}
	modules := make(map[string]struct{})
	owners := func(artifacts []architecturev2renderer.Artifact) map[string]string {
		result := make(map[string]string, len(artifacts))
		for _, artifact := range artifacts {
			result[artifact.Path] = artifact.ModuleID
		}
		return result
	}
	before, after := owners(baseline), owners(candidate)
	for _, change := range changes {
		owner := after[change.Path]
		if change.Status == StatusRemoved {
			owner = before[change.Path]
		}
		if owner != "" {
			modules[owner] = struct{}{}
		}
	}
	affected := make([]string, 0)
	for _, id := range order {
		stack, _ := layout.Stack(id)
		if _, touched := modules[stack.ModuleRef]; touched {
			affected = append(affected, id)
		}
	}
	return TerramateScope{AffectedStacks: affected, HostManifestSHA256: layout.ManifestSHA256}, nil
}

func validateTerramateScope(record Record) error {
	if record.AffectedStacks == nil {
		return fail(ErrInvalid, "affectedStacks", "is required (empty when no stack is affected)")
	}
	seen := make(map[string]struct{}, len(record.AffectedStacks))
	for _, id := range record.AffectedStacks {
		if !terramateStackIDPattern.MatchString(id) {
			return fail(ErrInvalid, "affectedStacks", "contains a value that is not a Terramate stack ID")
		}
		if _, duplicate := seen[id]; duplicate {
			return fail(ErrInvalid, "affectedStacks", "contains a duplicate stack ID")
		}
		seen[id] = struct{}{}
	}
	if !hashPattern.MatchString(record.TerramateHostManifestSHA256) {
		return fail(ErrInvalid, "terramateHostManifestSha256", "must be a canonical SHA-256 digest")
	}
	return nil
}
