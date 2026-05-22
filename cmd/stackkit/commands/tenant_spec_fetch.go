// Package commands implements the stackkit CLI subcommands.
//
// tenant_spec_fetch.go covers the VM-side "pull my spec from
// Admin" path used by `stackkit apply --tenant-deployment <uuid>`.
//
// Flow:
//  1. CLI boots on a freshly-provisioned VM with no local
//     stack-spec.yaml, no modules/ tree.
//  2. Environment carries STACKKIT_ADMIN_ENDPOINT (e.g.
//     https://admin.kombify.io) and STACKKIT_BOOTSTRAP_TOKEN (the
//     plaintext issued once by POST /sk/tenants/deployments).
//  3. CLI calls GET /api/v1/sk/tenants/deployments/{id}/spec with
//     `Authorization: Bearer <bootstrap-token>`.
//  4. Response is decoded into StackSpec + bindings, written to
//     disk, and the normal apply pipeline takes over.
//
// ADR-2026-04 invariant still holds: Admin surfaces state; CLI
// performs actions. The Admin endpoint is read-only; no mutation.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
	"gopkg.in/yaml.v3"
)

// tenantSpecEnvelope mirrors the JSON shape returned by Admin's
// GET /api/v1/sk/tenants/deployments/{id}/spec. Kept loose (the
// spec is a pointer-rich StackSpec) so that server-side additions
// (new optional fields) don't break old CLIs.
type tenantSpecEnvelope struct {
	Deployment struct {
		ID              string `json:"id"`
		TenantID        string `json:"tenantId"`
		TenantSlug      string `json:"tenantSlug"`
		StackkitID      string `json:"stackkitId"`
		StackkitSlug    string `json:"stackkitSlug"`
		StackkitVersion string `json:"stackkitVersion"`
		LifecycleState  string `json:"lifecycleState"`
		DopplerProject  string `json:"dopplerProject"`
		DopplerConfig   string `json:"dopplerConfig"`
	} `json:"deployment"`
	Spec              models.StackSpec                    `json:"spec"`
	Bindings          []tenantSpecBindng                  `json:"bindings"`
	IdentityBootstrap *models.OwnerAdminBootstrapEnvelope `json:"identityBootstrap,omitempty"`
}

type tenantSpecBindng struct {
	ModuleSlug        string `json:"moduleSlug"`
	ModuleVersion     string `json:"moduleVersion"`
	SpecID            string `json:"specId"`
	SecretKey         string `json:"secretKey"`
	DopplerSecretPath string `json:"dopplerSecretPath"`
	ActualDBName      string `json:"actualDbName"`
	DBEngine          string `json:"dbEngine"`
	SchemaName        string `json:"schemaName"`
	SharedMode        string `json:"sharedMode"`
	Status            string `json:"status"`
}

// fetchTenantSpec pulls the composed StackSpec for the given
// deployment from Admin, using the bootstrap token from env. It
// writes the spec as YAML to `wd/<specFile>` (default
// stack-spec.yaml) so the existing loader flow picks it up.
//
// Returns the parsed StackSpec (also written to disk) so callers
// don't need to re-read it.
func fetchTenantSpec(ctx context.Context, deploymentID, wd string) (*models.StackSpec, error) {
	rolloutEvent("tenant_spec_fetch", "started", "fetching tenant deployment spec", map[string]string{
		"tenant_deployment_id": deploymentID,
	})
	recordTenantDeploymentEvent(deploymentID, "tenant_spec_fetch", "started", "fetching tenant deployment spec", "")
	endpoint := resolveAdminEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("--tenant-deployment set but no --admin-endpoint / STACKKIT_ADMIN_ENDPOINT configured")
	}
	token := resolveBootstrapToken()
	if token == "" {
		return nil, fmt.Errorf("--tenant-deployment set but no STACKKIT_BOOTSTRAP_TOKEN / --admin-token configured")
	}

	url := fmt.Sprintf("%s/api/v1/sk/tenants/deployments/%s/spec",
		strings.TrimRight(endpoint, "/"), deploymentID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build spec request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	// User-Agent doubles as a server-side audit marker (shows up on
	// the sk_tenant_deployment_event row written by the endpoint).
	req.Header.Set("User-Agent", "stackkit-cli tenant-spec-fetch")

	// Moderate timeout: on a fresh VM with slow DNS the first admin
	// call may be slow, but 30s is ample for a JSON endpoint.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("admin rejected bootstrap token (401) -- check STACKKIT_BOOTSTRAP_TOKEN and expiry")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("admin does not know deployment %s (404) -- wrong UUID or tenant scope", deploymentID)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("admin returned %d fetching spec", resp.StatusCode)
	}

	var env tenantSpecEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode spec envelope: %w", err)
	}
	if err := validateTenantSpecEnvelope(deploymentID, &env); err != nil {
		return nil, err
	}

	if env.Spec.Name == "" {
		// Defensive fallback for older Admin payloads. The validated
		// deployment id keeps the generated workspace traceable even if a
		// tenant slug is absent.
		nameScope := firstNonEmpty(env.Deployment.TenantSlug, env.Deployment.TenantID, deploymentID)
		env.Spec.Name = fmt.Sprintf("%s-%s", nameScope, env.Deployment.StackkitSlug)
	}
	if env.Spec.StackKit == "" {
		env.Spec.StackKit = env.Deployment.StackkitSlug
	}
	if err := validateManagedIdentityBootstrap(&env); err != nil {
		return nil, err
	}

	// Inject deployment-scoped env so downstream components (auth0
	// callbacks, Doppler path composition, telemetry tags) can key
	// off the deployment UUID without re-fetching.
	if env.Spec.Environment == nil {
		env.Spec.Environment = map[string]string{}
	}
	env.Spec.Environment["STACKKIT_TENANT_DEPLOYMENT_ID"] = env.Deployment.ID
	env.Spec.Environment["STACKKIT_TENANT_ID"] = env.Deployment.TenantID
	for _, b := range env.Bindings {
		// Surface binding->doppler-path mapping so cloud-init /
		// container-entrypoints know WHICH key to resolve for each
		// binding. Keys are STACKKIT_BINDING_<SECRETKEY>.
		if b.SecretKey == "" || b.DopplerSecretPath == "" {
			continue
		}
		env.Spec.Environment["STACKKIT_BINDING_"+b.SecretKey] = b.DopplerSecretPath
	}
	if err := persistPlatformConfigFromSpecEnvironment(&env.Spec, wd); err != nil {
		return nil, err
	}
	if err := persistIdentityBootstrapEnvelope(wd, env.IdentityBootstrap); err != nil {
		return nil, err
	}

	yamlBytes, err := yaml.Marshal(&env.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec to yaml: %w", err)
	}

	specPath := filepath.Join(wd, specFile)
	if err := os.MkdirAll(filepath.Dir(specPath), 0o750); err != nil {
		return nil, fmt.Errorf("mkdir for spec: %w", err)
	}
	if err := os.WriteFile(specPath, yamlBytes, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", specPath, err)
	}
	rolloutEvent("tenant_spec_fetch", "succeeded", "tenant deployment spec written", map[string]string{
		"tenant_deployment_id": env.Deployment.ID,
		"tenant_id":            env.Deployment.TenantID,
		"stackkit":             env.Spec.StackKit,
	})
	recordTenantDeploymentEvent(deploymentID, "tenant_spec_fetch", "succeeded", "tenant deployment spec written", "")

	// Also persist the bindings next to the spec so diagnostics can
	// see which doppler paths the VM was told to pull from.
	bindingsPath := filepath.Join(wd, ".stackkit", "tenant-bindings.json")
	if err := os.MkdirAll(filepath.Dir(bindingsPath), 0o750); err == nil {
		if data, jerr := json.MarshalIndent(env.Bindings, "", "  "); jerr == nil {
			_ = os.WriteFile(bindingsPath, data, 0o600)
		}
	}

	return &env.Spec, nil
}

func validateManagedIdentityBootstrap(env *tenantSpecEnvelope) error {
	if env == nil {
		return fmt.Errorf("admin returned empty tenant spec envelope")
	}
	if env.Spec.Owner.EffectiveBootstrapMode() != models.OwnerBootstrapModeAuto {
		return nil
	}
	if env.IdentityBootstrap == nil {
		return fmt.Errorf("managed owner bootstrap requires identityBootstrap envelope for owner.bootstrapMode=auto")
	}
	owner := env.IdentityBootstrap.Owner
	if strings.TrimSpace(owner.Email) == "" || strings.TrimSpace(owner.Username) == "" {
		return fmt.Errorf("identityBootstrap.owner requires email and username for managed owner bootstrap")
	}
	if env.IdentityBootstrap.RecoveryPassphraseHash == "" &&
		env.IdentityBootstrap.RecoveryPassphrasePlain == "" &&
		owner.RecoveryPassphraseHash == "" {
		return fmt.Errorf("identityBootstrap requires recoveryPassphraseHash or recoveryPassphrasePlain for managed owner bootstrap")
	}
	return nil
}

func persistIdentityBootstrapEnvelope(wd string, env *models.OwnerAdminBootstrapEnvelope) error {
	if env == nil {
		return nil
	}
	path := identityBootstrapEnvelopePath(wd)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir identity bootstrap dir: %w", err)
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal identity bootstrap envelope: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write identity bootstrap envelope: %w", err)
	}
	return nil
}

func validateTenantSpecEnvelope(deploymentID string, env *tenantSpecEnvelope) error {
	if env == nil {
		return fmt.Errorf("admin returned empty tenant spec envelope")
	}
	requestedID := strings.TrimSpace(deploymentID)
	returnedID := strings.TrimSpace(env.Deployment.ID)
	if requestedID == "" {
		return fmt.Errorf("tenant deployment id is required")
	}
	if returnedID == "" {
		return fmt.Errorf("admin returned spec without deployment id")
	}
	if returnedID != requestedID {
		return fmt.Errorf("admin returned spec for deployment %s, expected %s", returnedID, requestedID)
	}
	if strings.TrimSpace(env.Deployment.TenantID) == "" {
		return fmt.Errorf("admin returned spec without tenant id for deployment %s", requestedID)
	}
	if strings.TrimSpace(env.Deployment.StackkitSlug) == "" {
		return fmt.Errorf("admin returned spec without stackkit slug for deployment %s", requestedID)
	}
	return nil
}

// resolveAdminEndpoint mirrors reportTenantDeploymentState() so the
// "fetch spec" and "report state" paths agree on env var precedence.
func resolveAdminEndpoint() string {
	if applyReportingEndpoint != "" {
		return strings.TrimRight(applyReportingEndpoint, "/")
	}
	if v := os.Getenv("STACKKIT_ADMIN_ENDPOINT"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := os.Getenv("STACKKIT_ADMIN_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return ""
}

// resolveBootstrapToken returns the plaintext bootstrap token used to
// authenticate GET /spec. Order:
//  1. STACKKIT_BOOTSTRAP_TOKEN (preferred; name signals "narrow
//     audience, per-deployment")
//  2. --admin-token CLI flag
//  3. STACKKIT_ADMIN_TOKEN (fallback for admin-jobs container runs
//     where the operator-scoped token doubles as bootstrap)
func resolveBootstrapToken() string {
	return resolveTenantDeploymentToken()
}

// resolveTenantDeploymentToken returns the token used by the managed tenant
// deployment bridge. The same deployment-scoped token must work for both the
// spec fetch and lifecycle reporting so Admin can build one event trace.
func resolveTenantDeploymentToken() string {
	if v := os.Getenv("STACKKIT_BOOTSTRAP_TOKEN"); v != "" {
		return v
	}
	if applyReportingToken != "" {
		return applyReportingToken
	}
	if v := os.Getenv("STACKKIT_ADMIN_TOKEN"); v != "" {
		return v
	}
	return ""
}
