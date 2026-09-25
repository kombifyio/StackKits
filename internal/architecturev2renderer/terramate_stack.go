package architecturev2renderer

import (
	"bytes"
	"context"
	"path"
	"strings"

	"github.com/kombifyio/stackkits/internal/terramatestackgraph"
)

// Terramate stacks (ADR-0045 section 2 and section 5). Every stack-bearing
// render instance emits one `stack.tm.hcl` from RenderTerramateStack; its
// identity, tags and in-project ordering come from terramatestackgraph, the
// same authority that renders the plan-owned stack graph.
const (
	terramateStackOutputName = "stack.tm.hcl"
	terramateRootOutputName  = "terramate.tm.hcl"
	terramateGenericVersion  = "1.0.0"
	terramateStackSchema     = `stackkit.terramate-stack/v1|artifact-revision:1|id:stackkit-sha256-module-site-node-20|tags:stackkit,role,site,node,module|after:terramate-tag-queries|terramate:` + terramatestackgraph.RequiredVersion
	terramateRootSchema      = `stackkit.terramate-project-root/v1|artifact-revision:1|required-version:` + terramatestackgraph.RequiredVersion + `|run-env:TF_IN_AUTOMATION,TF_INPUT|change-detection:explicit-order-no-git|placement:one-per-node`
)

// TerramateStackSpec is the complete content of one `stack.tm.hcl`.
type TerramateStackSpec struct {
	ID          string
	Name        string
	Description string
	Tags        []string
	After       []string
}

// RenderTerramateStack emits the canonical `stack` block. Values are limited
// to a literal-safe character set, so no HCL escaping or interpolation can
// occur.
func RenderTerramateStack(spec TerramateStackSpec) ([]byte, error) {
	if spec.ID == "" || spec.Name == "" || spec.Description == "" || len(spec.Tags) == 0 {
		return nil, fail(ErrRendererFailure, "renderer.terramate-stack", "stack id, name, description and tags are required")
	}
	values := append([]string{spec.ID, spec.Name, spec.Description}, spec.Tags...)
	for _, value := range append(values, spec.After...) {
		if !terramateLiteralSafe(value) {
			return nil, fail(ErrRendererFailure, "renderer.terramate-stack", "value %q is not a literal-safe HCL string", value)
		}
	}
	var out bytes.Buffer
	out.WriteString("stack {\n")
	out.WriteString(`  name        = "` + spec.Name + "\"\n")
	out.WriteString(`  description = "` + spec.Description + "\"\n")
	out.WriteString(`  id          = "` + spec.ID + "\"\n")
	out.WriteString(`  tags        = ` + terramateStringList(spec.Tags) + "\n")
	if len(spec.After) > 0 {
		out.WriteString(`  after       = ` + terramateStringList(spec.After) + "\n")
	}
	out.WriteString("}\n")
	return out.Bytes(), nil
}

func terramateStringList(values []string) string {
	return `["` + strings.Join(values, `", "`) + `"]`
}

func terramateLiteralSafe(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(" ./@:_-", r):
		default:
			return false
		}
	}
	return true
}

// renderTerramateStackForUnit derives the stack of one node-local render
// instance.
func renderTerramateStackForUnit(unit RenderUnit, role terramatestackgraph.Role) ([]byte, error) {
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode {
		return nil, fail(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".renderUnits."+unit.ID()+".instances", "a Terramate stack requires one exact node-local instance")
	}
	definition, err := terramatestackgraph.Define(role, unit.ModuleID(), siteRef, nodeRef)
	if err != nil {
		return nil, wrap(ErrInvalidPlan, "resolvedPlan.modules."+unit.ModuleID()+".renderUnits."+unit.ID(), "derive Terramate stack identity", err)
	}
	return RenderTerramateStack(TerramateStackSpec{
		ID: definition.ID, Name: definition.Name, Description: definition.Description,
		Tags: definition.Tags, After: definition.AfterQueries,
	})
}

func terramateGenericContract(templateRef, schema string) RendererContract {
	return RendererContract{
		Kind: "terramate", RendererRef: "stackkit", TemplateRef: templateRef,
		Version: terramateGenericVersion, ContractHash: "sha256:" + sha256Bytes([]byte(schema)),
	}
}

// TerramateWorkloadStackRendererContract identifies the companion stack unit
// of the selected-PaaS workload bundles.
func TerramateWorkloadStackRendererContract() RendererContract {
	return terramateGenericContract(terramatestackgraph.WorkloadStackTemplateRef, terramateStackSchema+"|role:workload")
}

// TerramateEdgeStackRendererContract identifies the companion stack unit of
// the Cloud public edge owner.
func TerramateEdgeStackRendererContract() RendererContract {
	return terramateGenericContract(terramatestackgraph.EdgeStackTemplateRef, terramateStackSchema+"|role:edge")
}

// TerramateFederationStackRendererContract identifies the companion stack
// unit of the Modern federation and bridge owners.
func TerramateFederationStackRendererContract() RendererContract {
	return terramateGenericContract(terramatestackgraph.FederationStackTemplateRef, terramateStackSchema+"|role:federation")
}

// TerramateProjectRootRendererContract identifies the per-host project root.
func TerramateProjectRootRendererContract() RendererContract {
	return terramateGenericContract(terramatestackgraph.ProjectRootTemplateRef, terramateRootSchema)
}

// terramateCompanionRenderer renders one artifact-only Terramate file for an
// exact node-local instance: a stack (role set) or the project root.
type terramateCompanionRenderer struct {
	contract RendererContract
	role     terramatestackgraph.Role
	output   string
}

func newTerramateCompanionRenderers() []terramateCompanionRenderer {
	return []terramateCompanionRenderer{
		{contract: TerramateWorkloadStackRendererContract(), role: terramatestackgraph.RoleWorkload, output: terramateStackOutputName},
		{contract: TerramateEdgeStackRendererContract(), role: terramatestackgraph.RoleEdge, output: terramateStackOutputName},
		{contract: TerramateFederationStackRendererContract(), role: terramatestackgraph.RoleFederation, output: terramateStackOutputName},
		{contract: TerramateProjectRootRendererContract(), output: terramateRootOutputName},
	}
}

func (r terramateCompanionRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unitPath := "resolvedPlan.modules." + unit.ModuleID() + ".renderUnits." + unit.ID()
	if unit.Kind() != r.contract.Kind || unit.RendererRef() != r.contract.RendererRef || unit.TemplateRef() != r.contract.TemplateRef ||
		unit.Version() != r.contract.Version || unit.ContractHash() != r.contract.ContractHash {
		return nil, fail(ErrOutputChanged, unitPath, "render-unit implementation identity differs from the registered Terramate contract")
	}
	outputs := unit.DeclaredOutputs()
	if len(outputs) != 1 || path.Base(outputs[0]) != r.output {
		return nil, fail(ErrInvalidPlan, unitPath+".outputs", "a Terramate companion unit declares exactly one %s output", r.output)
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return nil, fail(ErrInvalidPlan, unitPath+".interfaces", "a Terramate companion unit receives no runtime authority")
	}
	if r.output == terramateRootOutputName {
		if _, hasNode := unit.NodeRef(); unit.InstanceScope() != "node-local" || !hasNode {
			return nil, fail(ErrInvalidPlan, unitPath+".instances", "a Terramate project root requires one exact node-local instance")
		}
		return []UnitOutput{{Ref: outputs[0], Bytes: []byte(terramatestackgraph.ProjectRootConfig)}}, nil
	}
	stack, err := renderTerramateStackForUnit(unit, r.role)
	if err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: outputs[0], Bytes: stack}}, nil
}

var _ UnitRenderer = terramateCompanionRenderer{}
