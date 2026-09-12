package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
)

const homeAssistantInstanceSchema = "stackkit.home-assistant-instance/v1|module,site,node|external:external-control-plane:api|platform,installationMethod,instanceOrigin,managementScope,configurationPolicy,dataCustody,baselinePolicy,baselineVersion|no-endpoint-no-secret-no-guest|fresh-only-preserve-imported|native-auth-capability-evidence"

func HomeAssistantInstanceOutputRef(module string) string {
	switch module {
	case "stackkits-home-assistant-haos-runtime":
		return "workloads/home-assistant-haos/instance.json"
	case "stackkits-home-assistant-existing-runtime":
		return "workloads/home-assistant-existing/instance.json"
	case "stackkits-home-assistant-imported-runtime":
		return "workloads/home-assistant-imported/instance.json"
	default:
		return ""
	}
}

// ExternalApplicationInstance is the CUE-owned contract. An unknown method
// stays unknown until the external owner observes it; product identity is not
// proof of an installation method or permission to mutate an instance.
type ExternalApplicationInstance struct {
	Platform            string `json:"platform"`
	InstallationMethod  string `json:"installationMethod"`
	InstanceOrigin      string `json:"instanceOrigin"`
	ManagementScope     string `json:"managementScope"`
	ConfigurationPolicy string `json:"configurationPolicy"`
	DataCustody         string `json:"dataCustody"`
	BaselinePolicy      string `json:"baselinePolicy"`
	BaselineVersion     string `json:"baselineVersion,omitempty"`
}

type HomeAssistantInstanceArtifact struct {
	APIVersion string                      `json:"apiVersion"`
	ModuleRef  string                      `json:"moduleRef"`
	SiteRef    string                      `json:"siteRef"`
	NodeRef    string                      `json:"nodeRef"`
	Instance   ExternalApplicationInstance `json:"instance"`
}

func HomeAssistantInstanceRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(homeAssistantInstanceSchema))
	return RendererContract{Kind: "native-config", RendererRef: "stackkit", TemplateRef: "builtin://workloads/home-assistant/instance/v1.json", Version: "1.0.0", ContractHash: "sha256:" + hex.EncodeToString(sum[:])}
}

type homeAssistantInstanceRenderer struct{}

func (homeAssistantInstanceRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contract := HomeAssistantInstanceRendererContract()
	engine, hasEngine := unit.RuntimeEngine()
	if unit.ID() != "instance" || unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash || unit.RuntimeKind() != "external" || unit.RuntimeDelivery() != "external-control-plane" || !hasEngine || engine != "api" || unit.InstanceScope() != "node-local" {
		return nil, errors.New("Home Assistant instance requires the exact external API renderer contract")
	}
	site, hasSite := unit.SiteRef()
	node, hasNode := unit.NodeRef()
	if !hasSite || !hasNode || !slices.Equal(unit.DeclaredOutputs(), []string{HomeAssistantInstanceOutputRef(unit.ModuleID())}) || len(unit.PublicInputRefs()) != 0 || len(unit.SecretInputRefs()) != 0 || len(unit.PlanInputRefs()) != 0 || !emptyJSONObject(unit.ValuesJSON()) || !emptyJSONObject(unit.SecretRefsJSON()) || !emptyJSONArray(unit.ServiceEndpointsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) || !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) {
		return nil, errors.New("Home Assistant instance must have one external owner and no local service, network or user-supplied authority")
	}
	var instance ExternalApplicationInstance
	if err := decodeStrict(unit.RuntimeSettingsJSON(), &instance); err != nil {
		return nil, err
	}
	artifact := HomeAssistantInstanceArtifact{APIVersion: "stackkit.home-assistant-instance/v1", ModuleRef: unit.ModuleID(), SiteRef: site, NodeRef: node, Instance: instance}
	data, err := json.Marshal(artifact)
	if err != nil {
		return nil, err
	}
	if _, err := ValidateHomeAssistantInstanceArtifact(data, unit.ModuleID(), site, node); err != nil {
		return nil, err
	}
	return []UnitOutput{{Ref: HomeAssistantInstanceOutputRef(unit.ModuleID()), Bytes: append(data, '\n')}}, nil
}

// ValidateHomeAssistantInstanceArtifact rejects policy or target substitution
// before an API owner can observe or mutate its separately bound instance.
func ValidateHomeAssistantInstanceArtifact(data []byte, module, site, node string) (HomeAssistantInstanceArtifact, error) {
	var artifact HomeAssistantInstanceArtifact
	if err := decodeStrict(data, &artifact); err != nil {
		return artifact, err
	}
	i := artifact.Instance
	valid := artifact.APIVersion == "stackkit.home-assistant-instance/v1" && artifact.ModuleRef == module && artifact.SiteRef == site && artifact.NodeRef == node && site != "" && node != "" && i.Platform == "home-assistant" && i.ConfigurationPolicy == "preserve-user-changes" && i.DataCustody == "external-instance-owner"
	switch module {
	case "stackkits-home-assistant-haos-runtime":
		valid = valid && i.InstallationMethod == "haos" && i.InstanceOrigin == "new" && i.ManagementScope == "managed" && i.BaselinePolicy == "fresh-only" && i.BaselineVersion == "1"
	case "stackkits-home-assistant-existing-runtime":
		valid = valid && i.InstallationMethod == "unknown" && i.InstanceOrigin == "existing" && i.ManagementScope == "observed" && i.BaselinePolicy == "preserve" && i.BaselineVersion == ""
	case "stackkits-home-assistant-imported-runtime":
		valid = valid && i.InstallationMethod == "haos" && i.InstanceOrigin == "imported" && i.ManagementScope == "managed" && i.BaselinePolicy == "preserve" && i.BaselineVersion == ""
	default:
		valid = false
	}
	if !valid {
		return artifact, errors.New("Home Assistant instance does not match its exact module, origin and management authority")
	}
	return artifact, nil
}
