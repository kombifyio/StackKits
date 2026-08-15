package architecturev2renderer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const (
	monitoringAgentModuleID    = "stackkits-monitoring-agent-runtime"
	monitoringAgentUnitID      = "collector-intent"
	monitoringAgentRendererRef = "stackkit"
	monitoringAgentTemplateRef = "builtin://observability/monitoring-agent/collector-intent/v1.json"
	monitoringAgentVersion     = "1.1.0"
	monitoringAgentOutputRef   = "observability/monitoring-agent/collector-intent.json"
	monitoringAgentTargetToken = "@@TARGET@@"
	monitoringAgentIntentToken = "@@OBSERVABILITY@@"
)

const monitoringAgentIntentTemplate = `{"apiVersion":"stackkit.monitoring-agent-intent/v1","kind":"MonitoringAgentIntent","contract":{"credentialCustody":"external","lifecycle":"external-owner-bound","providerLifecycle":"not-owned","runtimeEnforcement":"post-apply-evidence-required","scope":"runtime-owner-input"},"target":@@TARGET@@,"observability":@@OBSERVABILITY@@}
`

var monitoringAgentPlanInputRefs = []string{"observability", "sites"}

type monitoringAgentIntentRenderer struct {
	template []byte
	contract RendererContract
}

// MonitoringAgentIntentRendererContract returns the exact built-in identity
// for the provider-free collector intent. The immutable template declares that
// runtime lifecycle is bound through an external owner while credentials and
// provider ownership remain outside StackKits.
func MonitoringAgentIntentRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(monitoringAgentIntentTemplate))
	return RendererContract{
		Kind:         "native-config",
		RendererRef:  monitoringAgentRendererRef,
		TemplateRef:  monitoringAgentTemplateRef,
		Version:      monitoringAgentVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

func newMonitoringAgentIntentRenderer() monitoringAgentIntentRenderer {
	return monitoringAgentIntentRenderer{
		template: []byte(monitoringAgentIntentTemplate),
		contract: MonitoringAgentIntentRendererContract(),
	}
}

func (r monitoringAgentIntentRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planInputs, err := validateMonitoringAgentIntentUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	if monitoringAgentTemplateHash(r.template) != r.contract.ContractHash ||
		bytes.Count(r.template, []byte(monitoringAgentTargetToken)) != 1 ||
		bytes.Count(r.template, []byte(monitoringAgentIntentToken)) != 1 {
		return nil, fail(ErrOutputChanged, "renderer.monitoring-agent.template", "embedded collector-intent manifest does not match its registered contract")
	}

	var inputs monitoringAgentPlanInputs
	if err := decodeStrict(planInputs, &inputs); err != nil {
		return nil, wrap(ErrInvalidPlan, "renderer.monitoring-agent.planInputs", "decode exact collector intent", err)
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if !hasSite || !hasNode {
		return nil, fail(ErrInvalidPlan, "renderer.monitoring-agent.target", "collector intent requires one exact Site/node target")
	}
	target, err := json.Marshal(monitoringAgentTarget{SiteRef: siteRef, NodeRef: nodeRef})
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.monitoring-agent.target", "encode target", err)
	}
	observability, err := json.Marshal(inputs.Observability)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.monitoring-agent.observability", "encode collector intent", err)
	}
	output := bytes.Replace(r.template, []byte(monitoringAgentTargetToken), target, 1)
	output = bytes.Replace(output, []byte(monitoringAgentIntentToken), observability, 1)
	return []UnitOutput{{Ref: monitoringAgentOutputRef, Bytes: output}}, nil
}

func monitoringAgentTemplateHash(template []byte) string {
	sum := sha256.Sum256(template)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type monitoringAgentPlanInputs struct {
	Observability monitoringAgentObservability `json:"observability"`
	Sites         []localAutonomySite          `json:"sites"`
}

type monitoringAgentTarget struct {
	SiteRef string `json:"siteRef"`
	NodeRef string `json:"nodeRef"`
}

type monitoringAgentObservability struct {
	Profile         string                          `json:"profile"`
	AgentBudget     monitoringAgentBudget           `json:"agentBudget"`
	Signals         monitoringAgentSignals          `json:"signals"`
	Collector       monitoringAgentCollector        `json:"collector"`
	OptionalSignals *monitoringAgentOptionalSignals `json:"optionalSignals,omitempty"`
}

type monitoringAgentSignals struct {
	Metrics bool `json:"metrics"`
	Logs    bool `json:"logs"`
	Traces  bool `json:"traces"`
}

type monitoringAgentCollector struct {
	Enabled  bool                        `json:"enabled"`
	Endpoint string                      `json:"endpoint"`
	Protocol string                      `json:"protocol"`
	TLS      monitoringAgentCollectorTLS `json:"tls"`
}

type monitoringAgentCollectorTLS struct {
	Insecure bool `json:"insecure"`
}

type monitoringAgentBudget struct {
	CPUMilli     int `json:"cpuMilli"`
	MemoryMiB    int `json:"memoryMiB"`
	EphemeralMiB int `json:"ephemeralMiB"`
}

type monitoringAgentOptionalBudget struct {
	Enabled          bool `json:"enabled"`
	CPUMilli         int  `json:"cpuMilli"`
	MemoryMiB        int  `json:"memoryMiB"`
	EphemeralMiB     int  `json:"ephemeralMiB"`
	PersistentMiB    int  `json:"persistentMiB"`
	MaxRetentionDays int  `json:"maxRetentionDays"`
}

type monitoringAgentOptionalSignals struct {
	Logs   *monitoringAgentLogLane   `json:"logs,omitempty"`
	Traces *monitoringAgentTraceLane `json:"traces,omitempty"`
}

type monitoringAgentLogLane struct {
	Enabled       bool                          `json:"enabled"`
	Protocol      string                        `json:"protocol"`
	Direction     string                        `json:"direction"`
	Lifecycle     string                        `json:"lifecycle"`
	RetentionDays int                           `json:"retentionDays"`
	Budget        monitoringAgentOptionalBudget `json:"budget"`
}

type monitoringAgentTraceLane struct {
	Enabled   bool                          `json:"enabled"`
	Protocol  string                        `json:"protocol"`
	Direction string                        `json:"direction"`
	Lifecycle string                        `json:"lifecycle"`
	Sampling  monitoringAgentTraceSampling  `json:"sampling"`
	Budget    monitoringAgentOptionalBudget `json:"budget"`
}

type monitoringAgentTraceSampling struct {
	Mode  string  `json:"mode"`
	Ratio float64 `json:"ratio"`
}

func validateMonitoringAgentIntentUnit(unit RenderUnit, contract RendererContract) ([]byte, error) {
	return validateGenerationOnlyPolicyUnit(unit, generationOnlyPolicyUnitSpec{
		moduleID: monitoringAgentModuleID, unitID: monitoringAgentUnitID,
		outputRef: monitoringAgentOutputRef, policyName: "monitoring-agent collector intent",
		placementScope: "node-local", placementCardinality: "one-per-node",
		contract: contract, planInputRefs: monitoringAgentPlanInputRefs,
		validatePlanInput: validateMonitoringAgentPlanInputs,
	})
}

func validateMonitoringAgentPlanInputs(raw []byte, path string) ([]string, error) {
	var inputs monitoringAgentPlanInputs
	if err := decodeStrict(raw, &inputs); err != nil {
		return nil, wrap(ErrInvalidPlan, path, "decode exact monitoring-agent plan inputs", err)
	}
	if len(inputs.Sites) == 0 {
		return nil, fail(ErrInvalidPlan, path+".sites", "monitoring-agent requires at least one governed Site")
	}
	siteRefs := make([]string, 0, len(inputs.Sites))
	seenSites := map[string]struct{}{}
	for index, site := range inputs.Sites {
		sitePath := fmt.Sprintf("%s.sites[%d]", path, index)
		if site.Kind != "home" && site.Kind != "cloud" || strings.TrimSpace(site.FailureDomain) == "" {
			return nil, fail(ErrInvalidPlan, sitePath, "collector target Site must be a governed Home or Cloud failure domain")
		}
		if _, exists := seenSites[site.ID]; exists {
			return nil, fail(ErrInvalidPlan, sitePath+".id", "collector target Sites must be unique")
		}
		seenSites[site.ID] = struct{}{}
		siteRefs = append(siteRefs, site.ID)
	}
	if !monitoringAgentExactSortedUnique(siteRefs) {
		return nil, fail(ErrInvalidPlan, path+".sites", "collector target Sites must be unique and sorted")
	}
	if err := validateMonitoringAgentObservability(inputs.Observability, path+".observability"); err != nil {
		return nil, err
	}
	if err := rejectGenerationOnlyPolicyProjectionLeaks(raw, path, "monitoring-agent collector intent"); err != nil {
		return nil, err
	}
	return siteRefs, nil
}

func monitoringAgentExactSortedUnique(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] <= values[index-1] {
			return false
		}
	}
	return true
}

func validateMonitoringAgentObservability(intent monitoringAgentObservability, path string) error {
	agentBudgets := map[string]monitoringAgentBudget{
		"pi":          {CPUMilli: 100, MemoryMiB: 128, EphemeralMiB: 256},
		"single-node": {CPUMilli: 200, MemoryMiB: 256, EphemeralMiB: 512},
		"multi-node":  {CPUMilli: 250, MemoryMiB: 384, EphemeralMiB: 1024},
	}
	wantAgent, exists := agentBudgets[intent.Profile]
	if !exists || intent.AgentBudget != wantAgent {
		return fail(ErrInvalidPlan, path+".agentBudget", "collector budget must exactly match the governed profile")
	}
	if !intent.Signals.Metrics && !intent.Signals.Logs && !intent.Signals.Traces {
		return fail(ErrInvalidPlan, path+".signals", "collector intent must enable at least one signal")
	}
	if !intent.Collector.Enabled || intent.Collector.Endpoint == "" ||
		intent.Collector.Protocol != "grpc" && intent.Collector.Protocol != "http/protobuf" {
		return fail(ErrInvalidPlan, path+".collector", "collector must use one enabled governed OTLP transport")
	}
	if err := validateMonitoringAgentEndpoint(intent.Collector.Endpoint, path+".collector.endpoint"); err != nil {
		return err
	}
	if intent.OptionalSignals == nil {
		return nil
	}
	if intent.OptionalSignals.Logs == nil && intent.OptionalSignals.Traces == nil {
		return fail(ErrInvalidPlan, path+".optionalSignals", "optional signal projection has no represented lane")
	}
	if logs := intent.OptionalSignals.Logs; logs != nil {
		if !intent.Signals.Logs || !logs.Enabled || logs.Protocol != "otlp" || logs.Direction != "loki" || logs.Lifecycle != "external" {
			return fail(ErrInvalidPlan, path+".optionalSignals.logs", "log lane must be an enabled external Loki direction for an enabled log signal")
		}
		want := monitoringAgentOptionalBudgetFor(intent.Profile, false)
		if logs.Budget != want || logs.RetentionDays < 1 || logs.RetentionDays > want.MaxRetentionDays {
			return fail(ErrInvalidPlan, path+".optionalSignals.logs", "log budget and retention must exactly match the governed profile")
		}
	}
	if traces := intent.OptionalSignals.Traces; traces != nil {
		if !intent.Signals.Traces || !traces.Enabled || traces.Protocol != "otlp" || traces.Direction != "tempo" || traces.Lifecycle != "external" ||
			traces.Sampling.Mode != "parentbased-ratio" || traces.Sampling.Ratio != 0.1 {
			return fail(ErrInvalidPlan, path+".optionalSignals.traces", "trace lane must be the fixed external Tempo sampling direction for an enabled trace signal")
		}
		if traces.Budget != monitoringAgentOptionalBudgetFor(intent.Profile, true) {
			return fail(ErrInvalidPlan, path+".optionalSignals.traces.budget", "trace budget must exactly match the governed profile")
		}
	}
	return nil
}

func monitoringAgentOptionalBudgetFor(profile string, traces bool) monitoringAgentOptionalBudget {
	budgets := map[string]monitoringAgentOptionalBudget{
		"pi":          {Enabled: true, CPUMilli: 50, MemoryMiB: 64, EphemeralMiB: 128, PersistentMiB: 0, MaxRetentionDays: 7},
		"single-node": {Enabled: true, CPUMilli: 100, MemoryMiB: 128, EphemeralMiB: 256, PersistentMiB: 0, MaxRetentionDays: 14},
		"multi-node":  {Enabled: true, CPUMilli: 200, MemoryMiB: 256, EphemeralMiB: 512, PersistentMiB: 0, MaxRetentionDays: 30},
	}
	budget := budgets[profile]
	if traces {
		budget.MaxRetentionDays = 0
	}
	return budget
}

func validateMonitoringAgentEndpoint(endpoint, path string) error {
	if strings.TrimSpace(endpoint) != endpoint || strings.ContainsAny(endpoint, "\r\n\t @") ||
		strings.HasPrefix(strings.ToLower(endpoint), "secret:") {
		return fail(ErrInvalidPlan, path, "endpoint must not contain whitespace, userinfo, or a secret reference")
	}
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fail(ErrInvalidPlan, path, "URL endpoint must contain only scheme, host, optional port, and path")
		}
	}
	return nil
}
