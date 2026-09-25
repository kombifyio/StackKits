package architecturev2renderer

import (
	"context"

	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

const (
	basementCoreTerramateUnitID      = "terramate"
	basementCoreTerramateTemplateRef = "builtin://basement/core/terramate/v1"

	basementCoreTerramateOpenTofuOutputRef = "platform/basement-core/main.tf"
	basementCoreTerramateStackOutputRef    = "platform/basement-core/stack.tm.hcl"
)

const basementCoreTerramateSchema = `stackkit.basement-core-terramate/v1|artifact-revision:9|compose-payload:byte-identical,runtime-dir-root,up-replace-without-down,down-on-destroy-only,project-stackkit-basement-core,local-provider-2.5.3|runtime-listeners:catalog-bound,direct-loopback-only-except-router-and-lan-dns|engine:terramate|underlay:opentofu|outputs:main.tf,stack.tm.hcl|stack:terramatestackgraph-core,project-root-per-host|execution-instance:node-local|credentials:none|cloud:none|step-ca:lan-dns-resolved-acme-challenges|ingress:forward-auth-bound,websecure-step-ca|listener-site-address:inventory-bound|acme-leaf-duration:24h-renew-before6h-health-grace10m`

type basementCoreTerramateRenderer struct {
	contract RendererContract
}

// BasementCoreTerramateRendererContract returns the immutable renderer
// identity for the Terramate orchestration layer over the existing OpenTofu
// Basement module.
func BasementCoreTerramateRendererContract() RendererContract {
	return basementCoreContract("terramate", basementCoreTerramateTemplateRef, basementCoreTerramateSchema)
}

func newBasementCoreTerramateRenderer() basementCoreTerramateRenderer {
	return basementCoreTerramateRenderer{contract: BasementCoreTerramateRendererContract()}
}

func (r basementCoreTerramateRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateBasementCoreTerramateUnit(unit, r.contract); err != nil {
		return nil, err
	}
	domain, _ := unit.NetworkDomainBase()
	root, err := RenderComposePayloadOpenTofu(ComposePayloadSpec{
		ResourcePrefix: basementCoreOpenTofuResourcePrefix, ProjectName: basementCoreComposeProjectName,
		Compose: renderKopiaSourceVolumeBinds(unit, renderSiteListenerBindings(unit, RenderBasementCoreComposeForDomain(domain))),
	})
	if err != nil {
		return nil, err
	}
	stack, err := renderTerramateStackForUnit(unit, terramatestackgraph.RoleCore)
	if err != nil {
		return nil, err
	}
	return []UnitOutput{
		{Ref: basementCoreTerramateOpenTofuOutputRef, Bytes: root},
		{Ref: basementCoreTerramateStackOutputRef, Bytes: stack},
	}, nil
}

func validateBasementCoreTerramateUnit(unit RenderUnit, contract RendererContract) error {
	outputs := []string{
		basementCoreTerramateOpenTofuOutputRef,
		basementCoreTerramateStackOutputRef,
	}
	if err := validateBasementCoreUnitOutputs(
		unit,
		contract,
		basementCoreTerramateUnitID,
		outputs,
	); err != nil {
		return err
	}
	return nil
}

var _ UnitRenderer = basementCoreTerramateRenderer{}
