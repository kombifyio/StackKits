package models

import "strings"

// legacyStackKitNames maps retired StackKit slugs to their canonical successor.
//
// "base-kit" was retired when the single base kit was split into the two derived
// products Basement Kit (local) and Cloud Kit (cloud) — the shared ~90% core
// moved into the base/ library and base-kit is no longer an installable kit
// (ADR-0026). Old stack-spec.yaml files and `stackkit init base-kit` invocations
// are normalized to the local product, basement-kit, with a deprecation warning
// at the call sites.
var legacyStackKitNames = map[string]string{
	"base-kit": "basement-kit",
}

// IsLegacyStackKitName reports whether name is a retired StackKit alias that
// will be normalized to a canonical name.
func IsLegacyStackKitName(name string) bool {
	_, ok := legacyStackKitNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// NormalizeStackKitName maps a retired StackKit alias to its canonical name.
// Unknown or already-canonical names (including the empty string) are returned
// unchanged so callers can apply it unconditionally.
func NormalizeStackKitName(name string) string {
	if canonical, ok := legacyStackKitNames[strings.ToLower(strings.TrimSpace(name))]; ok {
		return canonical
	}
	return name
}
