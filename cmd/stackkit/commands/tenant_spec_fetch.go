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
//  4. Response is classified without downconversion, published with a
//     digest-bound sidecar bundle, and the normal apply pipeline takes over.
//
// ADR-2026-04 invariant still holds: Admin surfaces state; CLI
// performs actions. The Admin endpoint is read-only; no mutation.
package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/pkg/models"
	"gopkg.in/yaml.v3"
)

// tenantSpecEnvelope mirrors the JSON shape returned by Admin's
// GET /api/v1/sk/tenants/deployments/{id}/spec. Spec stays raw so optional v2
// fields cannot be lost through the partial legacy Go model.
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
	Spec              json.RawMessage                     `json:"spec"`
	Bindings          []tenantSpecBindng                  `json:"bindings"`
	Telemetry         tenantSpecTelemetry                 `json:"telemetry,omitempty"`
	IdentityBootstrap *models.OwnerAdminBootstrapEnvelope `json:"identityBootstrap,omitempty"`
}

// tenantSpecFetchResult exposes the classified source without making a
// canonical v2 document pass through models.StackSpec. Legacy is populated
// only for the explicit v0.6 compatibility path.
type tenantSpecFetchResult struct {
	Version    stackspecmigration.SourceVersion
	KitProfile stackspecmigration.KitProfile
	Legacy     *models.StackSpec
	Platform   *platformConfigFile
}

type tenantSpecFetchCandidate struct {
	result        tenantSpecFetchResult
	canonicalSpec []byte
	envelope      tenantSpecEnvelope
}

type tenantSpecTelemetry struct {
	SentryDSN    string            `json:"sentryDsn,omitempty"`
	OTLPEndpoint string            `json:"otlpEndpoint,omitempty"`
	OTLPHeaders  map[string]string `json:"otlpHeaders,omitempty"`
	Environment  string            `json:"environment,omitempty"`
	Release      string            `json:"release,omitempty"`
	RolloutMode  string            `json:"rolloutMode,omitempty"`
}

type tenantTelemetryMetadata struct {
	SentryDSNConfigured bool     `json:"sentryDsnConfigured,omitempty"`
	OTLPEndpointSet     bool     `json:"otlpEndpointConfigured,omitempty"`
	OTLPHeaderKeys      []string `json:"otlpHeaderKeys,omitempty"`
	Environment         string   `json:"environment,omitempty"`
	Release             string   `json:"release,omitempty"`
	RolloutMode         string   `json:"rolloutMode,omitempty"`
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
// writes v0.6 compatibility as YAML and canonical v2 as stable JSON to
// `wd/<specFile>` (default stack-spec.yaml) so versioned admission picks it up.
//
// Returns only classified dispatch metadata. A v2 document is never decoded
// through the partial legacy Go model.
func fetchTenantSpec(ctx context.Context, deploymentID, wd string) (tenantSpecFetchResult, error) {
	rolloutEvent("tenant_spec_fetch", "started", "fetching tenant deployment spec", map[string]string{
		"tenant_deployment_id": deploymentID,
	})
	recordTenantDeploymentEvent(deploymentID, "tenant_spec_fetch", "started", "fetching tenant deployment spec", "")
	candidate, err := fetchTenantSpecCandidate(ctx, deploymentID)
	if err != nil {
		return tenantSpecFetchResult{}, err
	}
	return persistTenantSpecCandidate(wd, deploymentID, candidate)
}

// fetchTenantSpecCandidate performs the read-only Admin request and complete
// version/identity admission without publishing any local StackSpec or sidecar.
func fetchTenantSpecCandidate(ctx context.Context, deploymentID string) (tenantSpecFetchCandidate, error) {
	endpoint := resolveAdminEndpoint()
	if endpoint == "" {
		return tenantSpecFetchCandidate{}, fmt.Errorf("--tenant-deployment set but no --admin-endpoint / STACKKIT_ADMIN_ENDPOINT configured")
	}
	token := resolveBootstrapToken()
	if token == "" {
		return tenantSpecFetchCandidate{}, fmt.Errorf("--tenant-deployment set but no STACKKIT_BOOTSTRAP_TOKEN / --admin-token configured")
	}

	url := fmt.Sprintf("%s/api/v1/sk/tenants/deployments/%s/spec",
		strings.TrimRight(endpoint, "/"), deploymentID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return tenantSpecFetchCandidate{}, fmt.Errorf("build spec request: %w", err)
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
		return tenantSpecFetchCandidate{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return tenantSpecFetchCandidate{}, fmt.Errorf("admin rejected bootstrap token (401) -- check STACKKIT_BOOTSTRAP_TOKEN and expiry")
	}
	if resp.StatusCode == http.StatusNotFound {
		return tenantSpecFetchCandidate{}, fmt.Errorf("admin does not know deployment %s (404) -- wrong UUID or tenant scope", deploymentID)
	}
	if resp.StatusCode >= 400 {
		return tenantSpecFetchCandidate{}, fmt.Errorf("admin returned %d fetching spec", resp.StatusCode)
	}

	var env tenantSpecEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return tenantSpecFetchCandidate{}, fmt.Errorf("decode spec envelope: %w", err)
	}
	if err := validateTenantSpecEnvelope(deploymentID, &env); err != nil {
		return tenantSpecFetchCandidate{}, err
	}

	result, canonicalSpec, err := admitTenantStackSpec(&env, deploymentID)
	if err != nil {
		return tenantSpecFetchCandidate{}, err
	}
	return tenantSpecFetchCandidate{result: result, canonicalSpec: canonicalSpec, envelope: env}, nil
}

func persistTenantSpecCandidate(wd, deploymentID string, candidate tenantSpecFetchCandidate) (tenantSpecFetchResult, error) {
	if err := persistTenantFetchBundle(wd, specFile, deploymentID, candidate.canonicalSpec, &candidate.envelope, candidate.result); err != nil {
		return tenantSpecFetchResult{}, err
	}
	if err := verifyTenantFetchBundle(wd, specFile, deploymentID, candidate.result); err != nil {
		return tenantSpecFetchResult{}, err
	}
	rolloutEvent("tenant_spec_fetch", "succeeded", "tenant deployment spec written", map[string]string{
		"tenant_deployment_id": candidate.envelope.Deployment.ID,
		"tenant_id":            candidate.envelope.Deployment.TenantID,
		"stackkit":             string(candidate.result.KitProfile),
	})
	recordTenantDeploymentEvent(deploymentID, "tenant_spec_fetch", "succeeded", "tenant deployment spec written", "")

	return candidate.result, nil
}

func admitTenantStackSpec(env *tenantSpecEnvelope, deploymentID string) (tenantSpecFetchResult, []byte, error) {
	if env == nil {
		return tenantSpecFetchResult{}, nil, fmt.Errorf("admin returned empty tenant spec envelope")
	}
	document, err := stackspecmigration.Read(env.Spec)
	if err != nil {
		return tenantSpecFetchResult{}, nil, fmt.Errorf("classify tenant StackSpec: %w", err)
	}

	switch document.Version {
	case stackspecmigration.SourceVersionV1:
		if architectureV2RejectsV1Execution(version) {
			return tenantSpecFetchResult{}, nil, newArchitectureV2ExecutionGate().rejectV1Execution(document.Raw, architectureV2Apply)
		}
		if len(document.UnknownV1Fields) > 0 {
			return tenantSpecFetchResult{}, nil, fmt.Errorf(
				"tenant StackSpec v1 contains unknown fields (%s); refusing a lossy managed write",
				strings.Join(document.UnknownV1Fields, ", "),
			)
		}
		legacy := *document.Legacy
		if legacy.Name == "" {
			nameScope := firstNonEmpty(env.Deployment.TenantSlug, env.Deployment.TenantID, deploymentID)
			legacy.Name = fmt.Sprintf("%s-%s", nameScope, env.Deployment.StackkitSlug)
		}
		if legacy.StackKit == "" {
			legacy.StackKit = env.Deployment.StackkitSlug
		}
		if legacy.StackKit != env.Deployment.StackkitSlug {
			return tenantSpecFetchResult{}, nil, fmt.Errorf(
				"admin deployment stackkit %q does not match StackSpec v1 stackkit %q",
				env.Deployment.StackkitSlug,
				legacy.StackKit,
			)
		}
		if err := validateManagedIdentityBootstrap(env, &legacy); err != nil {
			return tenantSpecFetchResult{}, nil, err
		}

		// Deployment bindings are a v0.6 compatibility projection. Canonical
		// v2 keeps them in the sidecar and never mixes operational placement
		// or credentials into desired architecture intent.
		if legacy.Environment == nil {
			legacy.Environment = map[string]string{}
		}
		legacy.Environment["STACKKIT_TENANT_DEPLOYMENT_ID"] = env.Deployment.ID
		legacy.Environment["STACKKIT_TENANT_ID"] = env.Deployment.TenantID
		for _, binding := range env.Bindings {
			if binding.SecretKey == "" || binding.DopplerSecretPath == "" {
				continue
			}
			legacy.Environment["STACKKIT_BINDING_"+binding.SecretKey] = binding.DopplerSecretPath
		}
		platform, err := tenantPlatformConfigFromSpecEnvironment(&legacy)
		if err != nil {
			return tenantSpecFetchResult{}, nil, err
		}
		canonical, err := yaml.Marshal(&legacy)
		if err != nil {
			return tenantSpecFetchResult{}, nil, fmt.Errorf("marshal tenant StackSpec v1: %w", err)
		}
		return tenantSpecFetchResult{
			Version:    document.Version,
			KitProfile: stackspecmigration.KitProfile(legacy.StackKit),
			Legacy:     &legacy,
			Platform:   platform,
		}, canonical, nil

	case stackspecmigration.SourceVersionV2Alpha1:
		if document.V2 == nil {
			return tenantSpecFetchResult{}, nil, fmt.Errorf("tenant StackSpec v2 has no dispatch identity")
		}
		if string(document.V2.KitProfile) != env.Deployment.StackkitSlug {
			return tenantSpecFetchResult{}, nil, fmt.Errorf(
				"admin deployment stackkit %q does not match StackSpec v2 kit.slug %q",
				env.Deployment.StackkitSlug,
				document.V2.KitProfile,
			)
		}
		if env.IdentityBootstrap != nil {
			return tenantSpecFetchResult{}, nil, fmt.Errorf("canonical StackSpec v2 identity bootstrap handoff is not yet supported by the governed executor; refusing to persist recovery material")
		}
		intent, err := resolvedplan.DecodeDocument[map[string]any](document.Raw)
		if err != nil {
			return tenantSpecFetchResult{}, nil, fmt.Errorf("decode tenant StackSpec v2: %w", err)
		}
		canonical, err := resolvedplan.CanonicalJSON(intent)
		if err != nil {
			return tenantSpecFetchResult{}, nil, fmt.Errorf("canonicalize tenant StackSpec v2: %w", err)
		}
		canonical = append(canonical, '\n')
		return tenantSpecFetchResult{
			Version:    document.Version,
			KitProfile: document.V2.KitProfile,
		}, canonical, nil

	default:
		return tenantSpecFetchResult{}, nil, fmt.Errorf("unsupported tenant StackSpec version %q", document.Version)
	}
}

func validateManagedIdentityBootstrap(env *tenantSpecEnvelope, spec *models.StackSpec) error {
	if env == nil {
		return fmt.Errorf("admin returned empty tenant spec envelope")
	}
	if spec == nil || spec.Owner.EffectiveBootstrapMode() != models.OwnerBootstrapModeAuto {
		return nil
	}
	if env.IdentityBootstrap == nil {
		return fmt.Errorf("managed owner bootstrap requires identityBootstrap envelope for owner.bootstrapMode=auto")
	}
	return validateIdentityBootstrapEnvelope(env.IdentityBootstrap)
}

func validateIdentityBootstrapEnvelope(envelope *models.OwnerAdminBootstrapEnvelope) error {
	if envelope == nil {
		return fmt.Errorf("identityBootstrap envelope is required")
	}
	owner := envelope.Owner
	if strings.TrimSpace(owner.Email) == "" || strings.TrimSpace(owner.Username) == "" {
		return fmt.Errorf("identityBootstrap.owner requires email and username for managed owner bootstrap")
	}
	if envelope.RecoveryPassphraseHash == "" &&
		envelope.RecoveryPassphrasePlain == "" &&
		owner.RecoveryPassphraseHash == "" {
		return fmt.Errorf("identityBootstrap requires recoveryPassphraseHash or recoveryPassphrasePlain for managed owner bootstrap")
	}
	return nil
}

func tenantPlatformConfigFromSpecEnvironment(spec *models.StackSpec) (*platformConfigFile, error) {
	if spec == nil || len(spec.Environment) == 0 {
		return nil, nil
	}
	cfg, configured := platformConfigFromValueMap(spec.PAAS, spec.Environment)
	if !configured {
		return nil, nil
	}
	if err := validatePlatformConfigPlatform(cfg.Platform); err != nil {
		return nil, err
	}
	if cfg.Platform == models.PAASKomodo {
		if cfg.endpoint() == "" || cfg.APIKey == "" || cfg.APISecret == "" {
			return nil, fmt.Errorf("tenant spec Komodo platform config is incomplete; provide endpoint, API key, and API secret")
		}
	} else if cfg.endpoint() == "" || cfg.Token == "" {
		return nil, fmt.Errorf("tenant spec platform config is incomplete; provide endpoint and token")
	}
	redactPlatformConfigEnvironment(spec.Environment)
	return &cfg, nil
}

const tenantFetchBundleRelative = ".stackkit/tenant-fetch"

type tenantFetchManifest struct {
	DeploymentID  string                           `json:"deploymentId"`
	SourceVersion stackspecmigration.SourceVersion `json:"sourceVersion"`
	KitProfile    stackspecmigration.KitProfile    `json:"kitProfile"`
	SpecPath      string                           `json:"specPath"`
	SpecSHA256    string                           `json:"specSha256"`
	Files         []string                         `json:"files"`
	FileSHA256    map[string]string                `json:"fileSha256"`
}

func persistTenantFetchBundle(wd, requestedSpecPath, deploymentID string, specData []byte, env *tenantSpecEnvelope, result tenantSpecFetchResult) error {
	files := map[string][]byte{}
	bindings, err := json.MarshalIndent(env.Bindings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tenant bindings: %w", err)
	}
	files["tenant-bindings.json"] = append(bindings, '\n')
	if metadata := tenantTelemetryMetadataFor(env.Telemetry); metadata != nil {
		data, marshalErr := json.MarshalIndent(metadata, "", "  ")
		if marshalErr != nil {
			return fmt.Errorf("marshal tenant telemetry metadata: %w", marshalErr)
		}
		files["tenant-telemetry.json"] = append(data, '\n')
	}
	if env.IdentityBootstrap != nil {
		data, marshalErr := json.MarshalIndent(env.IdentityBootstrap, "", "  ")
		if marshalErr != nil {
			return fmt.Errorf("marshal identity bootstrap envelope: %w", marshalErr)
		}
		files["identity-bootstrap.json"] = append(data, '\n')
	}
	if result.Platform != nil {
		data, marshalErr := json.MarshalIndent(result.Platform, "", "  ") // #nosec G117 -- persisted only in a private 0600 tenant bundle.
		if marshalErr != nil {
			return fmt.Errorf("marshal tenant platform config: %w", marshalErr)
		}
		files["platform.json"] = append(data, '\n')
	}

	specRelative := filepath.ToSlash(filepath.Clean(requestedSpecPath))
	names := make([]string, 0, len(files))
	fileDigests := make(map[string]string, len(files))
	for name := range files {
		names = append(names, name)
		fileDigest := sha256.Sum256(files[name])
		fileDigests[name] = fmt.Sprintf("%x", fileDigest[:])
	}
	sort.Strings(names)
	digest := sha256.Sum256(specData)
	manifest := tenantFetchManifest{
		DeploymentID:  deploymentID,
		SourceVersion: result.Version,
		KitProfile:    result.KitProfile,
		SpecPath:      specRelative,
		SpecSHA256:    fmt.Sprintf("%x", digest[:]),
		Files:         append([]string(nil), names...),
		FileSHA256:    fileDigests,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tenant fetch manifest: %w", err)
	}
	files["manifest.json"] = append(manifestData, '\n')

	root, err := confinedfs.Open(wd)
	if err != nil {
		return fmt.Errorf("open tenant fetch workspace: %w", err)
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	lock, err := transaction.TryAcquireOutputLock(tenantFetchBundleRelative)
	if err != nil {
		return fmt.Errorf("acquire tenant fetch lock: %w", err)
	}
	defer lock.Release()
	specExists, _, err := transaction.Exists(specRelative)
	if err != nil {
		return err
	}
	bundleExists, _, err := transaction.Exists(tenantFetchBundleRelative)
	if err != nil {
		return err
	}
	if bundleExists {
		if err := verifyPublishedTenantBundle(transaction, specRelative, deploymentID, specData, result, fileDigests); err != nil {
			return fmt.Errorf("existing tenant fetch bundle is not resumable; move %s aside after review: %w", filepath.Join(wd, filepath.FromSlash(tenantFetchBundleRelative)), err)
		}
		if specExists {
			persisted, _, readErr := transaction.ReadStable(specRelative)
			if readErr != nil || !bytes.Equal(persisted, specData) {
				return fmt.Errorf("existing tenant StackSpec does not match the resumable bundle")
			}
			return nil
		}
		_, publishErr := publishTenantStackSpec(root, transaction, specRelative, specData)
		return publishErr
	}
	if specExists {
		return fmt.Errorf("tenant StackSpec target %s already exists; refusing to replace operator intent", requestedSpecPath)
	}
	if err := transaction.MkdirAll(".stackkit", 0o700); err != nil {
		return err
	}
	stage, err := transaction.CreatePrivateDirectory(".stackkit-tenant-fetch-")
	if err != nil {
		return err
	}
	stageInstalled := false
	defer func() {
		if !stageInstalled {
			_ = transaction.RemoveTree(stage)
		}
	}()
	for name, data := range files {
		if err := transaction.WriteFileExclusive(stage+"/"+name, data, 0o600); err != nil {
			return err
		}
	}
	installed, err := transaction.Rename(stage, tenantFetchBundleRelative)
	if err != nil {
		if installed {
			_ = transaction.RemoveTree(tenantFetchBundleRelative)
		}
		return fmt.Errorf("publish tenant fetch bundle: %w", err)
	}
	stageInstalled = true
	writeInstalled, err := publishTenantStackSpec(root, transaction, specRelative, specData)
	if err != nil {
		if !writeInstalled {
			_ = transaction.RemoveTree(tenantFetchBundleRelative)
		}
		return fmt.Errorf("publish tenant StackSpec without replacement: %w", err)
	}
	return nil
}

func publishTenantStackSpec(root *confinedfs.Root, transaction *confinedfs.Transaction, specRelative string, specData []byte) (bool, error) {
	if err := transaction.MkdirAll(filepath.ToSlash(filepath.Dir(specRelative)), 0o750); err != nil {
		return false, err
	}
	view, err := root.View(".")
	if err != nil {
		return false, err
	}
	result, err := view.WriteAtomic0600NoReplace(specRelative, specData)
	if err != nil {
		return result.Installed, fmt.Errorf("publish tenant StackSpec without replacement: %w", err)
	}
	return true, nil
}

func verifyPublishedTenantBundle(transaction *confinedfs.Transaction, specRelative, deploymentID string, specData []byte, result tenantSpecFetchResult, expectedFileDigests map[string]string) error {
	manifestData, _, err := transaction.ReadStable(tenantFetchBundleRelative + "/manifest.json")
	if err != nil {
		return err
	}
	var manifest tenantFetchManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}
	digest := sha256.Sum256(specData)
	if manifest.DeploymentID != deploymentID || manifest.SpecPath != specRelative || manifest.SourceVersion != result.Version || manifest.KitProfile != result.KitProfile || manifest.SpecSHA256 != fmt.Sprintf("%x", digest[:]) {
		return fmt.Errorf("manifest identity or StackSpec digest differs from the fetched deployment")
	}
	if expectedFileDigests != nil {
		if len(manifest.FileSHA256) != len(expectedFileDigests) {
			return fmt.Errorf("manifest sidecar set differs from the fetched deployment")
		}
		for name, expected := range expectedFileDigests {
			if manifest.FileSHA256[name] != expected {
				return fmt.Errorf("manifest sidecar %s differs from the fetched deployment", name)
			}
		}
	}
	for _, name := range manifest.Files {
		data, _, err := transaction.ReadStable(tenantFetchBundleRelative + "/" + name)
		if err != nil {
			return err
		}
		fileDigest := sha256.Sum256(data)
		if manifest.FileSHA256[name] != fmt.Sprintf("%x", fileDigest[:]) {
			return fmt.Errorf("sidecar %s digest mismatch", name)
		}
	}
	return nil
}

func withVerifiedTenantFetchBundle(wd, requestedSpecPath, deploymentID string, result tenantSpecFetchResult, run func() (bool, error)) (bool, error) {
	if run == nil {
		return false, fmt.Errorf("tenant bundle callback is required")
	}
	root, err := confinedfs.Open(wd)
	if err != nil {
		return false, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return false, err
	}
	defer transaction.Close()
	lock, err := transaction.TryAcquireOutputLock(tenantFetchBundleRelative)
	if err != nil {
		return false, err
	}
	defer lock.Release()
	specRelative := filepath.ToSlash(filepath.Clean(requestedSpecPath))
	specData, _, err := transaction.ReadStable(specRelative)
	if err != nil {
		return false, err
	}
	if err := verifyPublishedTenantBundle(transaction, specRelative, deploymentID, specData, result, nil); err != nil {
		return false, err
	}
	return run()
}

func verifyTenantFetchBundle(wd, requestedSpecPath, deploymentID string, result tenantSpecFetchResult) error {
	root, err := confinedfs.Open(wd)
	if err != nil {
		return fmt.Errorf("open tenant fetch verification workspace: %w", err)
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer transaction.Close()
	lock, err := transaction.TryAcquireOutputLock(tenantFetchBundleRelative)
	if err != nil {
		return fmt.Errorf("acquire tenant fetch verification lock: %w", err)
	}
	defer lock.Release()
	manifestData, _, err := transaction.ReadStable(tenantFetchBundleRelative + "/manifest.json")
	if err != nil {
		return fmt.Errorf("read tenant fetch manifest: %w", err)
	}
	var manifest tenantFetchManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("decode tenant fetch manifest: %w", err)
	}
	specRelative := filepath.ToSlash(filepath.Clean(requestedSpecPath))
	if manifest.DeploymentID != deploymentID || manifest.SpecPath != specRelative || manifest.SourceVersion != result.Version || manifest.KitProfile != result.KitProfile {
		return fmt.Errorf("tenant fetch manifest identity does not match the admitted deployment and StackSpec")
	}
	specData, _, err := transaction.ReadStable(specRelative)
	if err != nil {
		return fmt.Errorf("read tenant StackSpec for bundle verification: %w", err)
	}
	digest := sha256.Sum256(specData)
	if manifest.SpecSHA256 != fmt.Sprintf("%x", digest[:]) {
		return fmt.Errorf("tenant fetch manifest StackSpec digest mismatch")
	}
	document, err := stackspecmigration.Read(specData)
	if err != nil || document.Version != result.Version {
		return fmt.Errorf("tenant fetch StackSpec classification mismatch: version=%q error=%v", document.Version, err)
	}
	if document.V2 != nil && document.V2.KitProfile != result.KitProfile {
		return fmt.Errorf("tenant fetch StackSpec kit identity mismatch")
	}
	if document.Legacy != nil && stackspecmigration.KitProfile(document.Legacy.StackKit) != result.KitProfile {
		return fmt.Errorf("tenant fetch legacy StackSpec kit identity mismatch")
	}
	for _, name := range manifest.Files {
		if name == "" || strings.ContainsAny(name, `/\\`) {
			return fmt.Errorf("tenant fetch manifest contains invalid sidecar name %q", name)
		}
		data, _, err := transaction.ReadStable(tenantFetchBundleRelative + "/" + name)
		if err != nil {
			return fmt.Errorf("verify tenant fetch sidecar %s: %w", name, err)
		}
		fileDigest := sha256.Sum256(data)
		if manifest.FileSHA256[name] != fmt.Sprintf("%x", fileDigest[:]) {
			return fmt.Errorf("tenant fetch sidecar %s digest mismatch", name)
		}
	}
	return nil
}

func readVerifiedTenantSidecar(wd, requestedSpecPath, deploymentID, name string) ([]byte, bool, error) {
	if name == "" || strings.ContainsAny(name, `/\\`) {
		return nil, false, fmt.Errorf("invalid tenant sidecar name %q", name)
	}
	root, err := confinedfs.Open(wd)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return nil, false, err
	}
	defer transaction.Close()
	lock, err := transaction.TryAcquireOutputLock(tenantFetchBundleRelative)
	if err != nil {
		return nil, false, err
	}
	defer lock.Release()
	manifestData, _, err := transaction.ReadStable(tenantFetchBundleRelative + "/manifest.json")
	if err != nil {
		return nil, false, err
	}
	var manifest tenantFetchManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, false, fmt.Errorf("decode tenant fetch manifest: %w", err)
	}
	specRelative := filepath.ToSlash(filepath.Clean(requestedSpecPath))
	if manifest.DeploymentID != deploymentID || manifest.SpecPath != specRelative {
		return nil, false, fmt.Errorf("tenant sidecar manifest is not bound to deployment %s and spec %s", deploymentID, specRelative)
	}
	specData, _, err := transaction.ReadStable(specRelative)
	if err != nil {
		return nil, false, err
	}
	specDigest := sha256.Sum256(specData)
	if manifest.SpecSHA256 != fmt.Sprintf("%x", specDigest[:]) {
		return nil, false, fmt.Errorf("tenant sidecar manifest StackSpec digest mismatch")
	}
	expectedDigest, declared := manifest.FileSHA256[name]
	if !declared {
		return nil, false, nil
	}
	data, _, err := transaction.ReadStable(tenantFetchBundleRelative + "/" + name)
	if err != nil {
		return nil, false, err
	}
	digest := sha256.Sum256(data)
	if expectedDigest != fmt.Sprintf("%x", digest[:]) {
		return nil, false, fmt.Errorf("tenant sidecar %s digest mismatch", name)
	}
	return data, true, nil
}

func tenantFetchManifestExists(wd string) (bool, error) {
	info, err := os.Lstat(filepath.Join(wd, filepath.FromSlash(tenantFetchBundleRelative), "manifest.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("tenant fetch manifest is not a plain regular file")
	}
	return true, nil
}

func tenantTelemetryMetadataFor(telemetry tenantSpecTelemetry) *tenantTelemetryMetadata {
	if !tenantTelemetryConfigured(telemetry) {
		return nil
	}
	keys := make([]string, 0, len(telemetry.OTLPHeaders))
	for key := range telemetry.OTLPHeaders {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return &tenantTelemetryMetadata{
		SentryDSNConfigured: strings.TrimSpace(telemetry.SentryDSN) != "",
		OTLPEndpointSet:     strings.TrimSpace(telemetry.OTLPEndpoint) != "",
		OTLPHeaderKeys:      keys,
		Environment:         strings.TrimSpace(telemetry.Environment),
		Release:             strings.TrimSpace(telemetry.Release),
		RolloutMode:         strings.TrimSpace(telemetry.RolloutMode),
	}
}

func tenantTelemetryConfigured(telemetry tenantSpecTelemetry) bool {
	return strings.TrimSpace(telemetry.SentryDSN) != "" ||
		strings.TrimSpace(telemetry.OTLPEndpoint) != "" ||
		len(telemetry.OTLPHeaders) > 0 ||
		strings.TrimSpace(telemetry.Environment) != "" ||
		strings.TrimSpace(telemetry.Release) != "" ||
		strings.TrimSpace(telemetry.RolloutMode) != ""
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
