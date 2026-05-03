package kitio

import (
	"fmt"
	"reflect"
	"sort"
)

// LocalRoundTrip runs the YAML-only roundtrip cycle in memory:
//
//	1. Import original yaml -> KitDefinition A
//	2. Export A -> reconstructed yaml
//	3. Import reconstructed yaml -> KitDefinition B
//	4. Compare A vs B as structs (cosmetic yaml differences ignored)
//	5. Compute hashes; match indicates lossless roundtrip
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
				Path:     path,
				Severity: severity,
				Original: av,
				Reconstructed: bv,
				Note:     note,
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
	compareKeySet(&out, "platform", mapKeysGeneric(a.Platform.AsMap), mapKeysGeneric(b.Platform.AsMap), "critical")

	// Per-application role + defaultTool
	for k, va := range a.Application {
		vb := b.Application[k]
		add("application."+k+".role", "critical", string(va.Role), string(vb.Role), "")
		add("application."+k+".defaultTool", "critical", va.DefaultTool, vb.DefaultTool, "")
		if !sortedEqual(va.Alternatives, vb.Alternatives) {
			add("application."+k+".alternatives", "critical", va.Alternatives, vb.Alternatives, "")
		}
	}

	// Per-foundation role
	for k, va := range a.Foundation {
		vb := b.Foundation[k]
		add("foundation."+k+".role", "critical", string(va.Role), string(vb.Role), "")
	}

	// Per-platform role + defaultTool
	for k, va := range a.Platform.AsMap {
		vb := b.Platform.AsMap[k]
		add("platform."+k+".role", "critical", string(va.Role), string(vb.Role), "")
		add("platform."+k+".defaultTool", "critical", va.DefaultTool, vb.DefaultTool, "")
	}

	// computeTiers requirements
	for k, va := range a.ComputeTiers {
		vb := b.ComputeTiers[k]
		add("computeTiers."+k+".requirements", "critical", va.Requirements, vb.Requirements, "")
		if !sortedEqual(va.AdditionalServices, vb.AdditionalServices) {
			add("computeTiers."+k+".additionalServices", "critical", va.AdditionalServices, vb.AdditionalServices, "")
		}
	}

	// Features map
	if !reflect.DeepEqual(a.Features, b.Features) {
		add("features", "critical", a.Features, b.Features, "")
	}

	return out
}

func compareKeySet(out *[]FieldDifference, label string, a, b []string, severity string) {
	sort.Strings(a)
	sort.Strings(b)
	if !reflect.DeepEqual(a, b) {
		*out = append(*out, FieldDifference{
			Path:     label,
			Severity: severity,
			Original: a,
			Reconstructed: b,
			Note:     "key set differs",
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

func hasOnlyCosmetic(diffs []FieldDifference) bool {
	for _, d := range diffs {
		if d.Severity == "critical" {
			return false
		}
	}
	return true
}
