package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"cuelang.org/go/cue/format"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// Render turns validated facts into the module's artifacts, keyed by path
// relative to the module directory. Output is deterministic: same facts →
// byte-identical files (gate G0).
func Render(f *Facts) (map[string]string, error) {
	rm := buildRenderModel(f)
	out := map[string]string{}
	for name, tmplFile := range map[string]string{
		"module.cue":                  "module.cue.tmpl",
		"tests/reference-compose.yml": "reference-compose.yml.tmpl",
		"tests/integration_test.sh":   "integration_test.sh.tmpl",
	} {
		rendered, err := renderTemplate(tmplFile, rm)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", name, err)
		}
		if strings.HasSuffix(name, ".cue") {
			formatted, ferr := format.Source([]byte(rendered), format.Simplify())
			if ferr != nil {
				return nil, fmt.Errorf("format %s (template produced invalid CUE): %w", name, ferr)
			}
			rendered = string(formatted)
		}
		out[name] = rendered
	}
	return out, nil
}

func renderTemplate(file string, rm *renderModel) (string, error) {
	t, err := template.New(file).Funcs(template.FuncMap{
		"quote": func(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\"" },
	}).ParseFS(templatesFS, "templates/"+file)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, rm); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// --- deterministic render model ---

type renderModel struct {
	Package     string
	Slug        string
	DisplayName string
	Description string
	Version     string
	Layer       string
	Core        bool
	Maturity    string
	Requires    *reqModel
	Placement   placementModel
	Services    []svcModel
	// Test harness coordinates (derived).
	PrimaryContainer   string
	HealthHost         string
	HealthPath         string
	RouterName         string
	RoutedBaseURL      string
	TraefikAPI         string
	HealthyContainers  string
	SecurityContainers string
	ReadonlyContainers string
	Isolated           string
	TestNet            string
	TestDBNet          string
	HasDBNet           bool
	NamedVolumes       []string
}

type reqModel struct {
	Services       []kvReqService
	Infrastructure infraModel
	HasServices    bool
	HasInfra       bool
}

type kvReqService struct {
	Name       string
	MinVersion string
	Provides   []string
	Optional   bool
}

type infraModel struct {
	Docker            bool
	Network           string
	DockerSocket      bool
	PersistentStorage bool
	MinMemory         string
	Arch              string
}

type placementModel struct {
	LocalOnly         bool
	Standard          bool
	ManagedServerless bool
}

type svcModel struct {
	Name          string
	Type          string
	Image         string
	Tag           string
	Required      bool
	Needs         []string
	Routed        bool
	TraefikRule   string
	TraefikPort   int
	Networks      []string
	OuterAuth     string
	AppAuth       string
	HasAccess     bool
	HC            hcModel
	Resources     *FactsResources
	Security      secModel
	Volumes       []FactsVolume
	Environment   []kv
	Upstream      *upstreamModel
	Subdomain     *FactsSubdomain
	ContainerName string
	IsDB          bool
	OutputDesc    string
	// Compose-derived (test reference-compose.yml)
	ComposeNetworks []string
	MemLimit        string
	CPUs            float64
	HealthTest      string // full compose healthcheck `test:` array literal
	StartPeriod     string
	TraefikLabels   []string
}

type hcModel struct {
	HTTP     bool
	Path     string
	Port     int
	Scheme   string
	Command  string
	Interval string
	Timeout  string
	Retries  int
}

type secModel struct {
	NoNewPrivileges bool
	CapDrop         []string
	ReadOnly        bool
	Tmpfs           []string
}

type upstreamModel struct {
	GitHub        string
	RegistryImage string
	Track         string
	PinLine       string
	OSVEcosystem  string
	OSVName       string
	HasGitHub     bool
	HasOSV        bool
}

type kv struct {
	Key   string
	Value string
}

func pkgName(slug string) string {
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func deref(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// isDBType reports whether a service type is an internal data sidecar that
// should sit on the isolated db network and never be routed.
func isDBType(t string) bool {
	switch t {
	case "database", "cache", "storage", "block-storage", "distributed-storage":
		return true
	}
	return false
}

func buildRenderModel(f *Facts) *renderModel {
	rm := &renderModel{
		Package:     pkgName(f.Slug),
		Slug:        f.Slug,
		DisplayName: f.DisplayName,
		Description: f.Description,
		Version:     "0.1.0",
		Layer:       f.Layer,
		Core:        deref(f.Core, false),
		Maturity:    "draft", // scaffold output is always draft (ADR-0027 §5)
		TestNet:     f.Slug + "-test-net",
		TestDBNet:   f.Slug + "-test-db-net",
	}

	rm.Requires = buildRequires(f)
	rm.Placement = buildPlacement(f)

	// services
	var healthy, security, readonly, isolated []string
	for i := range f.Services {
		s := &f.Services[i]
		sm := buildServiceModel(f, s)
		rm.Services = append(rm.Services, sm)

		// harness coordinates
		healthy = append(healthy, sm.ContainerName)
		security = append(security, sm.ContainerName)
		if sm.Security.ReadOnly {
			readonly = append(readonly, sm.ContainerName)
		}
		if sm.IsDB {
			rm.HasDBNet = true
			isolated = append(isolated, fmt.Sprintf("%s:%s:%s", sm.ContainerName, rm.TestDBNet, rm.TestNet))
		}
		if s.Routed {
			rm.PrimaryContainer = sm.ContainerName
			rm.RouterName = s.Name
			rm.HealthPath = sm.HC.Path
			// derive host from the traefik rule (Host(`x`)) when present
			rm.HealthHost = hostFromRule(s.Traefik)
		}
	}

	if rm.PrimaryContainer == "" && len(rm.Services) > 0 {
		rm.PrimaryContainer = rm.Services[0].ContainerName
		rm.RouterName = rm.Services[0].Name
	}
	if rm.HealthHost == "" {
		rm.HealthHost = rm.Slug + ".test.local"
	}
	if rm.HealthPath == "" {
		rm.HealthPath = "/"
	}
	rm.HealthyContainers = strings.Join(healthy, " ")
	rm.SecurityContainers = strings.Join(security, " ")
	rm.ReadonlyContainers = strings.Join(readonly, " ")
	rm.Isolated = strings.Join(isolated, " ")
	rm.RoutedBaseURL = "http://localhost:8899"
	rm.TraefikAPI = "http://localhost:19099"

	deriveComposeFields(rm)
	return rm
}

// buildRequires renders the requires block deterministically.
func buildRequires(f *Facts) *reqModel {
	if f.Requires == nil {
		return nil
	}
	req := &reqModel{}
	names := make([]string, 0, len(f.Requires.Services))
	for n := range f.Requires.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := f.Requires.Services[n]
		req.Services = append(req.Services, kvReqService{Name: n, MinVersion: s.MinVersion, Provides: s.Provides, Optional: s.Optional})
	}
	req.HasServices = len(req.Services) > 0
	if f.Requires.Infrastructure != nil {
		in := f.Requires.Infrastructure
		req.Infrastructure = infraModel{
			Docker:            deref(in.Docker, true),
			Network:           in.Network,
			DockerSocket:      in.DockerSocket,
			PersistentStorage: in.PersistentStorage,
			MinMemory:         in.MinMemory,
			Arch:              in.Arch,
		}
		req.HasInfra = true
	}
	return req
}

// buildPlacement resolves eligibility; managed-serverless is forced off when a
// module needs the Docker socket.
func buildPlacement(f *Facts) placementModel {
	dockerSocket := f.Requires != nil && f.Requires.Infrastructure != nil && f.Requires.Infrastructure.DockerSocket
	pm := placementModel{LocalOnly: true, Standard: true}
	if f.Placement != nil {
		pm.LocalOnly = deref(f.Placement.LocalOnly, true)
		pm.Standard = deref(f.Placement.Standard, true)
		pm.ManagedServerless = f.Placement.ManagedServerless
	}
	if dockerSocket {
		pm.ManagedServerless = false
	}
	return pm
}

// buildServiceModel renders one service (house security defaults applied).
func buildServiceModel(f *Facts, s *FactsService) svcModel {
	sm := svcModel{
		Name:          s.Name,
		Type:          s.Type,
		Image:         s.Image,
		Tag:           s.Tag,
		Required:      deref(s.Required, true),
		Needs:         s.Needs,
		Routed:        s.Routed,
		ContainerName: "test-" + s.Name,
		IsDB:          isDBType(s.Type),
		Subdomain:     s.Subdomain,
		Resources:     s.Resources,
		HC:            buildHealthCheck(s.HealthCheck),
		Security:      buildSecurity(s.Security),
		Environment:   sortedEnv(s.Environment),
	}
	for _, v := range s.Volumes {
		if v.Type == "" {
			v.Type = "volume"
		}
		sm.Volumes = append(sm.Volumes, v)
	}
	if s.Routed {
		sm.OutputDesc = f.DisplayName + " dashboard"
	} else {
		sm.OutputDesc = f.DisplayName + " " + s.Type + " (internal)"
	}
	if s.Routed && s.Traefik != nil {
		sm.TraefikRule = s.Traefik.Rule
		sm.TraefikPort = s.Traefik.Port
	}
	switch {
	case len(s.Networks) > 0:
		sm.Networks = s.Networks
	case sm.IsDB:
		sm.Networks = []string{"base_net_db"}
	default:
		sm.Networks = []string{"base_net"}
	}
	if s.Routed {
		outer, app := "tinyauth-pocketid", "self-auth"
		if s.AccessPolicy != nil {
			outer = firstNonEmpty(s.AccessPolicy.OuterAuth, outer)
			app = firstNonEmpty(s.AccessPolicy.AppAuth, app)
		}
		sm.OuterAuth, sm.AppAuth, sm.HasAccess = outer, app, true
	}
	if s.Upstream != nil {
		sm.Upstream = buildUpstream(s)
	}
	return sm
}

func buildHealthCheck(hc *FactsHealthCheck) hcModel {
	m := hcModel{
		Interval: firstNonEmpty(hc.Interval, "30s"),
		Timeout:  firstNonEmpty(hc.Timeout, "10s"),
		Retries:  firstPositive(hc.Retries, 3),
	}
	if hc.Type == "command" || (hc.Type == "" && hc.Command != "") {
		m.Command = hc.Command
	} else {
		m.HTTP = true
		m.Path = firstNonEmpty(hc.Path, "/")
		m.Port = hc.Port
		m.Scheme = firstNonEmpty(hc.Scheme, "http")
	}
	return m
}

func buildSecurity(sec *FactsSecurity) secModel {
	out := secModel{NoNewPrivileges: true, CapDrop: []string{"ALL"}}
	if sec != nil {
		out.ReadOnly = sec.ReadOnly
		out.Tmpfs = sec.Tmpfs
		if len(sec.CapDrop) > 0 && !containsFold(sec.CapDrop, "ALL") {
			out.CapDrop = append([]string{"ALL"}, sec.CapDrop...)
		}
	}
	return out
}

func sortedEnv(env map[string]string) []kv {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		out = append(out, kv{Key: k, Value: env[k]})
	}
	return out
}

func buildUpstream(s *FactsService) *upstreamModel {
	um := &upstreamModel{
		GitHub:        s.Upstream.GitHub,
		RegistryImage: firstNonEmpty(s.Upstream.RegistryImage, s.Image),
		Track:         firstNonEmpty(s.Upstream.Track, "minor"),
		PinLine:       s.Upstream.PinLine,
	}
	um.HasGitHub = um.GitHub != ""
	if s.Upstream.OSV != nil {
		um.OSVEcosystem = s.Upstream.OSV.Ecosystem
		um.OSVName = s.Upstream.OSV.Name
		um.HasOSV = um.OSVEcosystem != ""
	}
	return um
}

// deriveComposeFields fills the reference-compose-only fields on each service
// (needs HealthHost + HasDBNet, so it runs after the first pass).
func deriveComposeFields(rm *renderModel) {
	for i := range rm.Services {
		s := &rm.Services[i]
		for _, n := range s.Networks {
			if n == "base_net_db" {
				s.ComposeNetworks = appendUnique(s.ComposeNetworks, "test-db-net")
			} else {
				s.ComposeNetworks = appendUnique(s.ComposeNetworks, "test-net")
			}
		}
		if len(s.ComposeNetworks) == 0 {
			s.ComposeNetworks = []string{"test-net"}
		}
		s.MemLimit, s.CPUs = "512m", 0.5
		if s.Resources != nil {
			if m := firstNonEmpty(s.Resources.MemoryMax, s.Resources.Memory); m != "" {
				s.MemLimit = m
			}
			if s.Resources.CPUs > 0 {
				s.CPUs = s.Resources.CPUs
			}
		}
		s.StartPeriod = "30s"
		if s.HC.HTTP {
			s.HealthTest = fmt.Sprintf(`["CMD", "curl", "-f", "http://127.0.0.1:%d%s"]`, s.HC.Port, s.HC.Path)
		} else {
			s.HealthTest = fmt.Sprintf(`["CMD-SHELL", %q]`, s.HC.Command)
		}
		if s.Routed {
			s.TraefikLabels = []string{
				"traefik.enable=true",
				fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", s.Name, rm.HealthHost),
				fmt.Sprintf("traefik.http.routers.%s.entrypoints=web", s.Name),
				fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port=%d", s.Name, s.TraefikPort),
			}
			if rm.HasDBNet {
				s.TraefikLabels = append(s.TraefikLabels, fmt.Sprintf("traefik.docker.network=%s-test-net", rm.Slug))
			}
		}
		for _, v := range s.Volumes {
			if v.Type == "volume" {
				rm.NamedVolumes = appendUnique(rm.NamedVolumes, v.Source)
			}
		}
	}
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstPositive(a, b int) int {
	if a > 0 {
		return a
	}
	return b
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// hostFromRule extracts the host from a Traefik Host(`x`) rule so the smoke
// manifest and the compose labels agree. Falls back to empty.
func hostFromRule(t *FactsTraefik) string {
	if t == nil {
		return ""
	}
	r := t.Rule
	i := strings.Index(r, "`")
	if i < 0 {
		return ""
	}
	j := strings.Index(r[i+1:], "`")
	if j < 0 {
		return ""
	}
	host := r[i+1 : i+1+j]
	// tests use a .test.local host, not the templated production domain
	if strings.Contains(host, "{{") {
		return ""
	}
	return host
}
