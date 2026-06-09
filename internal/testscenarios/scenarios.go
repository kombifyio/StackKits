// Package testscenarios loads the canonical StackKit rollout scenarios used by
// fast tests and production-readiness gates.
package testscenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
)

const scenariosRelDir = "tests/scenarios"

type Scenario struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Stage     string           `json:"stage"`
	LiveGate  string           `json:"liveGate"`
	StackSpec models.StackSpec `json:"stackSpec"`
	Env       EnvContract      `json:"env"`
	Expected  ExpectedContract `json:"expected"`
}

type EnvContract struct {
	RequiredKeys  []string `json:"requiredKeys,omitempty"`
	OptionalKeys  []string `json:"optionalKeys,omitempty"`
	ForbiddenKeys []string `json:"forbiddenKeys,omitempty"`
}

type ExpectedContract struct {
	Generation GenerationExpectation `json:"generation,omitempty"`
	Access     AccessExpectation     `json:"access,omitempty"`
	Target     TargetExpectation     `json:"target,omitempty"`
	Automation AutomationExpectation `json:"automation,omitempty"`
	Placement  PlacementExpectation  `json:"placement,omitempty"`
	Failure    FailureExpectation    `json:"failure,omitempty"`
	Profile    ProfileExpectation    `json:"profile,omitempty"`
	Simulation SimulationExpectation `json:"simulation,omitempty"`
}

type GenerationExpectation struct {
	Context             string            `json:"context,omitempty"`
	NetworkMode         string            `json:"networkMode,omitempty"`
	Domain              string            `json:"domain,omitempty"`
	PAAS                string            `json:"paas,omitempty"`
	InstallMode         string            `json:"installMode,omitempty"`
	BootstrapMode       string            `json:"bootstrapMode,omitempty"`
	DemoDataEnabled     bool              `json:"demoDataEnabled,omitempty"`
	ReverseProxyBackend string            `json:"reverseProxyBackend,omitempty"`
	EnableHTTPS         bool              `json:"enableHTTPS,omitempty"`
	StepCAEnabled       bool              `json:"stepCAEnabled,omitempty"`
	EnableKombifyPoint  bool              `json:"enableKombifyPoint,omitempty"`
	AdminEmail          string            `json:"adminEmail,omitempty"`
	ACMEChallenge       string            `json:"acmeChallenge,omitempty"`
	DNSProvider         string            `json:"dnsProvider,omitempty"`
	ServiceFlags        map[string]bool   `json:"serviceFlags,omitempty"`
	SetupPolicies       map[string]string `json:"setupPolicies,omitempty"`
}

type AccessExpectation struct {
	HubURL         string            `json:"hubUrl,omitempty"`
	BrowserURLMode string            `json:"browserUrlMode,omitempty"`
	Services       []ExpectedService `json:"services,omitempty"`
}

type TargetExpectation struct {
	Lane             string   `json:"lane,omitempty"`
	Provisioner      string   `json:"provisioner,omitempty"`
	Runtime          string   `json:"runtime,omitempty"`
	Provider         string   `json:"provider,omitempty"`
	AllowedProviders []string `json:"allowedProviders,omitempty"`
	HostSource       string   `json:"hostSource,omitempty"`
}

type AutomationExpectation struct {
	RolloutTrigger         string   `json:"rolloutTrigger,omitempty"`
	RuntimeActionMode      string   `json:"runtimeActionMode,omitempty"`
	ServiceCaller          string   `json:"serviceCaller,omitempty"`
	ServiceAudience        string   `json:"serviceAudience,omitempty"`
	LeaseAPIEndpoints      []string `json:"leaseApiEndpoints,omitempty"`
	RuntimeActionEndpoints []string `json:"runtimeActionEndpoints,omitempty"`
	ManagedCleanup         bool     `json:"managedCleanup,omitempty"`
}

type ExpectedService struct {
	Key    string `json:"key"`
	Host   string `json:"host"`
	Scheme string `json:"scheme"`
	Path   string `json:"path,omitempty"`
}

type PlacementExpectation struct {
	PublicNode string `json:"publicNode,omitempty"`
	LocalNode  string `json:"localNode,omitempty"`
	OwnerEmail string `json:"ownerEmail,omitempty"`
}

type FailureExpectation struct {
	NonInteractive  bool   `json:"nonInteractive,omitempty"`
	MessageContains string `json:"messageContains,omitempty"`
}

type ProfileExpectation struct {
	AdminProfileKey string `json:"adminProfileKey,omitempty"`
	Domain          string `json:"domain,omitempty"`
	MailMode        string `json:"mailMode,omitempty"`
	OwnerMode       string `json:"ownerMode,omitempty"`
	OwnerSource     string `json:"ownerSource,omitempty"`
	PAAS            string `json:"paas,omitempty"`
	BootstrapMode   string `json:"bootstrapMode,omitempty"`
	DemoDataEnabled bool   `json:"demoDataEnabled,omitempty"`
}

type SimulationExpectation struct {
	SetupActions  []string `json:"setupActions,omitempty"`
	SeededContent []string `json:"seededContent,omitempty"`
	HealthChecks  []string `json:"healthChecks,omitempty"`
}

type ObservedAccess struct {
	Domain          string            `json:"domain,omitempty"`
	SubdomainPrefix string            `json:"subdomainPrefix,omitempty"`
	HubURL          string            `json:"hubUrl"`
	Services        []ObservedService `json:"services"`
}

type ObservedService struct {
	Key        string   `json:"key"`
	Name       string   `json:"name,omitempty"`
	ToolName   string   `json:"toolName,omitempty"`
	ModuleSlug string   `json:"moduleSlug,omitempty"`
	RouteSlug  string   `json:"routeSlug,omitempty"`
	URL        string   `json:"url"`
	Host       string   `json:"host"`
	Aliases    []string `json:"legacyAliases,omitempty"`
}

type Target struct {
	Host          string `json:"host,omitempty"`
	PublicIP      string `json:"publicIp,omitempty"`
	SSHPort       int    `json:"sshPort,omitempty"`
	HTTPPort      int    `json:"httpPort,omitempty"`
	HTTPSPort     int    `json:"httpsPort,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	VolumeName    string `json:"volumeName,omitempty"`
	Image         string `json:"image,omitempty"`
}

type Artifact struct {
	ScenarioID   string                `json:"scenarioId"`
	ScenarioName string                `json:"scenarioName"`
	RunID        string                `json:"runId"`
	Status       string                `json:"status"`
	HubURL       string                `json:"hubUrl"`
	BrowserURL   string                `json:"browserUrl"`
	Profile      ProfileExpectation    `json:"profile,omitempty"`
	Simulation   SimulationExpectation `json:"simulation,omitempty"`
	Services     []ObservedService     `json:"services"`
	Target       Target                `json:"target"`
	LogsHint     string                `json:"logsHint,omitempty"`
	GeneratedAt  time.Time             `json:"generatedAt"`
}

func LoadAll() ([]Scenario, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	dir, err := FindScenarioDir(wd)
	if err != nil {
		return nil, err
	}
	return LoadAllFromDir(dir)
}

func ByID(id string) (Scenario, error) {
	scenarios, err := LoadAll()
	if err != nil {
		return Scenario{}, err
	}
	for _, scenario := range scenarios {
		if strings.EqualFold(scenario.ID, id) {
			return scenario, nil
		}
	}
	return Scenario{}, fmt.Errorf("scenario %q not found", id)
}

func LoadAllFromDir(dir string) ([]Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read scenario directory %s: %w", dir, err)
	}
	var scenarios []Scenario
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		scenario, err := loadOne(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, scenario)
	}
	sort.Slice(scenarios, func(i, j int) bool {
		return scenarios[i].ID < scenarios[j].ID
	})
	if err := validateScenarios(scenarios); err != nil {
		return nil, err
	}
	return scenarios, nil
}

func FindScenarioDir(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", start, err)
	}
	for cur := filepath.Clean(abs); ; cur = filepath.Dir(cur) {
		candidate := filepath.Join(cur, filepath.FromSlash(scenariosRelDir))
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
	}
	return "", fmt.Errorf("could not find %s from %s", scenariosRelDir, start)
}

func (s Scenario) HasRunnableHomelab() bool {
	return s.Stage == "live" || s.Stage == "gated-live"
}

func NewArtifact(s Scenario, runID, status string, access ObservedAccess, target Target) Artifact {
	hubURL := strings.TrimSpace(access.HubURL)
	if hubURL == "" {
		hubURL = s.Expected.Access.HubURL
	}
	browserURL := browserURLForScenario(s, hubURL, target)
	if browserURL == "" {
		browserURL = firstObservedAccessURL(access.Services)
	}
	if browserURL == "" {
		browserURL = firstExpectedAccessURL(s.Expected.Access.Services)
	}
	if browserURL == "" {
		browserURL = hubURL
	}
	artifact := Artifact{
		ScenarioID:   s.ID,
		ScenarioName: s.Name,
		RunID:        runID,
		Status:       status,
		HubURL:       hubURL,
		BrowserURL:   browserURL,
		Profile:      s.Expected.Profile,
		Simulation:   s.Expected.Simulation,
		Services:     append([]ObservedService(nil), access.Services...),
		Target:       target,
		GeneratedAt:  time.Now().UTC(),
	}
	return artifact
}

func WriteArtifact(path string, artifact Artifact) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal scenario artifact: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write scenario artifact: %w", err)
	}
	return nil
}

func loadOne(path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("read scenario %s: %w", path, err)
	}
	var scenario Scenario
	if err := json.Unmarshal(data, &scenario); err != nil {
		return Scenario{}, fmt.Errorf("parse scenario %s: %w", path, err)
	}
	return scenario, nil
}

func validateScenarios(scenarios []Scenario) error {
	seen := map[string]bool{}
	for _, scenario := range scenarios {
		if scenario.ID == "" {
			return fmt.Errorf("scenario has empty id")
		}
		if seen[scenario.ID] {
			return fmt.Errorf("duplicate scenario id %q", scenario.ID)
		}
		seen[scenario.ID] = true
		if scenario.Name == "" {
			return fmt.Errorf("%s has empty name", scenario.ID)
		}
		if scenario.StackSpec.StackKit == "" {
			return fmt.Errorf("%s has empty stackkit", scenario.ID)
		}
		if scenario.HasRunnableHomelab() {
			if len(scenario.Expected.Access.Services) == 0 {
				return fmt.Errorf("%s runnable scenario has empty expected services", scenario.ID)
			}
			if scenario.Expected.Access.HubURL == "" && scenario.Expected.Generation.InstallMode != string(models.InstallModeBare) {
				return fmt.Errorf("%s runnable non-bare scenario has empty expected hubUrl", scenario.ID)
			}
		}
	}
	return nil
}

func browserURLForScenario(s Scenario, hubURL string, target Target) string {
	return hubURL
}

func firstObservedAccessURL(services []ObservedService) string {
	for _, svc := range services {
		if strings.TrimSpace(svc.URL) != "" {
			return strings.TrimSpace(svc.URL)
		}
	}
	return ""
}

func firstExpectedAccessURL(services []ExpectedService) string {
	for _, svc := range services {
		if strings.TrimSpace(svc.Host) == "" || strings.TrimSpace(svc.Scheme) == "" {
			continue
		}
		return svc.Scheme + "://" + svc.Host + svc.Path
	}
	return ""
}
