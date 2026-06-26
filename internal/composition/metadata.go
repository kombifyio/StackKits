package composition

import (
	"encoding/json"

	"github.com/kombifyio/stackkits/pkg/models"
)

// Metadata is the non-secret composition evidence emitted alongside generated
// tfvars. It deliberately excludes passwords, tokens, and file-backed keys.
type Metadata struct {
	SchemaVersion        string                `json:"schemaVersion"`
	StackKit             string                `json:"stackkit,omitempty"`
	InstallMode          string                `json:"installMode,omitempty"`
	PlacementMode        string                `json:"placementMode,omitempty"`
	EnabledModules       []string              `json:"enabledModules,omitempty"`
	Warnings             []string              `json:"warnings,omitempty"`
	ApplicationHandoffs  []ControlPlaneHandoff `json:"applicationHandoffs,omitempty"`
	Identity             *IdentityMetadata     `json:"identity,omitempty"`
	Placement            any                   `json:"placement,omitempty"`
	GeneratedArtifact    string                `json:"generatedArtifact"`
	ManagedBy            string                `json:"managedBy"`
	RuntimeDecisionPath  string                `json:"runtimeDecisionPath"`
	ContainsSecretValues bool                  `json:"containsSecretValues"`
}

type IdentityMetadata struct {
	PocketIDEnabled      bool   `json:"pocketIdEnabled"`
	TinyAuthEnabled      bool   `json:"tinyAuthEnabled"`
	TinyAuthOAuthEnabled bool   `json:"tinyAuthOAuthEnabled"`
	AuthMode             string `json:"authMode,omitempty"`
	OIDCIssuerURL        string `json:"oidcIssuerUrl,omitempty"`
	SecureCookie         bool   `json:"secureCookie"`
}

func BuildMetadata(spec *models.StackSpec, cr *CompositionResult) Metadata {
	meta := Metadata{
		SchemaVersion:        "stackkit.composition.v1",
		GeneratedArtifact:    "deploy/.stackkit/composition.json",
		ManagedBy:            "stackkit-generate",
		RuntimeDecisionPath:  "application.runtimeProfiles",
		ContainsSecretValues: false,
	}
	if spec != nil {
		meta.StackKit = spec.StackKit
		meta.InstallMode = spec.EffectiveInstallMode()
		meta.PlacementMode = spec.EffectivePlacementMode()
	}
	if cr == nil {
		return meta
	}
	meta.EnabledModules = append([]string(nil), cr.EnabledModules...)
	meta.Warnings = append([]string(nil), cr.Warnings...)
	meta.ApplicationHandoffs = append([]ControlPlaneHandoff(nil), cr.ControlPlaneHandoffs...)
	if cr.Identity != nil {
		meta.Identity = &IdentityMetadata{
			PocketIDEnabled:      cr.Identity.PocketIDEnabled,
			TinyAuthEnabled:      cr.Identity.TinyAuthEnabled,
			TinyAuthOAuthEnabled: cr.Identity.TinyAuthOAuthEnabled,
			AuthMode:             cr.Identity.AuthMode,
			OIDCIssuerURL:        cr.Identity.OIDCIssuerURL,
			SecureCookie:         cr.Identity.SecureCookie,
		}
	}
	if cr.Placement != nil {
		meta.Placement = cr.Placement
	}
	return meta
}

func GenerateMetadataJSON(spec *models.StackSpec, cr *CompositionResult) ([]byte, error) {
	data, err := json.MarshalIndent(BuildMetadata(spec, cr), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
