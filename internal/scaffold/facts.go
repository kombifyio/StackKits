// Package scaffold renders module artifacts deterministically from a
// schema-validated module_facts.json (ADR-0027 Decision 1: "agents emit facts,
// a deterministic templater renders CUE"). Given the same facts and the same
// repo templates, Render always produces byte-identical output — that property
// is gate G0.
package scaffold

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// FactsSchemaVersion is the version this package renders. It is written into
// scaffolded artifacts and checked on load.
const FactsSchemaVersion = "1.0.0"

// Facts is the constrained input the promotion-drafter emits. It is the only
// LLM-authored surface; everything the templater renders is a pure function of
// these facts plus the repo templates.
type Facts struct {
	FactsSchemaVersion string          `json:"facts_schema_version"`
	Slug               string          `json:"slug"`
	DisplayName        string          `json:"displayName"`
	Description        string          `json:"description"`
	Rationale          string          `json:"rationale,omitempty"`
	Layer              string          `json:"layer"`
	Maturity           string          `json:"maturity,omitempty"`
	Core               *bool           `json:"core,omitempty"`
	ToolClass          string          `json:"tool_class,omitempty"`
	Requires           *FactsRequires  `json:"requires,omitempty"`
	Placement          *FactsPlacement `json:"placement,omitempty"`
	Services           []FactsService  `json:"services"`
}

// FactsRequires declares dependency + infrastructure needs.
type FactsRequires struct {
	Services       map[string]FactsRequiredService `json:"services,omitempty"`
	Infrastructure *FactsInfra                     `json:"infrastructure,omitempty"`
}

// FactsRequiredService is a dependency on another module.
type FactsRequiredService struct {
	MinVersion string   `json:"minVersion,omitempty"`
	Provides   []string `json:"provides,omitempty"`
	Optional   bool     `json:"optional,omitempty"`
}

// FactsInfra declares infrastructure requirements.
type FactsInfra struct {
	Docker            *bool  `json:"docker,omitempty"`
	Network           string `json:"network,omitempty"`
	DockerSocket      bool   `json:"dockerSocket,omitempty"`
	PersistentStorage bool   `json:"persistentStorage,omitempty"`
	MinMemory         string `json:"minMemory,omitempty"`
	Arch              string `json:"arch,omitempty"`
}

// FactsPlacement is the module placement eligibility. managed_serverless stays
// false unless the facts prove eligibility (and never with dockerSocket).
type FactsPlacement struct {
	LocalOnly         *bool `json:"localOnly,omitempty"`
	Standard          *bool `json:"standard,omitempty"`
	ManagedServerless bool  `json:"managedServerless,omitempty"`
}

// FactsService is one container the module deploys.
type FactsService struct {
	Name         string             `json:"name"`
	Type         string             `json:"type"`
	Image        string             `json:"image"`
	Tag          string             `json:"tag"`
	Required     *bool              `json:"required,omitempty"`
	Needs        []string           `json:"needs,omitempty"`
	Routed       bool               `json:"routed,omitempty"`
	Traefik      *FactsTraefik      `json:"traefik,omitempty"`
	Networks     []string           `json:"networks,omitempty"`
	AccessPolicy *FactsAccessPolicy `json:"accessPolicy,omitempty"`
	HealthCheck  *FactsHealthCheck  `json:"healthCheck,omitempty"`
	Resources    *FactsResources    `json:"resources,omitempty"`
	Security     *FactsSecurity     `json:"security,omitempty"`
	Volumes      []FactsVolume      `json:"volumes,omitempty"`
	Environment  map[string]string  `json:"environment,omitempty"`
	Upstream     *FactsUpstream     `json:"upstream,omitempty"`
	Subdomain    *FactsSubdomain    `json:"subdomain,omitempty"`
}

// FactsTraefik carries routing coordinates for a routed service.
type FactsTraefik struct {
	Rule string `json:"rule"`
	Port int    `json:"port"`
}

// FactsAccessPolicy is the auth posture for a routed service.
type FactsAccessPolicy struct {
	OuterAuth string `json:"outerAuth"`
	AppAuth   string `json:"appAuth,omitempty"`
}

// FactsHealthCheck is either an HTTP probe (path+port) or a shell command.
type FactsHealthCheck struct {
	Type     string `json:"type"` // "http" | "command"
	Path     string `json:"path,omitempty"`
	Port     int    `json:"port,omitempty"`
	Scheme   string `json:"scheme,omitempty"`
	Command  string `json:"command,omitempty"`
	Interval string `json:"interval,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
	Retries  int    `json:"retries,omitempty"`
}

// FactsResources are container limits.
type FactsResources struct {
	Memory    string  `json:"memory"`
	MemoryMax string  `json:"memoryMax,omitempty"`
	CPUs      float64 `json:"cpus,omitempty"`
}

// FactsSecurity is the hardening block; house defaults are enforced on render.
type FactsSecurity struct {
	NoNewPrivileges *bool    `json:"noNewPrivileges,omitempty"`
	CapDrop         []string `json:"capDrop,omitempty"`
	ReadOnly        bool     `json:"readOnly,omitempty"`
	Tmpfs           []string `json:"tmpfs,omitempty"`
}

// FactsVolume is a mount with a backup classification.
type FactsVolume struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Type        string `json:"type,omitempty"` // "volume" | "bind"
	ReadOnly    bool   `json:"readOnly,omitempty"`
	Backup      bool   `json:"backup,omitempty"`
	Description string `json:"description,omitempty"`
}

// FactsUpstream carries ADR-0028 watch coordinates.
type FactsUpstream struct {
	GitHub        string    `json:"github,omitempty"`
	RegistryImage string    `json:"registryImage,omitempty"`
	Track         string    `json:"track,omitempty"`
	PinLine       string    `json:"pinLine,omitempty"`
	OSV           *FactsOSV `json:"osv,omitempty"`
}

// FactsOSV is the secondary advisory source.
type FactsOSV struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

// FactsSubdomain is the domain-computation routing key.
type FactsSubdomain struct {
	Key    string `json:"key"`
	Nested string `json:"nested"`
	Flat   string `json:"flat"`
}

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]+$`)

// LoadFacts unmarshals and validates a module_facts.json document.
func LoadFacts(data []byte) (*Facts, error) {
	var f Facts
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse module_facts.json: %w", err)
	}
	if err := f.validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

func (f *Facts) validate() error {
	if f.FactsSchemaVersion != "" && f.FactsSchemaVersion != FactsSchemaVersion {
		return fmt.Errorf("unsupported facts_schema_version %q (want %q)", f.FactsSchemaVersion, FactsSchemaVersion)
	}
	if !slugRe.MatchString(f.Slug) {
		return fmt.Errorf("slug %q must match ^[a-z][a-z0-9-]+$", f.Slug)
	}
	if f.DisplayName == "" {
		return fmt.Errorf("displayName is required")
	}
	if f.Description == "" {
		return fmt.Errorf("description is required")
	}
	if f.Layer == "" {
		return fmt.Errorf("layer is required")
	}
	if len(f.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}
	seen := map[string]bool{}
	for i := range f.Services {
		s := &f.Services[i]
		if !slugRe.MatchString(s.Name) {
			return fmt.Errorf("service[%d].name %q must match ^[a-z][a-z0-9-]+$", i, s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("duplicate service name %q", s.Name)
		}
		seen[s.Name] = true
		if s.Image == "" {
			return fmt.Errorf("service %q: image is required", s.Name)
		}
		if s.Tag == "" {
			return fmt.Errorf("service %q: tag is required (no floating tags)", s.Name)
		}
		if s.HealthCheck == nil {
			return fmt.Errorf("service %q: healthCheck is required (it is the smoke assertion)", s.Name)
		}
		if s.Routed && s.Traefik == nil {
			return fmt.Errorf("service %q: routed service needs traefik {rule, port}", s.Name)
		}
	}
	return nil
}
