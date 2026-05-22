package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
)

const (
	setupActionModeApply  = "apply"
	setupActionModeDryRun = "dry-run"
)

type serviceSetupResponse struct {
	ServiceKey       string              `json:"serviceKey"`
	DisplayName      string              `json:"displayName"`
	AppName          string              `json:"appName,omitempty"`
	SetupPolicy      string              `json:"setupPolicy"`
	SetupActionLabel string              `json:"setupActionLabel,omitempty"`
	Mode             string              `json:"mode,omitempty"`
	Status           string              `json:"status"`
	Message          string              `json:"message"`
	Drops            []setupDropResponse `json:"drops,omitempty"`
}

type setupDropResponse struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Runner      string `json:"runner,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
}

type baseHubProtectionResponse struct {
	Status               string `json:"status"`
	Mode                 string `json:"mode,omitempty"`
	Protected            bool   `json:"protected"`
	Message              string `json:"message"`
	TFVarsUpdated        bool   `json:"tfvarsUpdated,omitempty"`
	DynamicConfigUpdated bool   `json:"dynamicConfigUpdated,omitempty"`
}

type baseHubProtectionState struct {
	tfvarsPath        string
	dynamicConfigPath string
	tfvars            map[string]any
	enableTinyAuth    bool
	tfvarsProtected   bool
	dynamicProtected  bool
	dynamicExists     bool
	networkMode       string
}

func (s *Server) handleGetBaseHubProtection(w http.ResponseWriter, r *http.Request) {
	state, err := s.loadBaseHubProtectionState()
	if err != nil {
		writeStructuredError(w, r, http.StatusConflict, err)
		return
	}

	protected := state.effectiveProtected()
	resp := baseHubProtectionResponse{
		Status:    baseHubProtectionStatus(protected),
		Mode:      s.setupActionMode(),
		Protected: protected,
		Message:   baseHubProtectionMessage(protected),
	}
	writeSuccess(w, r, http.StatusOK, resp)
}

func (s *Server) handleProtectBaseHub(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()

	state, err := s.loadBaseHubProtectionState()
	if err != nil {
		writeStructuredError(w, r, http.StatusConflict, err)
		return
	}
	mode := s.setupActionMode()
	if mode != setupActionModeApply {
		writeSuccess(w, r, http.StatusAccepted, baseHubProtectionResponse{
			Status:    "ready",
			Mode:      mode,
			Protected: state.effectiveProtected(),
			Message:   "Base Hub protection was validated but not applied because setup actions are in dry-run mode.",
		})
		return
	}
	if !state.enableTinyAuth {
		writeStructuredError(w, r, http.StatusConflict, skerrors.NewValidationError(
			"base_hub_protection_requires_tinyauth",
			"Base Hub protection requires TinyAuth",
			skerrors.WithSuggestion("Enable TinyAuth in the StackKit spec and re-apply before protecting Base Hub"),
		))
		return
	}

	state.tfvars["protect_base_hub"] = true
	tfvarsData, marshalErr := json.MarshalIndent(state.tfvars, "", "  ")
	if marshalErr != nil {
		writeStructuredError(w, r, http.StatusInternalServerError, skerrors.NewDeploymentError(
			"base_hub_protection_tfvars_marshal_failed",
			"failed to persist Base Hub protection settings",
			skerrors.WithCause(marshalErr),
		))
		return
	}
	tfvarsData = append(tfvarsData, '\n')

	if writeErr := os.WriteFile(state.tfvarsPath, tfvarsData, 0600); writeErr != nil {
		writeStructuredError(w, r, http.StatusInternalServerError, skerrors.NewDeploymentError(
			"base_hub_protection_tfvars_write_failed",
			"failed to persist Base Hub protection settings",
			skerrors.WithField("path", state.tfvarsPath),
			skerrors.WithCause(writeErr),
		))
		return
	}
	if writeErr := writeBaseHubProtectionDynamicConfig(state.dynamicConfigPath, state.networkMode, true); writeErr != nil {
		writeStructuredError(w, r, http.StatusInternalServerError, skerrors.NewDeploymentError(
			"base_hub_protection_dynamic_config_failed",
			"failed to activate Base Hub protection in the local router",
			skerrors.WithField("path", state.dynamicConfigPath),
			skerrors.WithCause(writeErr),
			skerrors.WithSuggestion("Re-run StackKit apply; the Base Hub protection setting has been persisted for the next rollout"),
		))
		return
	}

	writeSuccess(w, r, http.StatusOK, baseHubProtectionResponse{
		Status:               "completed",
		Mode:                 mode,
		Protected:            true,
		Message:              "Base Hub and the node-local API are now protected by TinyAuth.",
		TFVarsUpdated:        true,
		DynamicConfigUpdated: true,
	})
}

func (s *Server) handleRunServiceSetup(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("service")
	service, ok := serviceByKey(key)
	if !ok {
		writeStructuredError(w, r, http.StatusNotFound, skerrors.NewValidationError(
			"setup_service_not_found",
			"setup service not found",
			skerrors.WithField("service", key),
		))
		return
	}

	resp := serviceSetupResponse{
		ServiceKey:       service.Key,
		DisplayName:      service.DisplayName,
		SetupPolicy:      service.SetupPolicy,
		SetupActionLabel: service.SetupActionLabel,
	}

	switch service.SetupPolicy {
	case servicecatalog.SetupPolicyAutomatic:
		resp.Status = "already_automatic"
		resp.Message = "StackKit configures this platform service during rollout."
		writeSuccess(w, r, http.StatusOK, resp)
	case servicecatalog.SetupPolicyOnDemand:
		resp, status, err := s.runOnDemandServiceSetup(r.Context(), service)
		if err != nil {
			writeStructuredError(w, r, status, err)
			return
		}
		writeSuccess(w, r, status, resp)
	default:
		writeStructuredError(w, r, http.StatusConflict, skerrors.NewValidationError(
			"setup_not_automated",
			"this service only has a manual setup guide",
			skerrors.WithField("service", service.Key),
			skerrors.WithSuggestion("Open the How to Setup and Use guide for this service"),
		))
	}
}

func (s *Server) runOnDemandServiceSetup(ctx context.Context, service servicecatalog.Service) (serviceSetupResponse, int, *skerrors.StackKitError) {
	bundle, manifestPath, err := s.loadSetupBundle()
	if err != nil {
		return serviceSetupResponse{}, http.StatusConflict, skerrors.NewValidationError(
			"setup_manifest_unavailable",
			"setup manifest is not available for this StackKit deployment",
			skerrors.WithField("service", service.Key),
			skerrors.WithCause(err),
			skerrors.WithSuggestion("Run stackkit generate/apply so .platform-apps-manifest.json is present in the node workspace"),
		)
	}

	app, ok := findSetupApp(bundle, service)
	if !ok {
		return serviceSetupResponse{}, http.StatusConflict, skerrors.NewValidationError(
			"setup_app_not_found",
			"setup manifest does not contain an app for this service",
			skerrors.WithField("service", service.Key),
			skerrors.WithField("manifest", manifestPath),
		)
	}
	if app.SetupPolicy != platformdeploy.SetupPolicyOnDemand {
		return serviceSetupResponse{}, http.StatusConflict, skerrors.NewValidationError(
			"setup_policy_mismatch",
			"setup manifest does not allow on-demand setup for this service",
			skerrors.WithField("service", service.Key),
			skerrors.WithField("app", app.Name),
			skerrors.WithField("policy", app.SetupPolicy),
		)
	}
	if len(app.SetupDrops) == 0 {
		return serviceSetupResponse{}, http.StatusConflict, skerrors.NewValidationError(
			"setup_drop_missing",
			"setup manifest does not define a setup drop for this service",
			skerrors.WithField("service", service.Key),
			skerrors.WithField("app", app.Name),
		)
	}

	mode := s.setupActionMode()
	resp := serviceSetupResponse{
		ServiceKey:       service.Key,
		DisplayName:      service.DisplayName,
		AppName:          app.Name,
		SetupPolicy:      service.SetupPolicy,
		SetupActionLabel: service.SetupActionLabel,
		Mode:             mode,
		Status:           "ready",
		Message:          "On-demand setup is available for this service.",
		Drops:            setupDropResponses(app.SetupDrops, "ready"),
	}

	if mode != setupActionModeApply {
		resp.Message = "On-demand setup was validated but not executed because setup actions are in dry-run mode."
		return resp, http.StatusAccepted, nil
	}

	completed := make([]setupDropResponse, 0, len(app.SetupDrops))
	for _, drop := range app.SetupDrops {
		if err := s.runSetupDrop(ctx, service, app, drop); err != nil {
			return serviceSetupResponse{}, setupHTTPStatus(err), err
		}
		completed = append(completed, setupDropResponse{
			Name:        drop.Name,
			Version:     drop.Version,
			Runner:      drop.Runner,
			Description: drop.Description,
			Status:      "completed",
		})
	}

	resp.Status = "completed"
	resp.Message = "On-demand setup completed."
	resp.Drops = completed
	return resp, http.StatusOK, nil
}

func (s *Server) loadSetupBundle() (platformdeploy.BundleManifest, string, error) {
	var lastErr error
	for _, candidate := range setupManifestCandidates(s.config.BaseDir) {
		bundle, err := platformdeploy.LoadBundleManifest(candidate)
		if err == nil {
			return bundle, candidate, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no manifest path candidates")
	}
	return platformdeploy.BundleManifest{}, "", lastErr
}

func setupManifestCandidates(baseDir string) []string {
	if baseDir == "" {
		baseDir = "."
	}
	return []string{
		filepath.Join(baseDir, "platform-apps", "manifest.json"),
		filepath.Join(baseDir, ".platform-apps-manifest.json"),
		filepath.Join(baseDir, "deploy", "platform-apps", "manifest.json"),
		filepath.Join(baseDir, "deploy", ".platform-apps-manifest.json"),
	}
}

func (s *Server) loadBaseHubProtectionState() (baseHubProtectionState, *skerrors.StackKitError) {
	tfvarsPath, tfvars, err := loadBaseHubTFVars(s.config.BaseDir)
	if err != nil {
		return baseHubProtectionState{}, skerrors.NewValidationError(
			"base_hub_tfvars_unavailable",
			"Base Hub protection requires generated StackKit inputs",
			skerrors.WithCause(err),
			skerrors.WithSuggestion("Run stackkit generate/apply so deploy/terraform.tfvars.json is present in the node workspace"),
		)
	}

	dynamicConfigPath := baseHubDynamicConfigPath(tfvarsPath)
	dynamicData, readErr := os.ReadFile(dynamicConfigPath)
	dynamicExists := readErr == nil

	return baseHubProtectionState{
		tfvarsPath:        tfvarsPath,
		dynamicConfigPath: dynamicConfigPath,
		tfvars:            tfvars,
		enableTinyAuth:    boolTFVar(tfvars, "enable_tinyauth", true),
		tfvarsProtected:   boolTFVar(tfvars, "protect_base_hub", false),
		dynamicProtected:  dynamicExists && bytes.Contains(dynamicData, []byte("forwardAuth:")),
		dynamicExists:     dynamicExists,
		networkMode:       stringTFVar(tfvars, "network_mode", "bridge"),
	}, nil
}

func loadBaseHubTFVars(baseDir string) (string, map[string]any, error) {
	var lastErr error
	for _, candidate := range baseHubTFVarsCandidates(baseDir) {
		data, err := os.ReadFile(candidate)
		if err != nil {
			lastErr = err
			continue
		}
		var tfvars map[string]any
		if err := json.Unmarshal(data, &tfvars); err != nil {
			return "", nil, fmt.Errorf("parse %s: %w", candidate, err)
		}
		return candidate, tfvars, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no tfvars path candidates")
	}
	return "", nil, lastErr
}

func baseHubTFVarsCandidates(baseDir string) []string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	return []string{
		filepath.Join(baseDir, "deploy", "terraform.tfvars.json"),
		filepath.Join(baseDir, "terraform.tfvars.json"),
	}
}

func baseHubDynamicConfigPath(tfvarsPath string) string {
	return filepath.Join(filepath.Dir(tfvarsPath), "traefik-dynamic", "stackkit.yml")
}

func (s baseHubProtectionState) effectiveProtected() bool {
	if s.dynamicExists {
		return s.dynamicProtected
	}
	return s.tfvarsProtected
}

func baseHubProtectionStatus(protected bool) string {
	if protected {
		return "protected"
	}
	return "bootstrap_open"
}

func baseHubProtectionMessage(protected bool) string {
	if protected {
		return "Base Hub and the node-local API are protected by TinyAuth."
	}
	return "Base Hub is open for first setup. Use the protection action after the owner setup is complete."
}

func boolTFVar(tfvars map[string]any, key string, fallback bool) bool {
	value, ok := tfvars[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

func stringTFVar(tfvars map[string]any, key, fallback string) string {
	value, ok := tfvars[key]
	if !ok {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return fallback
	}
	return text
}

func writeBaseHubProtectionDynamicConfig(path, networkMode string, protected bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(baseHubProtectionDynamicConfig(networkMode, protected)), 0640)
}

func baseHubProtectionDynamicConfig(networkMode string, protected bool) string {
	if !protected {
		return `http:
  middlewares:
    base-hub-auth:
      headers:
        customResponseHeaders:
          X-StackKit-Base-Hub-Mode: "bootstrap"
`
	}
	address := "http://tinyauth:3000/api/auth/traefik"
	if strings.EqualFold(strings.TrimSpace(networkMode), "host") {
		address = "http://127.0.0.1:3004/api/auth/traefik"
	}
	return fmt.Sprintf(`http:
  middlewares:
    base-hub-auth:
      forwardAuth:
        address: %q
        trustForwardHeader: true
        authResponseHeaders:
          - "X-User"
          - "X-Email"
`, address)
}

func findSetupApp(bundle platformdeploy.BundleManifest, service servicecatalog.Service) (platformdeploy.AppManifest, bool) {
	candidates := setupAppCandidates(service)
	for _, app := range bundle.Apps {
		if setupAppNameMatches(app.Name, candidates) {
			return app, true
		}
	}
	for _, systemApp := range bundle.SystemApps {
		app := systemApp.AppManifest
		if setupAppNameMatches(app.Name, candidates) {
			return app, true
		}
	}
	return platformdeploy.AppManifest{}, false
}

func setupAppCandidates(service servicecatalog.Service) map[string]struct{} {
	values := []string{service.Key, service.Name, service.ToolName, service.ModuleSlug, service.LocalSlug, service.PublicSlug}
	candidates := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(strings.ToLower(value)); value != "" {
			candidates[value] = struct{}{}
		}
	}
	return candidates
}

func setupAppNameMatches(name string, candidates map[string]struct{}) bool {
	_, ok := candidates[strings.TrimSpace(strings.ToLower(name))]
	return ok
}

func setupDropResponses(drops []platformdeploy.SetupDropManifest, status string) []setupDropResponse {
	out := make([]setupDropResponse, 0, len(drops))
	for _, drop := range drops {
		out = append(out, setupDropResponse{
			Name:        drop.Name,
			Version:     drop.Version,
			Runner:      drop.Runner,
			Description: drop.Description,
			Status:      status,
		})
	}
	return out
}

func (s *Server) setupActionMode() string {
	switch strings.ToLower(strings.TrimSpace(s.config.SetupActionMode)) {
	case setupActionModeApply:
		return setupActionModeApply
	default:
		return setupActionModeDryRun
	}
}

func (s *Server) runSetupDrop(ctx context.Context, service servicecatalog.Service, app platformdeploy.AppManifest, drop platformdeploy.SetupDropManifest) *skerrors.StackKitError {
	switch drop.Name {
	case "immich-owner-bootstrap":
		return s.runImmichOwnerBootstrap(ctx)
	default:
		return skerrors.NewValidationError(
			"setup_runner_not_implemented",
			"this setup drop does not have a node-local runner yet",
			skerrors.WithField("service", service.Key),
			skerrors.WithField("app", app.Name),
			skerrors.WithField("drop", drop.Name),
			skerrors.WithField("runner", drop.Runner),
		)
	}
}

func (s *Server) runImmichOwnerBootstrap(ctx context.Context) *skerrors.StackKitError {
	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupImmichURL, "http://immich:2283"), "/")
	email := strings.TrimSpace(s.config.SetupAdminEmail)
	password := strings.TrimSpace(s.config.SetupAdminPassword)
	if email == "" || password == "" {
		return skerrors.NewValidationError(
			"setup_credentials_missing",
			"Immich owner bootstrap requires StackKit admin credentials",
			skerrors.WithSuggestion("Set STACKKIT_ADMIN_EMAIL and STACKKIT_ADMIN_PASSWORD for stackkit-server"),
		)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	var config struct {
		IsInitialized bool `json:"isInitialized"`
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodGet, "/api/server/config", nil, "", &config); err != nil {
		return skerrors.NewDependencyError("immich_config_failed", "failed to read Immich server config", skerrors.WithCause(err))
	}
	if !config.IsInitialized {
		payload := map[string]string{
			"email":    email,
			"password": password,
			"name":     "StackKit Admin",
		}
		if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/auth/admin-sign-up", payload, "", nil); err != nil {
			return skerrors.NewDependencyError("immich_admin_signup_failed", "failed to create Immich owner", skerrors.WithCause(err))
		}
	}

	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/auth/login", map[string]string{
		"email":    email,
		"password": password,
	}, "", &login); err != nil {
		return skerrors.NewAuthError("immich_login_failed", "failed to log in to Immich with StackKit admin credentials", skerrors.WithCause(err))
	}
	if strings.TrimSpace(login.AccessToken) == "" {
		return skerrors.NewAuthError("immich_login_missing_token", "Immich login did not return an access token")
	}

	token := login.AccessToken
	name := "StackKit Admin"
	if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/users/me", map[string]string{
		"name":     name,
		"password": password,
	}, token, nil); err != nil {
		return skerrors.NewDependencyError("immich_profile_update_failed", "failed to update Immich owner profile", skerrors.WithCause(err))
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/users/me/onboarding", map[string]bool{
		"isOnboarded": true,
	}, token, nil); err != nil {
		return skerrors.NewDependencyError("immich_user_onboarding_failed", "failed to complete Immich user onboarding", skerrors.WithCause(err))
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/system-metadata/admin-onboarding", map[string]bool{
		"isOnboarded": true,
	}, token, nil); err != nil {
		return skerrors.NewDependencyError("immich_admin_onboarding_failed", "failed to complete Immich admin onboarding", skerrors.WithCause(err))
	}

	return nil
}

func setupHTTPStatus(err *skerrors.StackKitError) int {
	if err.Code == "setup_runner_not_implemented" {
		return http.StatusNotImplemented
	}
	switch err.Category {
	case skerrors.CategoryValidation:
		return http.StatusConflict
	case skerrors.CategoryAuth:
		return http.StatusBadGateway
	case skerrors.CategoryDependency:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func immichRequest(ctx context.Context, client *http.Client, baseURL, method, path string, payload any, token string, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func serviceByKey(key string) (servicecatalog.Service, bool) {
	for _, service := range servicecatalog.Default() {
		if service.Key == key {
			return service, true
		}
	}
	return servicecatalog.Service{}, false
}
