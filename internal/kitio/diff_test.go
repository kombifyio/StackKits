package kitio

import (
	"testing"
)

// Direct unit tests for Diff() — independent of import/export plumbing.
// Each test constructs two KitDefinitions and asserts the produced
// FieldDifference list matches expectations.

func TestDiff_IdenticalKitsHaveZeroDiffs(t *testing.T) {
	def := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1.0.0"},
		Modes: map[string]ModeDef{
			"simple": {Engine: "opentofu"},
		},
	}
	diffs := Diff(def, def)
	if len(diffs) != 0 {
		t.Errorf("identical kits produced %d diffs: %+v", len(diffs), diffs)
	}
}

func TestDiff_VersionChange_IsCritical(t *testing.T) {
	a := KitDefinition{Metadata: KitMetadata{Name: "k", Version: "1.0.0"}}
	b := KitDefinition{Metadata: KitMetadata{Name: "k", Version: "2.0.0"}}
	diffs := Diff(a, b)
	if !hasCritical(diffs, "metadata.version") {
		t.Errorf("expected critical metadata.version diff; got %+v", diffs)
	}
}

func TestDiff_DescriptionChange_IsCosmetic(t *testing.T) {
	a := KitDefinition{Metadata: KitMetadata{Name: "k", Version: "1", Description: "old"}}
	b := KitDefinition{Metadata: KitMetadata{Name: "k", Version: "1", Description: "new"}}
	diffs := Diff(a, b)
	if !hasCosmetic(diffs, "metadata.description") {
		t.Errorf("expected cosmetic metadata.description diff; got %+v", diffs)
	}
	if hasCritical(diffs, "metadata.description") {
		t.Errorf("description change leaked to critical severity")
	}
}

func TestDiff_MissingModeIsCritical(t *testing.T) {
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Modes:    map[string]ModeDef{"simple": {Engine: "opentofu"}, "advanced": {Engine: "terramate"}},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Modes:    map[string]ModeDef{"simple": {Engine: "opentofu"}},
	}
	diffs := Diff(a, b)
	if !hasCritical(diffs, "modes.advanced") {
		t.Errorf("expected critical 'modes.advanced missing' diff; got %+v", diffs)
	}
}

func TestDiff_AddedModeIsCritical(t *testing.T) {
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Modes:    map[string]ModeDef{"simple": {Engine: "opentofu"}},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Modes:    map[string]ModeDef{"simple": {Engine: "opentofu"}, "extra": {Engine: "terramate"}},
	}
	diffs := Diff(a, b)
	if !hasCritical(diffs, "modes.extra") {
		t.Errorf("expected critical 'modes.extra added' diff; got %+v", diffs)
	}
}

func TestDiff_PlatformStringVsMap_IsCritical(t *testing.T) {
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Platform: PlatformField{AsString: "docker"},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Platform: PlatformField{AsMap: map[string]PlatformDef{"traefik": {Role: "default"}}},
	}
	diffs := Diff(a, b)
	if len(diffs) == 0 {
		t.Fatal("expected diffs for platform string-vs-map; got 0")
	}
	if !hasCritical(diffs, "platform(string)") && !hasCritical(diffs, "platform") {
		t.Errorf("expected critical platform diff; got %+v", diffs)
	}
}

func TestDiff_PlatformAliasesUseServiceGroupSemantics(t *testing.T) {
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Platform: PlatformField{AsMap: map[string]PlatformDef{
			"tinyauth":      {Role: "default"},
			"login-gateway": {Role: "default"},
		}},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Platform: PlatformField{AsMap: map[string]PlatformDef{
			"login-gateway": {Role: "default", DefaultTool: "tinyauth"},
		}},
	}
	diffs := Diff(a, b)
	if hasCritical(diffs, "platform") {
		t.Errorf("platform aliases leaked to critical diff: %+v", diffs)
	}
}

func TestDiff_PlatformSelfDefaultIsEquivalentToOmittedDefault(t *testing.T) {
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Platform: PlatformField{AsMap: map[string]PlatformDef{
			"traefik": {Role: "default"},
		}},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Platform: PlatformField{AsMap: map[string]PlatformDef{
			"traefik": {Role: "default", DefaultTool: "traefik"},
		}},
	}
	diffs := Diff(a, b)
	if hasCritical(diffs, "platform.reverse-proxy.defaultTool") {
		t.Errorf("self default leaked to critical diff: %+v", diffs)
	}
}

func TestDiff_AlternativeOrderingIsNotCritical(t *testing.T) {
	// Alternatives are a set semantically — order shouldn't matter.
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Application: map[string]ApplicationDef{
			"photos": {Role: "default", DefaultTool: "immich", Alternatives: []string{"ente", "stingle"}},
		},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Application: map[string]ApplicationDef{
			"photos": {Role: "default", DefaultTool: "immich", Alternatives: []string{"stingle", "ente"}},
		},
	}
	diffs := Diff(a, b)
	for _, d := range diffs {
		if d.Severity == "critical" && d.Path == "application.photos.alternatives" {
			t.Errorf("alternative ordering leaked to critical: %+v", d)
		}
	}
}

func TestDiff_DefaultToolChangeIsCritical(t *testing.T) {
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Application: map[string]ApplicationDef{
			"photos": {Role: "default", DefaultTool: "immich"},
		},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Application: map[string]ApplicationDef{
			"photos": {Role: "default", DefaultTool: "ente-photos"},
		},
	}
	diffs := Diff(a, b)
	if !hasCritical(diffs, "application.photos.defaultTool") {
		t.Errorf("expected critical defaultTool diff; got %+v", diffs)
	}
}

func TestDiff_FeatureFlagChangeIsCritical(t *testing.T) {
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Features: map[string]bool{"multiNode": false},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Features: map[string]bool{"multiNode": true},
	}
	diffs := Diff(a, b)
	if !hasCritical(diffs, "features") {
		t.Errorf("expected critical features diff; got %+v", diffs)
	}
}

func TestDiff_EmptyFeaturesAndMissingFeaturesAreEquivalent(t *testing.T) {
	a := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
		Features: map[string]bool{},
	}
	b := KitDefinition{
		Metadata: KitMetadata{Name: "k", Version: "1"},
	}
	diffs := Diff(a, b)
	if hasCritical(diffs, "features") {
		t.Errorf("empty features leaked to critical diff: %+v", diffs)
	}
}

func TestDiff_ComputeTierRequirementsChangeIsCritical(t *testing.T) {
	a := KitDefinition{
		Metadata:     KitMetadata{Name: "k", Version: "1"},
		ComputeTiers: map[string]ComputeTierDef{"standard": {Requirements: ResourceRequirements{CPU: 4, Memory: 8}}},
	}
	b := KitDefinition{
		Metadata:     KitMetadata{Name: "k", Version: "1"},
		ComputeTiers: map[string]ComputeTierDef{"standard": {Requirements: ResourceRequirements{CPU: 8, Memory: 16}}},
	}
	diffs := Diff(a, b)
	if !hasCritical(diffs, "computeTiers.standard.requirements") {
		t.Errorf("expected critical tier requirements diff; got %+v", diffs)
	}
}

// Helpers
func hasCritical(diffs []FieldDifference, pathPrefix string) bool {
	for _, d := range diffs {
		if d.Severity == "critical" && (d.Path == pathPrefix || hasPathPrefix(d.Path, pathPrefix)) {
			return true
		}
	}
	return false
}

func hasCosmetic(diffs []FieldDifference, path string) bool {
	for _, d := range diffs {
		if d.Severity == "cosmetic" && d.Path == path {
			return true
		}
	}
	return false
}

func hasPathPrefix(path, prefix string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}
