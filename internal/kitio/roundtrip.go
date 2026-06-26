package kitio

import (
	"fmt"
	"reflect"
	"sort"
)

// LocalRoundTrip runs the YAML-only roundtrip cycle in memory:
//
//  1. Import original yaml -> KitDefinition A
//  2. Export A -> reconstructed yaml
//  3. Import reconstructed yaml -> KitDefinition B
//  4. Compare A vs B as structs (cosmetic yaml differences ignored)
//  5. Compute hashes; match indicates lossless roundtrip
//
// This is the cheap, no-API check that ships with `stackkit kit roundtrip`
// and runs in unit tests. The live API counterpart lives in client.go +
// kit_roundtrip_live_test.go.
func LocalRoundTrip(yamlBytes []byte) (RoundTripReport, error) {
	a, err := Import(yamlBytes)
	if err != nil {
		return RoundTripReport{}, fmt.Errorf("first import: %w", err)
	}

	reconstructed, err := ExportYAML(a)
	if err != nil {
		return RoundTripReport{}, fmt.Errorf("export yaml: %w", err)
	}

	b, err := Import(reconstructed)
	if err != nil {
		return RoundTripReport{}, fmt.Errorf("re-import reconstructed: %w", err)
	}

	hashA, err := CanonicalHash(a)
	if err != nil {
		return RoundTripReport{}, fmt.Errorf("hash A: %w", err)
	}
	hashB, err := CanonicalHash(b)
	if err != nil {
		return RoundTripReport{}, fmt.Errorf("hash B: %w", err)
	}

	diffs := Diff(a, b)

	report := RoundTripReport{
		Slug:              a.Metadata.Name,
		OriginalHash:      hashA,
		ReconstructedHash: hashB,
		HashesEqual:       hashA == hashB,
		Differences:       diffs,
		CosmeticOnly:      hasOnlyCosmetic(diffs),
		Formats:           []string{"yaml"},
	}
	return report, nil
}

// Diff produces a list of FieldDifference entries between two KitDefinition
// values. Tracks the most relevant top-level + nested field paths for
// roundtrip validation.
func Diff(a, b KitDefinition) []FieldDifference {
	var out []FieldDifference

	add := func(path, severity string, av, bv interface{}, note string) {
		if !reflect.DeepEqual(av, bv) {
			out = append(out, FieldDifference{
				Path:          path,
				Severity:      severity,
				Original:      av,
				Reconstructed: bv,
				Note:          note,
			})
		}
	}

	// Critical: identity + version + metadata core
	add("metadata.name", "critical", a.Metadata.Name, b.Metadata.Name, "")
	add("metadata.version", "critical", a.Metadata.Version, b.Metadata.Version, "")
	add("metadata.description", "cosmetic", a.Metadata.Description, b.Metadata.Description, "free-text drift OK")
	add("metadata.license", "critical", a.Metadata.License, b.Metadata.License, "")
	add("metadata.author", "cosmetic", a.Metadata.Author, b.Metadata.Author, "")

	// Top-level lists
	if !sortedEqual(a.SupportedOS, b.SupportedOS) {
		add("supportedOS", "critical", a.SupportedOS, b.SupportedOS, "set semantics")
	}

	// Modes (key set + key engine fields)
	for k, va := range a.Modes {
		vb, ok := b.Modes[k]
		if !ok {
			out = append(out, FieldDifference{Path: "modes." + k, Severity: "critical", Original: va, Note: "missing in reconstructed"})
			continue
		}
		add("modes."+k+".engine", "critical", va.Engine, vb.Engine, "")
		add("modes."+k+".templateDir", "critical", va.TemplateDir, vb.TemplateDir, "")
		add("modes."+k+".description", "cosmetic", va.Description, vb.Description, "")
	}
	for k := range b.Modes {
		if _, ok := a.Modes[k]; !ok {
			out = append(out, FieldDifference{Path: "modes." + k, Severity: "critical", Reconstructed: b.Modes[k], Note: "added in reconstructed"})
		}
	}

	// application / foundation / platform / computeTiers — compare key sets
	compareKeySet(&out, "application", mapKeysGeneric(a.Application), mapKeysGeneric(b.Application), "critical")
	compareKeySet(&out, "foundation", mapKeysGeneric(a.Foundation), mapKeysGeneric(b.Foundation), "critical")
	compareKeySet(&out, "computeTiers", mapKeysGeneric(a.ComputeTiers), mapKeysGeneric(b.ComputeTiers), "critical")

	// Platform may be string or map
	if a.Platform.AsString != b.Platform.AsString {
		add("platform(string)", "critical", a.Platform.AsString, b.Platform.AsString, "")
	}
	platformA := normalizePlatform(a.Platform.AsMap)
	platformB := normalizePlatform(b.Platform.AsMap)
	compareKeySet(&out, "platform", mapKeysGeneric(platformA), mapKeysGeneric(platformB), "critical")

	// Per-application role + defaultTool
	for k, va := range a.Application {
		vb := b.Application[k]
		add("application."+k+".role", "critical", string(va.Role), string(vb.Role), "")
		add("application."+k+".defaultTool", "critical", va.DefaultTool, vb.DefaultTool, "")
		add("application."+k+".description", "cosmetic", va.Description, vb.Description, "free-text drift OK")
		add("application."+k+".package", "critical", va.Package, vb.Package, "")
		add("application."+k+".defaultRuntimeProfile", "critical", va.DefaultRuntimeProfile, vb.DefaultRuntimeProfile, "")
		if !sortedEqual(va.Alternatives, vb.Alternatives) {
			add("application."+k+".alternatives", "critical", va.Alternatives, vb.Alternatives, "")
		}
		add("application."+k+".runtimeProfiles", "critical", va.RuntimeProfiles, vb.RuntimeProfiles, "")
		add("application."+k+".connectors", "critical", va.Connectors, vb.Connectors, "")
		add("application."+k+".productApis", "critical", va.ProductAPIs, vb.ProductAPIs, "")
		add("application."+k+".ril", "critical", va.RIL, vb.RIL, "")
	}

	// Per-foundation role
	for k, va := range a.Foundation {
		vb := b.Foundation[k]
		add("foundation."+k+".role", "critical", string(va.Role), string(vb.Role), "")
	}

	// Per-platform role + defaultTool
	for group, va := range platformA {
		vb := platformB[group]
		add("platform."+group+".role", "critical", string(va.Role), string(vb.Role), "")
		if !platformDefaultEqual(va.Keys, va.DefaultTool, vb.Keys, vb.DefaultTool) {
			add("platform."+group+".defaultTool", "critical", va.DefaultTool, vb.DefaultTool, "")
		}
		if !sortedEqual(va.Alternatives, vb.Alternatives) {
			add("platform."+group+".alternatives", "critical", va.Alternatives, vb.Alternatives, "")
		}
	}

	// computeTiers requirements
	for k, va := range a.ComputeTiers {
		vb := b.ComputeTiers[k]
		add("computeTiers."+k+".requirements", "critical", va.Requirements, vb.Requirements, "")
		if !sortedEqual(va.AdditionalServices, vb.AdditionalServices) {
			add("computeTiers."+k+".additionalServices", "critical", va.AdditionalServices, vb.AdditionalServices, "")
		}
	}

	// Features map. A missing features block and an empty features block are
	// semantically equivalent after DB export.
	if !boolMapEqual(a.Features, b.Features) {
		add("features", "critical", a.Features, b.Features, "")
	}

	return out
}

func compareKeySet(out *[]FieldDifference, label string, a, b []string, severity string) {
	sort.Strings(a)
	sort.Strings(b)
	if !reflect.DeepEqual(a, b) {
		*out = append(*out, FieldDifference{
			Path:          label,
			Severity:      severity,
			Original:      a,
			Reconstructed: b,
			Note:          "key set differs",
		})
	}
}

func mapKeysGeneric[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	return reflect.DeepEqual(aa, bb)
}

func boolMapEqual(a, b map[string]bool) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

type normalizedPlatformDef struct {
	Role         string
	DefaultTool  string
	Alternatives []string
	Keys         []string
}

func normalizePlatform(in map[string]PlatformDef) map[string]normalizedPlatformDef {
	out := make(map[string]normalizedPlatformDef, len(in))
	for key, value := range in {
		group := key
		if mapped, ok := PlatformToGroup[key]; ok {
			group = mapped
		}
		current := out[group]
		if current.Role == "" {
			current.Role = value.Role
		}
		if current.DefaultTool == "" {
			current.DefaultTool = value.DefaultTool
		}
		current.Alternatives = mergeStringSet(current.Alternatives, value.Alternatives)
		current.Keys = mergeStringSet(current.Keys, []string{key})
		out[group] = current
	}
	return out
}

func platformDefaultEqual(aKeys []string, aDefault string, bKeys []string, bDefault string) bool {
	if aDefault == bDefault {
		return true
	}
	if aDefault == "" && containsString(aKeys, bDefault) {
		return true
	}
	if bDefault == "" && containsString(bKeys, aDefault) {
		return true
	}
	return false
}

func mergeStringSet(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for _, value := range a {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	for _, value := range b {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func hasOnlyCosmetic(diffs []FieldDifference) bool {
	for _, d := range diffs {
		if d.Severity == "critical" {
			return false
		}
	}
	return true
}
