package commands

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

type accessSummary struct {
	SchemaVersion   string                 `json:"schemaVersion,omitempty"`
	StackID         string                 `json:"stackId,omitempty"`
	PlanHash        string                 `json:"planHash,omitempty"`
	ApplyResultHash string                 `json:"applyResultHash,omitempty"`
	StackKit        string                 `json:"stackkit"`
	StackKitVersion string                 `json:"stackkitVersion,omitempty"`
	Mode            string                 `json:"mode"`
	Domain          string                 `json:"domain"`
	SubdomainPrefix string                 `json:"subdomainPrefix,omitempty"`
	HubURL          string                 `json:"hubUrl"`
	Services        []accessService        `json:"services"`
	RuntimeServices []accessRuntimeService `json:"runtime_services,omitempty"`
	SetupActions    []string               `json:"setupActions,omitempty"`
	// ClientTrust carries the workspace step-ca root identity LAN devices need
	// to open the printed https links: the CA fingerprint to verify before
	// trusting it, the workspace-relative certificate path to copy, per-OS
	// enrollment guidance, and the resolver state. It is advisory manifest
	// content, never runtime enforcement evidence, and stays absent when no
	// local custody anchor exists.
	ClientTrust *accessClientTrust `json:"clientTrust,omitempty"`
	GeneratedAt time.Time          `json:"generatedAt"`
}

// accessClientTrust is the I2 client-trust handoff for one workspace.
type accessClientTrust struct {
	Authority       string   `json:"authority"`
	CAFingerprint   string   `json:"caFingerprint"`
	CAWorkspacePath string   `json:"caWorkspacePath"`
	EnrollmentSteps []string `json:"enrollmentSteps"`
	// Resolver stays pending until the lan-dns realization epic lands; a LAN
	// device reaches the node only after that step plus CA enrollment.
	Resolver string `json:"resolver"`
}

type accessRuntimeService struct {
	ServiceKey        string                `json:"service_key"`
	ApplicationKey    string                `json:"application_key"`
	DisplayName       string                `json:"display_name"`
	Role              string                `json:"role"`
	Lifecycle         string                `json:"lifecycle"`
	OperationalImpact string                `json:"operational_impact"`
	RuntimeIdentity   accessRuntimeIdentity `json:"runtime_identity"`
	InternalAddress   string                `json:"internal_address,omitempty"`
}

type accessRuntimeIdentity struct {
	Kind       string `json:"kind"`
	Adapter    string `json:"adapter"`
	RuntimeRef string `json:"runtime_ref"`
	Project    string `json:"project,omitempty"`
	File       string `json:"file,omitempty"`
	Deployment string `json:"deployment,omitempty"`
	Service    string `json:"service,omitempty"`
	Unit       string `json:"unit,omitempty"`
}

type accessService struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	DisplayName    string   `json:"displayName"`
	ToolName       string   `json:"toolName"`
	ModuleSlug     string   `json:"moduleSlug"`
	RouteSlug      string   `json:"routeSlug"`
	RouteRef       string   `json:"routeRef,omitempty"`
	Section        string   `json:"section,omitempty"`
	URL            string   `json:"url"`
	Host           string   `json:"host"`
	Status         string   `json:"status,omitempty"`
	LegacyAliases  []string `json:"legacyAliases,omitempty"`
	DesiredState   string   `json:"desiredState,omitempty"`
	AllowedActions []string `json:"allowedActions,omitempty"`
	EvidenceRef    string   `json:"evidenceRef,omitempty"`
}

func buildAccessSummary(wd string, spec *models.StackSpec) (*accessSummary, error) {
	if spec == nil {
		return nil, fmt.Errorf("stack spec is nil")
	}

	tfvars, err := loadGeneratedTFVars(wd)
	if err != nil {
		return nil, err
	}

	catalog := loadCanonicalServiceCatalog(wd, spec)
	return buildAccessSummaryFromInputs(spec, tfvars, catalog), nil
}

func loadGeneratedTFVars(wd string) (map[string]any, error) {
	path := filepath.Join(wd, config.GetDeployDir(), "terraform.tfvars.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read terraform.tfvars.json: %w", err)
	}

	var tfvars map[string]any
	if err := json.Unmarshal(data, &tfvars); err != nil {
		return nil, fmt.Errorf("parse terraform.tfvars.json: %w", err)
	}
	return tfvars, nil
}

func buildAccessSummaryFromInputs(spec *models.StackSpec, tfvars map[string]any, catalog []servicecatalog.Service) *accessSummary {
	domain := stringInput(tfvars, "domain", spec.Domain)
	if domain == "" {
		domain = models.DomainHomeLab
	}
	prefix := stringInput(tfvars, "subdomain_prefix", spec.SubdomainPrefix)

	proto := "http"
	if boolInput(tfvars, "enable_https", false) || models.IsKombifyMeDomain(domain) {
		proto = "https"
	}

	summary := &accessSummary{
		StackKit:        spec.StackKit,
		Mode:            spec.Mode,
		Domain:          domain,
		SubdomainPrefix: prefix,
		GeneratedAt:     time.Now().UTC(),
	}

	seen := map[string]bool{}
	for _, entry := range catalog {
		if entry.Key == "" || seen[entry.Key] || !entryEnabled(entry, tfvars) {
			continue
		}
		seen[entry.Key] = true

		host := hostForEntry(entry, domain, prefix)
		if host == "" {
			continue
		}
		display := entry.DisplayName
		if display == "" {
			display = entry.Key
		}
		name := entry.Name
		if name == "" {
			name = entry.Key
		}
		routeSlug := routeSlugForEntry(entry, prefix)

		svc := accessService{
			Key:           entry.Key,
			Name:          name,
			DisplayName:   display,
			ToolName:      entry.ToolName,
			ModuleSlug:    entry.ModuleSlug,
			RouteSlug:     routeSlug,
			Section:       entry.Section,
			Host:          host,
			URL:           accessURLForEntry(entry, proto, host, tfvars),
			Status:        string(models.ServiceStatusRunning),
			LegacyAliases: append([]string(nil), entry.LegacyAliases...),
		}
		summary.Services = append(summary.Services, svc)
		if entry.Key == "base" {
			summary.HubURL = svc.URL
		}
	}

	return summary
}

func accessURLForEntry(entry servicecatalog.Service, proto, host string, tfvars map[string]any) string {
	raw := proto + "://" + host
	if entry.Key == "files" && cloudreveSessionBridgeEnabled(tfvars) {
		return raw + "/stackkit/files/session"
	}
	return raw
}

func cloudreveSessionBridgeEnabled(tfvars map[string]any) bool {
	if !boolInput(tfvars, "enable_tinyauth", true) {
		return false
	}
	provider := strings.ToLower(stringInput(tfvars, "files_provider", "cloudreve"))
	cloudreveEnabled := boolInput(tfvars, "enable_cloudreve", true)
	nextcloudEnabled := boolInput(tfvars, "enable_nextcloud", false)
	if provider == "nextcloud" || (!cloudreveEnabled && nextcloudEnabled) {
		return false
	}
	return cloudreveEnabled
}

func entryEnabled(entry servicecatalog.Service, tfvars map[string]any) bool {
	enableVar := entry.EnableVar
	if enableVar == "" {
		enableVar = defaultEnableVar(entry.Key)
	}
	if enableVar == "" {
		return entry.Default
	}
	return boolInput(tfvars, enableVar, entry.Default)
}

func defaultEnableVar(key string) string {
	switch key {
	case "base", "dashboard":
		return "enable_dashboard"
	case "home", "homepage":
		return "enable_homepage"
	case "traefik":
		return "enable_traefik"
	case "auth":
		return "enable_tinyauth"
	case "id", "pocketid":
		return "enable_pocketid"
	case "dokploy":
		return "enable_dokploy"
	case "dockge":
		return "enable_dockge"
	case "coolify":
		return "enable_coolify"
	case "komodo":
		return "enable_komodo"
	case "kuma":
		return "enable_uptime_kuma"
	case "whoami":
		return "enable_whoami"
	case "vault":
		return "enable_vaultwarden"
	case "media":
		return "enable_jellyfin"
	case "photos":
		return "enable_immich"
	case "files":
		return "enable_files"
	default:
		return ""
	}
}

func hostForEntry(entry servicecatalog.Service, domain, prefix string) string {
	if prefix != "" {
		flat := entry.PublicSlug
		if flat == "" {
			flat = entry.Key
		}
		return prefix + "-" + flat + "." + domain
	}

	nested := entry.LocalSlug
	if nested == "" {
		nested = entry.Key
	}
	return nested + "." + domain
}

func routeSlugForEntry(entry servicecatalog.Service, prefix string) string {
	if prefix != "" {
		if entry.PublicSlug != "" {
			return entry.PublicSlug
		}
		return entry.Key
	}
	if entry.LocalSlug != "" {
		return entry.LocalSlug
	}
	return entry.Key
}

func writeAccessSummary(wd string, summary *accessSummary) error {
	if summary == nil {
		return nil
	}
	dir := filepath.Join(wd, ".stackkit")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create .stackkit directory: %w", err)
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal access summary: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(dir, "access.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write access summary: %w", err)
	}
	return nil
}

func attachObservedSetupActions(summary *accessSummary, state *models.DeploymentState) {
	if summary == nil {
		return
	}
	summary.SetupActions = observedSetupActionsFromState(state)
}

// attachAccessClientTrust fills the advisory client-trust handoff from the
// workspace step-ca root: fingerprint, certificate path, per-OS enrollment
// guidance, and the resolver state. It never fails the manifest: without a
// local custody anchor the section stays absent, which is the honest pending
// state, never a pass.
func attachAccessClientTrust(wd string, summary *accessSummary) {
	if summary == nil {
		return
	}
	raw, relPath, err := localevidence.BasementStepCARootCAPEM(wd)
	if err != nil || len(raw) == 0 {
		return
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}
	digest := sha256.Sum256(certificate.Raw)
	groups := make([]string, 0, len(digest))
	for _, b := range digest {
		groups = append(groups, strings.ToUpper(hex.EncodeToString([]byte{b})))
	}
	summary.ClientTrust = &accessClientTrust{
		Authority:       "step-ca basement root",
		CAFingerprint:   "SHA256:" + strings.Join(groups, ":"),
		CAWorkspacePath: relPath,
		EnrollmentSteps: []string{
			"Copy " + relPath + " to the device, then compare its SHA-256 fingerprint with the one above before trusting it.",
			"Windows: install the certificate into Trusted Root Certification Authorities for the current user.",
			"macOS: add it to the login keychain and set it to Always Trust.",
			"Linux: place it under /usr/local/share/ca-certificates and run update-ca-certificates (paths vary by distribution).",
			"iOS: install the profile, then enable full trust for it under Certificate Trust Settings.",
			"Android: install the CA certificate, then enable it for apps under trusted credentials.",
			"After enrollment open the printed https links; passkey registration requires that trusted secure context.",
		},
		Resolver: "pending:lan-dns-realization",
	}
}

func observedSetupActionsFromState(state *models.DeploymentState) []string {
	if state == nil {
		return nil
	}
	seen := map[string]bool{}
	actions := []string{}
	for _, run := range state.SetupRuns {
		drop := strings.TrimSpace(run.DropName)
		if drop == "" || seen[drop] {
			continue
		}
		if strings.TrimSpace(run.Status) != models.SetupRunStatusCompleted {
			continue
		}
		if strings.TrimSpace(run.Phase) != models.BootstrapPhaseVerified {
			continue
		}
		seen[drop] = true
		actions = append(actions, drop)
	}
	return actions
}

func urlAliases(summary *accessSummary) map[string]string {
	aliases := map[string]string{}
	if summary == nil {
		return aliases
	}
	for _, svc := range summary.Services {
		candidates := []string{svc.Key, svc.Name, svc.RouteSlug, svc.ToolName, svc.ModuleSlug, firstHostLabel(svc.Host)}
		candidates = append(candidates, svc.LegacyAliases...)
		for _, alias := range candidates {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias != "" {
				aliases[alias] = svc.URL
			}
		}
	}
	return aliases
}

func firstHostLabel(host string) string {
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		return host[:idx]
	}
	return host
}

func stringInput(values map[string]any, key, fallback string) string {
	if values == nil {
		return fallback
	}
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func boolInput(values map[string]any, key string, fallback bool) bool {
	if values == nil {
		return fallback
	}
	if value, ok := values[key].(bool); ok {
		return value
	}
	return fallback
}
