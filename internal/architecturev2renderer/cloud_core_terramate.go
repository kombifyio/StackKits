package architecturev2renderer

import (
	"context"

	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

// Cloud core Terramate twins: the OpenTofu root of the matching OpenTofu twin
// (same Compose pipeline, same wrapper) plus the core `stack.tm.hcl`. The
// project root is emitted once per host by the Core host bootstrap module.
const (
	cloudCoreTerramateUnitID                      = "terramate"
	cloudCoreTerramateTemplateRef                 = "builtin://cloud/core/terramate/v1"
	cloudCoreTerramateOpenTofuOutputRef           = "platform/cloud-core/main.tf"
	cloudCoreTerramateStackOutputRef              = "platform/cloud-core/stack.tm.hcl"
	cloudStandaloneCoreTerramateTemplateRef       = "builtin://cloud/core-standalone/terramate/v1"
	cloudStandaloneCoreTerramateOpenTofuOutputRef = "platform/cloud-core-standalone/main.tf"
	cloudStandaloneCoreTerramateStackOutputRef    = "platform/cloud-core-standalone/stack.tm.hcl"
	cloudCoreTerramateSchema                      = `stackkit.cloud-core-terramate/v1|artifact-revision:1|engine:terramate|underlay:opentofu|outputs:main.tf,stack.tm.hcl|stack:terramatestackgraph-core,project-root-per-host|root:` + cloudCoreOpenTofuSchema
	cloudStandaloneCoreTerramateSchema            = `stackkit.cloud-core-standalone-terramate/v1|artifact-revision:1|engine:terramate|underlay:opentofu|outputs:main.tf,stack.tm.hcl|stack:terramatestackgraph-core,project-root-per-host|root:` + cloudStandaloneCoreOpenTofuSchema
)

func CloudCoreTerramateRendererContract() RendererContract {
	return RendererContract{
		Kind: "terramate", RendererRef: cloudCoreRendererRef, TemplateRef: cloudCoreTerramateTemplateRef,
		Version: cloudCoreVersion, ContractHash: "sha256:" + sha256Bytes([]byte(cloudCoreTerramateSchema)),
	}
}

func CloudStandaloneCoreTerramateRendererContract() RendererContract {
	return RendererContract{
		Kind: "terramate", RendererRef: cloudCoreRendererRef, TemplateRef: cloudStandaloneCoreTerramateTemplateRef,
		Version: cloudStandaloneCoreVersion, ContractHash: "sha256:" + sha256Bytes([]byte(cloudStandaloneCoreTerramateSchema)),
	}
}

type cloudCoreTerramateRenderer struct {
	contract       RendererContract
	root           composePayloadOpenTofuRenderer
	openTofuOutput string
	stackOutput    string
}

func newCloudCoreTerramateRenderer() cloudCoreTerramateRenderer {
	contract := CloudCoreTerramateRendererContract()
	return cloudCoreTerramateRenderer{
		contract: contract, openTofuOutput: cloudCoreTerramateOpenTofuOutputRef, stackOutput: cloudCoreTerramateStackOutputRef,
		root: cloudCoreTerramatePayloadRenderer(contract, cloudCoreRenderProfileForCloudCore(),
			cloudCoreTerramateOpenTofuOutputRef, cloudCoreOpenTofuResourcePrefix, cloudCoreComposeProjectName),
	}
}

func newCloudStandaloneCoreTerramateRenderer() cloudCoreTerramateRenderer {
	contract := CloudStandaloneCoreTerramateRendererContract()
	return cloudCoreTerramateRenderer{
		contract: contract, openTofuOutput: cloudStandaloneCoreTerramateOpenTofuOutputRef, stackOutput: cloudStandaloneCoreTerramateStackOutputRef,
		root: cloudCoreTerramatePayloadRenderer(contract, cloudStandaloneCoreRenderProfile(),
			cloudStandaloneCoreTerramateOpenTofuOutputRef, cloudStandaloneCoreOpenTofuResourcePrefix, cloudStandaloneCoreComposeProjectName),
	}
}

func cloudCoreTerramatePayloadRenderer(contract RendererContract, profile cloudCoreRenderProfile, outputRef, resourcePrefix, projectName string) composePayloadOpenTofuRenderer {
	profile.unitID, profile.outputRef = cloudCoreTerramateUnitID, outputRef
	return composePayloadOpenTofuRenderer{
		contract: contract, outputRef: outputRef, resourcePrefix: resourcePrefix, projectName: projectName,
		compose: func(ctx context.Context, unit RenderUnit) ([]byte, error) {
			// The Compose pipeline proves exactly one governed output; the
			// Terramate unit owns the root and the stack, so the pipeline sees
			// the root only.
			payloadUnit := unit
			payloadUnit.declaredOutputRef = []string{outputRef}
			outputs, err := renderCloudCoreUnit(ctx, payloadUnit, contract, profile)
			if err != nil {
				return nil, err
			}
			return outputs[0].Bytes, nil
		},
	}
}

func (r cloudCoreTerramateRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if !exactStringList(unit.DeclaredOutputs(), []string{r.openTofuOutput, r.stackOutput}) {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".renderUnits."+unit.ID()+".outputs", "a Cloud core Terramate unit emits exactly main.tf and stack.tm.hcl")
	}
	outputs, err := r.root.RenderUnit(ctx, unit)
	if err != nil {
		return nil, err
	}
	stack, err := renderTerramateStackForUnit(unit, terramatestackgraph.RoleCore)
	if err != nil {
		return nil, err
	}
	return append(outputs, UnitOutput{Ref: r.stackOutput, Bytes: stack}), nil
}

var _ UnitRenderer = cloudCoreTerramateRenderer{}
