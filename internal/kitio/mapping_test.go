package kitio

import (
	"testing"
)

// TestMappingsAreInvertible asserts that every (section, key) -> service-group
// mapping is recoverable: if foundation.X maps to group G, GroupReverseMappings(G)
// must contain a Foundation entry with key X. Otherwise round-trip would
// silently lose the section context.
func TestMappingsAreInvertible(t *testing.T) {
	cases := []struct {
		section SectionKind
		fwd     map[string]string
	}{
		{SectionFoundation, FoundationToGroup},
		{SectionPlatform, PlatformToGroup},
		{SectionApplication, ApplicationToGroup},
	}

	for _, tc := range cases {
		for key, group := range tc.fwd {
			mappings := GroupReverseMappings(group)
			found := false
			for _, m := range mappings {
				if m.Section == tc.section && m.Key == key {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("section=%s key=%q -> group=%q: reverse mapping missing", tc.section, key, group)
			}
		}
	}
}

// TestPreferredSectionPriorityOrder checks that when a service group has
// multiple sources (e.g. forward-auth from platform.login-gateway AND
// platform.tinyauth), the V6-canonical key wins deterministically.
//
// Migration 000086 moved login-gateway from foundation to platform per
// ARCHITECTURE_V6 §4. Both candidates now sit in the platform section;
// canonicalKeyPriority + the deterministic compare in PreferredSection
// keep login-gateway as the canonical pick.
func TestPreferredSectionPriorityOrder(t *testing.T) {
	// forward-auth has two mappings, both in platform now
	section, key, ok := PreferredSection("forward-auth")
	if !ok {
		t.Fatal("forward-auth has no mapping")
	}
	if section != SectionPlatform {
		t.Errorf("forward-auth: want section=platform (per V6 §4), got %s", section)
	}
	if key != "login-gateway" {
		t.Errorf("forward-auth: want key=login-gateway (canonical over tinyauth alias), got %s", key)
	}

	// reverse-proxy is platform-only
	section, key, ok = PreferredSection("reverse-proxy")
	if !ok {
		t.Fatal("reverse-proxy has no mapping")
	}
	if section != SectionPlatform {
		t.Errorf("reverse-proxy: want section=platform, got %s", section)
	}
	if key != "traefik" {
		t.Errorf("reverse-proxy: want key=traefik, got %s", key)
	}

	// photo-management is application-only
	section, key, ok = PreferredSection("photo-management")
	if !ok {
		t.Fatal("photo-management has no mapping")
	}
	if section != SectionApplication {
		t.Errorf("photo-management: want section=application, got %s", section)
	}
	if key != "photos" {
		t.Errorf("photo-management: want key=photos, got %s", key)
	}
}

// TestUnknownGroupReturnsNothing verifies that querying an unknown service
// group returns the zero values (and ok=false).
func TestUnknownGroupReturnsNothing(t *testing.T) {
	if mappings := GroupReverseMappings("nonexistent-group"); len(mappings) != 0 {
		t.Errorf("expected empty mappings for unknown group; got %v", mappings)
	}
	if _, _, ok := PreferredSection("nonexistent-group"); ok {
		t.Error("PreferredSection returned ok=true for unknown group")
	}
}
