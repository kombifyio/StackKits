package architecturev2renderer

import "context"

// Cloud core OpenTofu twins wrap the exact Compose artifact of the matching
// Compose unit (ADR-0045 Stage 1). Each schema embeds the Compose schema, so a
// Compose contract change always yields a new OpenTofu contract hash.
const (
	cloudCoreOpenTofuUnitID                   = "opentofu"
	cloudCoreOpenTofuTemplateRef              = "builtin://cloud/core/opentofu/v1.tf"
	cloudCoreOpenTofuOutputRef                = "platform/cloud-core/main.tf"
	cloudStandaloneCoreOpenTofuTemplateRef    = "builtin://cloud/core-standalone/opentofu/v1.tf"
	cloudStandaloneCoreOpenTofuOutputRef      = "platform/cloud-core-standalone/main.tf"
	cloudCoreOpenTofuSchema                   = `stackkit.cloud-core-opentofu/v1|artifact-revision:2|compose-payload:byte-identical,runtime-dir-root,up-replace-without-down,down-on-destroy-only,project-stackkit-cloud-core,local-provider-2.5.3|payload:` + cloudCoreComposeSchema
	cloudStandaloneCoreOpenTofuSchema         = `stackkit.cloud-core-standalone-opentofu/v1|artifact-revision:2|compose-payload:byte-identical,runtime-dir-root,up-replace-without-down,down-on-destroy-only,project-stackkit-cloud-core-standalone,local-provider-2.5.3|payload:` + cloudStandaloneCoreComposeSchema
	cloudCoreComposeProjectName               = "stackkit-cloud-core"
	cloudStandaloneCoreComposeProjectName     = "stackkit-cloud-core-standalone"
	cloudCoreOpenTofuResourcePrefix           = "cloud_core"
	cloudStandaloneCoreOpenTofuResourcePrefix = "cloud_core_standalone"
)

func CloudCoreOpenTofuRendererContract() RendererContract {
	return RendererContract{
		Kind: "opentofu", RendererRef: cloudCoreRendererRef, TemplateRef: cloudCoreOpenTofuTemplateRef,
		Version: cloudCoreVersion, ContractHash: "sha256:" + sha256Bytes([]byte(cloudCoreOpenTofuSchema)),
	}
}

func CloudStandaloneCoreOpenTofuRendererContract() RendererContract {
	return RendererContract{
		Kind: "opentofu", RendererRef: cloudCoreRendererRef, TemplateRef: cloudStandaloneCoreOpenTofuTemplateRef,
		Version: cloudStandaloneCoreVersion, ContractHash: "sha256:" + sha256Bytes([]byte(cloudStandaloneCoreOpenTofuSchema)),
	}
}

func newCloudCoreOpenTofuRenderer() composePayloadOpenTofuRenderer {
	return cloudCorePayloadOpenTofuRenderer(CloudCoreOpenTofuRendererContract(), cloudCoreRenderProfileForCloudCore(),
		cloudCoreOpenTofuOutputRef, cloudCoreOpenTofuResourcePrefix, cloudCoreComposeProjectName)
}

func newCloudStandaloneCoreOpenTofuRenderer() composePayloadOpenTofuRenderer {
	return cloudCorePayloadOpenTofuRenderer(CloudStandaloneCoreOpenTofuRendererContract(), cloudStandaloneCoreRenderProfile(),
		cloudStandaloneCoreOpenTofuOutputRef, cloudStandaloneCoreOpenTofuResourcePrefix, cloudStandaloneCoreComposeProjectName)
}

// cloudCorePayloadOpenTofuRenderer runs the unchanged Cloud Compose pipeline
// (validation, listener binding, Kopia binds) for the OpenTofu unit identity
// and wraps its bytes.
func cloudCorePayloadOpenTofuRenderer(contract RendererContract, profile cloudCoreRenderProfile, outputRef, resourcePrefix, projectName string) composePayloadOpenTofuRenderer {
	profile.unitID, profile.outputRef = cloudCoreOpenTofuUnitID, outputRef
	return composePayloadOpenTofuRenderer{
		contract: contract, outputRef: outputRef, resourcePrefix: resourcePrefix, projectName: projectName,
		compose: func(ctx context.Context, unit RenderUnit) ([]byte, error) {
			outputs, err := renderCloudCoreUnit(ctx, unit, contract, profile)
			if err != nil {
				return nil, err
			}
			return outputs[0].Bytes, nil
		},
	}
}
