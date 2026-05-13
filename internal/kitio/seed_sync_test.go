package kitio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestServiceGroupMappingMatchesDBSeed asserts that the hardcoded Go maps
// (ApplicationToGroup / FoundationToGroup / PlatformToGroup in mapping.go)
// agree with the frozen snapshot of the kombify-DB migration 000082 seed
// in testdata/service_group_seed.json.
//
// Drift between Go and DB causes silent breakage at the kit-import /
// kit-export boundary: the Go CLI thinks "platform.traefik" maps to
// reverse-proxy while DB doesn't, or vice versa. Hash mismatches and
// 404 errors follow.
//
// To intentionally update the seed:
//  1. Edit kombify-DB/migrations/000082_sk_service_group_yaml_mapping.up.sql
//     (or write a new follow-up migration with ALTER ... yaml_section).
//  2. Update kombify-StackKits/internal/kitio/mapping.go to match.
//  3. Update testdata/service_group_seed.json to reflect the new state.
//  4. Run this test — it must pass.
//
// Regenerate the test data:
//
//	psql -At "$ADMIN_DATABASE_URL" -c "
//	  SELECT json_build_object(
//	    'mappings', json_agg(json_build_object(
//	      'section', yaml_section,
//	      'key', yaml_key,
//	      'groupSlug', slug
//	    )))
//	  FROM sk_service_group
//	  WHERE yaml_section <> '';
//	"
type seedFile struct {
	Mappings []struct {
		Section   string `json:"section"`
		Key       string `json:"key"`
		GroupSlug string `json:"groupSlug"`
	} `json:"mappings"`
}

func TestServiceGroupMappingMatchesDBSeed(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	seedPath := filepath.Join(filepath.Dir(file), "testdata", "service_group_seed.json")
	raw, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	var seed seedFile
	if err := json.Unmarshal(raw, &seed); err != nil {
		t.Fatalf("decode seed: %v", err)
	}

	// Build expected: yaml-key -> group-slug per section.
	expectedFoundation := map[string]string{}
	expectedPlatform := map[string]string{}
	expectedApplication := map[string]string{}

	for _, m := range seed.Mappings {
		switch m.Section {
		case "foundation":
			expectedFoundation[m.Key] = m.GroupSlug
		case "platform":
			expectedPlatform[m.Key] = m.GroupSlug
		case "application":
			expectedApplication[m.Key] = m.GroupSlug
		default:
			t.Errorf("unknown section %q in seed mapping for key=%s — expected foundation/platform/application per ADR-0012 layer standard", m.Section, m.Key)
		}
	}

	assertMapEqual(t, "FoundationToGroup", FoundationToGroup, expectedFoundation)
	assertMapEqual(t, "PlatformToGroup", PlatformToGroup, expectedPlatform)
	assertMapEqual(t, "ApplicationToGroup", ApplicationToGroup, expectedApplication)
}

func assertMapEqual(t *testing.T, name string, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if g, ok := got[k]; !ok {
			t.Errorf("[%s] missing key %q (DB seed has it -> %q; Go map does not)", name, k, v)
		} else if g != v {
			t.Errorf("[%s] key %q: Go=%q != DB=%q", name, k, g, v)
		}
	}
	for k, v := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("[%s] extra key %q (Go has it -> %q; DB seed does not)", name, k, v)
		}
	}
}
