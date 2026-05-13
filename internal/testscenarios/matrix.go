package testscenarios

const (
	MatrixStatusCovered        = "covered"
	MatrixStatusExpectedReject = "expected-reject"
)

type MatrixReport struct {
	Entries []MatrixEntry `json:"entries"`
}

type MatrixEntry struct {
	StackKit   string `json:"stackkit"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	ScenarioID string `json:"scenarioId,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func BuildStackKitContextMatrix(scenarios []Scenario) MatrixReport {
	scenarioByID := map[string]Scenario{}
	for _, scenario := range scenarios {
		scenarioByID[scenario.ID] = scenario
	}

	entries := []MatrixEntry{
		coveredEntry("base-kit", "local", "SK-S1", scenarioByID),
		coveredEntry("base-kit", "cloud", "SK-S2", scenarioByID),
		expectedRejectEntry("base-kit", "hybrid", "Base Kit v1 is single-node; hybrid placement belongs to modern-homelab."),
		expectedRejectEntry("modern-homelab", "local", "Modern Homelab local-only rollout is pending V6 restructuring."),
		expectedRejectEntry("modern-homelab", "cloud", "Modern Homelab cloud-only rollout is pending V6 restructuring."),
		coveredEntry("modern-homelab", "hybrid", "SK-S4", scenarioByID),
		expectedRejectEntry("ha-kit", "local", "HA Kit remains scaffolding and requires a public multi-node HA contract."),
		expectedRejectEntry("ha-kit", "cloud", "HA Kit remains scaffolding until swarm/failover contracts are complete."),
		expectedRejectEntry("ha-kit", "hybrid", "HA Kit hybrid validation waits for accepted HA topology contracts."),
	}

	return MatrixReport{Entries: entries}
}

func coveredEntry(stackkit, context, scenarioID string, scenarios map[string]Scenario) MatrixEntry {
	entry := MatrixEntry{
		StackKit:   stackkit,
		Context:    context,
		Status:     MatrixStatusCovered,
		ScenarioID: scenarioID,
	}
	if _, ok := scenarios[scenarioID]; !ok {
		entry.Status = MatrixStatusExpectedReject
		entry.ScenarioID = ""
		entry.Reason = "canonical scenario " + scenarioID + " is not available"
	}
	return entry
}

func expectedRejectEntry(stackkit, context, reason string) MatrixEntry {
	return MatrixEntry{
		StackKit: stackkit,
		Context:  context,
		Status:   MatrixStatusExpectedReject,
		Reason:   reason,
	}
}
