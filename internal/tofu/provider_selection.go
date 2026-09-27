package tofu

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// providerLockForConfiguration uses HCL provider declarations, not text
// matches: comments and resource values cannot add a provider to a root lock.
func providerLockForConfiguration(config []byte, manifest providerManifest) ([]byte, error) {
	file, diagnostics := hclsyntax.ParseConfig(config, "main.tf", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("%w: root configuration is invalid HCL", ErrProviderClosureInvalid)
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("%w: root has no HCL body", ErrProviderClosureInvalid)
	}
	bySource := make(map[string]providerManifestEntry, len(manifest.Providers))
	for _, provider := range manifest.Providers {
		bySource[provider.Source] = provider
	}
	selected := make(map[string]bool)
	for _, block := range body.Blocks {
		if block.Type != "terraform" {
			continue
		}
		for _, nested := range block.Body.Blocks {
			if nested.Type != "required_providers" {
				continue
			}
			for name, attribute := range nested.Body.Attributes {
				value, diagnostics := attribute.Expr.Value(nil)
				if diagnostics.HasErrors() || !value.IsKnown() || value.IsNull() || !value.Type().IsObjectType() {
					return nil, fmt.Errorf("%w: provider %s has no static declaration", ErrProviderClosureInvalid, name)
				}
				fields := value.AsValueMap()
				sourceValue, sourceOK := fields["source"]
				versionValue, versionOK := fields["version"]
				if !sourceOK || !versionOK || sourceValue.Type() != cty.String || versionValue.Type() != cty.String ||
					!sourceValue.IsKnown() || sourceValue.IsNull() || !versionValue.IsKnown() || versionValue.IsNull() {
					return nil, fmt.Errorf("%w: provider %s requires a static source and exact version", ErrProviderClosureInvalid, name)
				}
				source := sourceValue.AsString()
				if strings.Count(source, "/") == 1 {
					source = ProviderRegistryHost + "/" + source
				}
				provider, known := bySource[source]
				if !known || (versionValue.AsString() != provider.Version && versionValue.AsString() != "= "+provider.Version) || selected[source] {
					return nil, fmt.Errorf("%w: provider %s is absent, unpinned or repeated", ErrProviderClosureInvalid, name)
				}
				selected[source] = true
			}
		}
	}
	if len(selected) == 0 {
		return []byte(emptyProviderLock), nil
	}
	filtered := providerManifest{SchemaVersion: manifest.SchemaVersion}
	for _, provider := range manifest.Providers {
		if selected[provider.Source] {
			filtered.Providers = append(filtered.Providers, provider)
		}
	}
	return renderProviderLock(filtered), nil
}
