package testscenarios

import (
	"slices"
	"strings"
	"testing"
)

func TestLoadAllReturnsCanonicalScenarios(t *testing.T) {
	scenarios, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll returned error: %v", err)
	}
	if len(scenarios) != 7 {
		t.Fatalf("expected 7 canonical scenarios, got %d", len(scenarios))
	}

	gotIDs := make([]string, 0, len(scenarios))
	seen := map[string]bool{}
	for _, scenario := range scenarios {
		if scenario.ID == "" {
			t.Fatal("scenario ID must not be empty")
		}
		if seen[scenario.ID] {
			t.Fatalf("duplicate scenario ID %q", scenario.ID)
		}
		seen[scenario.ID] = true
		gotIDs = append(gotIDs, scenario.ID)

		if scenario.Name == "" {
			t.Fatalf("%s has empty name", scenario.ID)
		}
		if scenario.StackSpec.StackKit == "" {
			t.Fatalf("%s has empty stackkit", scenario.ID)
		}
	}

	for _, want := range []string{"SK-S1", "SK-S2", "SK-S2A", "SK-S3", "SK-S3A", "SK-S4", "SK-S5"} {
		if !slices.Contains(gotIDs, want) {
			t.Fatalf("missing scenario %s in %v", want, gotIDs)
		}
	}
}

func TestByIDReturnsScenario(t *testing.T) {
	scenario, err := ByID("SK-S2")
	if err != nil {
		t.Fatalf("ByID returned error: %v", err)
	}
	if scenario.Name != "Cloud OneClick kombify.me" {
		t.Fatalf("unexpected scenario name: %q", scenario.Name)
	}
	if scenario.StackSpec.Domain != "kombify.me" {
		t.Fatalf("unexpected scenario domain: %q", scenario.StackSpec.Domain)
	}
}

func TestLiveScenariosDeclareDashboardLinksAndServices(t *testing.T) {
	scenarios, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll returned error: %v", err)
	}

	for _, scenario := range scenarios {
		if !scenario.HasRunnableHomelab() {
			continue
		}
		if scenario.Expected.Access.HubURL == "" {
			t.Fatalf("%s live scenario must declare expected hubUrl", scenario.ID)
		}
		if len(scenario.Expected.Access.Services) == 0 {
			t.Fatalf("%s live scenario must declare expected services", scenario.ID)
		}
	}
}

func TestNewArtifactUsesPublicHubURLAsBrowserURL(t *testing.T) {
	scenario, err := ByID("SK-S2")
	if err != nil {
		t.Fatalf("ByID returned error: %v", err)
	}

	artifact := NewArtifact(scenario, "run-123", "passed", ObservedAccess{
		HubURL: "https://sh-scenario-s2-base.kombify.me",
		Services: []ObservedService{{
			Key:  "base",
			URL:  "https://sh-scenario-s2-base.kombify.me",
			Host: "sh-scenario-s2-base.kombify.me",
		}},
	}, Target{PublicIP: "203.0.113.10"})

	if artifact.ScenarioID != "SK-S2" || artifact.RunID != "run-123" || artifact.Status != "passed" {
		t.Fatalf("unexpected artifact identity: %+v", artifact)
	}
	if artifact.BrowserURL != artifact.HubURL {
		t.Fatalf("public artifact browserUrl = %q, want hubUrl %q", artifact.BrowserURL, artifact.HubURL)
	}
}

func TestNewArtifactBuildsLocalBrowserURLWithHostMappingHint(t *testing.T) {
	scenario, err := ByID("SK-S1")
	if err != nil {
		t.Fatalf("ByID returned error: %v", err)
	}

	artifact := NewArtifact(scenario, "local-run", "passed", ObservedAccess{
		HubURL: "https://base.stack.home",
		Services: []ObservedService{{
			Key:  "base",
			URL:  "https://base.stack.home",
			Host: "base.stack.home",
		}},
	}, Target{Host: "127.0.0.1", HTTPPort: 32780, HTTPSPort: 32743, ContainerName: "stackkits-e2e"})

	if artifact.BrowserURL != "https://base.stack.home:32743" {
		t.Fatalf("local browserUrl = %q", artifact.BrowserURL)
	}
	if !strings.Contains(artifact.LogsHint, "base.stack.home") || !strings.Contains(artifact.LogsHint, "127.0.0.1") {
		t.Fatalf("local logsHint should include host mapping guidance, got %q", artifact.LogsHint)
	}
}

func TestResolverOnlyScenariosDeclarePlacementContracts(t *testing.T) {
	scenario, err := ByID("SK-S4")
	if err != nil {
		t.Fatalf("ByID returned error: %v", err)
	}

	placement := scenario.Expected.Placement
	if placement.PublicNode == "" || placement.LocalNode == "" || placement.OwnerEmail == "" {
		t.Fatalf("SK-S4 placement contract is incomplete: %+v", placement)
	}
	nodeNames := map[string]bool{}
	for _, node := range scenario.StackSpec.Nodes {
		nodeNames[node.Name] = true
	}
	if !nodeNames[placement.PublicNode] || !nodeNames[placement.LocalNode] {
		t.Fatalf("SK-S4 placement nodes missing from spec: placement=%+v nodes=%v", placement, nodeNames)
	}
	if scenario.StackSpec.Owner.Email != placement.OwnerEmail {
		t.Fatalf("SK-S4 owner email = %q, want %q", scenario.StackSpec.Owner.Email, placement.OwnerEmail)
	}
}

func TestStackKitContextMatrixReportCoversAllReleaseCombinations(t *testing.T) {
	scenarios, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll returned error: %v", err)
	}

	report := BuildStackKitContextMatrix(scenarios)
	if len(report.Entries) != 9 {
		t.Fatalf("expected 9 StackKit x context entries, got %d", len(report.Entries))
	}

	for _, entry := range report.Entries {
		if entry.StackKit == "" || entry.Context == "" {
			t.Fatalf("matrix entry missing identity: %+v", entry)
		}
		switch entry.Status {
		case MatrixStatusCovered:
			if entry.ScenarioID == "" {
				t.Fatalf("covered matrix entry must name scenario: %+v", entry)
			}
		case MatrixStatusExpectedReject:
			if entry.Reason == "" {
				t.Fatalf("expected-reject matrix entry must explain reason: %+v", entry)
			}
		default:
			t.Fatalf("matrix entry must be covered or expected-reject for CI: %+v", entry)
		}
	}

	assertMatrixEntry(t, report, "base-kit", "local", MatrixStatusCovered, "SK-S1")
	assertMatrixEntry(t, report, "base-kit", "cloud", MatrixStatusCovered, "SK-S2")
	assertMatrixEntry(t, report, "modern-homelab", "hybrid", MatrixStatusCovered, "SK-S4")
	assertMatrixEntry(t, report, "ha-kit", "cloud", MatrixStatusExpectedReject, "")
}

func assertMatrixEntry(t *testing.T, report MatrixReport, stackkit, context, status, scenarioID string) {
	t.Helper()
	for _, entry := range report.Entries {
		if entry.StackKit == stackkit && entry.Context == context {
			if entry.Status != status {
				t.Fatalf("%s/%s status = %q, want %q", stackkit, context, entry.Status, status)
			}
			if scenarioID != "" && entry.ScenarioID != scenarioID {
				t.Fatalf("%s/%s scenario = %q, want %q", stackkit, context, entry.ScenarioID, scenarioID)
			}
			return
		}
	}
	t.Fatalf("matrix missing %s/%s", stackkit, context)
}
