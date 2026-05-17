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
	Placement  PlacementExpectation  `json:"placement,omitempty"`
	Failure    FailureExpectation    `json:"failure,omitempty"`
}

type GenerationExpectation struct {
	Context             string          `json:"context,omitempty"`
	NetworkMode         string          `json:"networkMode,omitempty"`
	Domain              string          `json:"domain,omitempty"`
	PAAS                string          `json:"paas,omitempty"`
	ReverseProxyBackend string          `json:"reverseProxyBackend,omitempty"`
	EnableHTTPS         bool            `json:"enableHTTPS,omitempty"`
	StepCAEnabled       bool            `json:"stepCAEnabled,omitempty"`
	EnableKombifyPoint  bool            `json:"enableKombifyPoint,omitempty"`
	AdminEmail          string          `json:"adminEmail,omitempty"`
	ACMEChallenge       string          `json:"acmeChallenge,omitempty"`
	DNSProvider         string          `json:"dnsProvider,omitempty"`
	ServiceFlags        map[string]bool `json:"serviceFlags,omitempty"`
}

type AccessExpectation struct {
	HubURL         string            `json:"hubUrl,omitempty"`
	BrowserURLMode string            `json:"browserUrlMode,omitempty"`
	Services       []ExpectedService `json:"services,omitempty"`
}

type ExpectedService struct {
	Key    string `json:"key"`
	Host   string `json:"host"`
	Scheme string `json:"scheme"`
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
	ScenarioID   string            `json:"scenarioId"`
	ScenarioName string            `json:"scenarioName"`
	RunID        string            `json:"runId"`
	Status       string            `json:"status"`
	HubURL       string            `json:"hubUrl"`
	BrowserURL   string            `json:"browserUrl"`
	Services     []ObservedService `json:"services"`
	Target       Target            `json:"target"`
	LogsHint     string            `json:"logsHint,omitempty"`
	GeneratedAt  time.Time         `json:"generatedAt"`
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
	artifact := Artifact{
		ScenarioID:   s.ID,
		ScenarioName: s.Name,
		RunID:        runID,
		Status:       status,
		HubURL:       hubURL,
		BrowserURL:   browserURLForScenario(s, hubURL, target),
		Services:     append([]ObservedService(nil), access.Services...),
		Target:       target,
		GeneratedAt:  time.Now().UTC(),
	}
	if artifact.BrowserURL == "" {
		artifact.BrowserURL = hubURL
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
		if scenario.HasRunnableHomelab() && scenario.Expected.Access.HubURL == "" {
			return fmt.Errorf("%s runnable scenario has empty expected hubUrl", scenario.ID)
		}
	}
	return nil
}

func browserURLForScenario(s Scenario, hubURL string, target Target) string {
	return hubURL
}
