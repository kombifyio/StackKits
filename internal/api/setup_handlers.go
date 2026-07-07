package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kombifyio/stackkits/internal/config"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
	"github.com/kombifyio/stackkits/internal/platformdeploy"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

const (
	setupActionModeApply  = "apply"
	setupActionModeDryRun = "dry-run"

	initialAccessCredentialRole     = "technical-admin"
	initialAccessOwnerLogin         = "pocketid-passkey"
	initialAccessCredentialBoundary = "Bootstrap/service setup credentials; Owner login stays PocketID passkey."

	immichPocketIDClientID = "stackkit-immich"
)

type pocketIDUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	DisplayName string `json:"displayName"`
	IsAdmin     bool   `json:"isAdmin"`
	Disabled    bool   `json:"disabled"`
}

type immichAdminUser struct {
	ID                   string `json:"id"`
	Email                string `json:"email"`
	Name                 string `json:"name"`
	IsAdmin              bool   `json:"isAdmin"`
	ShouldChangePassword bool   `json:"shouldChangePassword"`
	OAuthID              string `json:"oauthId"`
}

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
	Name          string                    `json:"name"`
	RunID         string                    `json:"runId,omitempty"`
	Version       string                    `json:"version,omitempty"`
	Runner        string                    `json:"runner,omitempty"`
	Description   string                    `json:"description,omitempty"`
	Status        string                    `json:"status"`
	Phase         string                    `json:"phase,omitempty"`
	Attempts      int                       `json:"attempts,omitempty"`
	Message       string                    `json:"message,omitempty"`
	Error         string                    `json:"error,omitempty"`
	FailureClass  string                    `json:"failureClass,omitempty"`
	Evidence      map[string]string         `json:"evidence,omitempty"`
	Logs          []models.SetupRunLogEntry `json:"logs,omitempty"`
	RollbackNotes []string                  `json:"rollbackNotes,omitempty"`
	LastRequested time.Time                 `json:"lastRequested,omitempty"`
	LastStarted   time.Time                 `json:"lastStarted,omitempty"`
	LastFinished  time.Time                 `json:"lastFinished,omitempty"`
}

type baseHubProtectionResponse struct {
	Status               string `json:"status"`
	Mode                 string `json:"mode,omitempty"`
	Protected            bool   `json:"protected"`
	Message              string `json:"message"`
	TFVarsUpdated        bool   `json:"tfvarsUpdated,omitempty"`
	DynamicConfigUpdated bool   `json:"dynamicConfigUpdated,omitempty"`
}

type initialAccessResponse struct {
	Status             string                    `json:"status"`
	Mode               string                    `json:"mode,omitempty"`
	Protected          bool                      `json:"protected"`
	Available          bool                      `json:"available"`
	Consumed           bool                      `json:"consumed"`
	CredentialRole     string                    `json:"credentialRole"`
	OwnerLogin         string                    `json:"ownerLogin"`
	CredentialBoundary string                    `json:"credentialBoundary"`
	Message            string                    `json:"message"`
	Credentials        *initialAccessCredentials `json:"credentials,omitempty"`
}

type initialAccessCredentials struct {
	AdminEmail         string   `json:"adminEmail"`
	AdminPassword      string   `json:"adminPassword"`
	SelectedPaaS       string   `json:"selectedPaaS"`
	CredentialRole     string   `json:"credentialRole"`
	OwnerLogin         string   `json:"ownerLogin"`
	CredentialBoundary string   `json:"credentialBoundary"`
	IntendedFor        []string `json:"intendedFor"`
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

type initialAccessState struct {
	protected     bool
	consumed      bool
	markerPath    string
	adminEmail    string
	adminPassword string
	selectedPaaS  string
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

func (s *Server) handleGetInitialAccess(w http.ResponseWriter, r *http.Request) {
	state, err := s.loadInitialAccessState()
	if err != nil {
		writeStructuredError(w, r, http.StatusConflict, err)
		return
	}
	writeSuccess(w, r, http.StatusOK, state.toResponse(nil))
}

func (s *Server) handleRevealInitialAccess(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()

	state, err := s.loadInitialAccessState()
	if err != nil {
		writeStructuredError(w, r, http.StatusConflict, err)
		return
	}
	if !state.protected {
		writeStructuredError(w, r, http.StatusConflict, skerrors.NewValidationError(
			"initial_access_requires_protected_base",
			"Initial access credentials can only be revealed after Base Hub is protected",
			skerrors.WithSuggestion("Complete PocketID owner setup, click Protect Base Hub, then log in through TinyAuth and retry"),
		))
		return
	}
	if state.consumed {
		writeSuccess(w, r, http.StatusGone, state.toResponse(nil))
		return
	}
	if state.adminEmail == "" || state.adminPassword == "" {
		writeStructuredError(w, r, http.StatusConflict, skerrors.NewValidationError(
			"initial_access_credentials_unavailable",
			"Initial access credentials are not available on this node",
			skerrors.WithSuggestion("Use the terminal output, recovery bundle, or Techstack Wallet for this deployment"),
		))
		return
	}

	credentials := state.credentials()
	if writeErr := writeInitialAccessMarker(state.markerPath, credentials); writeErr != nil {
		if os.IsExist(writeErr) {
			state.consumed = true
			writeSuccess(w, r, http.StatusGone, state.toResponse(nil))
			return
		}
		writeStructuredError(w, r, http.StatusInternalServerError, skerrors.NewDeploymentError(
			"initial_access_marker_write_failed",
			"failed to mark initial access credentials as revealed",
			skerrors.WithField("path", state.markerPath),
			skerrors.WithCause(writeErr),
		))
		return
	}
	state.consumed = true
	writeSuccess(w, r, http.StatusOK, state.toResponse(credentials))
}

func (s *Server) handleRunServiceSetup(w http.ResponseWriter, r *http.Request) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()

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

	resp, status, err := s.runManifestServiceSetup(r.Context(), service, resp)
	if err != nil {
		writeStructuredError(w, r, status, err)
		return
	}
	writeSuccess(w, r, status, resp)
}

func (s *Server) runManifestServiceSetup(ctx context.Context, service servicecatalog.Service, baseResp serviceSetupResponse) (serviceSetupResponse, int, *skerrors.StackKitError) {
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
	effectivePolicy := strings.TrimSpace(app.SetupPolicy)
	if effectivePolicy == "" {
		effectivePolicy = servicecatalog.SetupPolicyManual
	}
	if effectivePolicy == servicecatalog.SetupPolicyManual {
		return serviceSetupResponse{}, http.StatusConflict, skerrors.NewValidationError(
			"setup_not_automated",
			"this service only has a manual setup guide",
			skerrors.WithField("service", service.Key),
			skerrors.WithField("app", app.Name),
			skerrors.WithSuggestion("Open the How to Setup and Use guide for this service"),
		)
	}
	if effectivePolicy == servicecatalog.SetupPolicyAutomatic && service.Section != "Applications" && len(app.SetupDrops) == 0 {
		baseResp.AppName = app.Name
		baseResp.SetupPolicy = effectivePolicy
		baseResp.Status = "already_automatic"
		baseResp.Message = "StackKit configures this platform service during rollout."
		return baseResp, http.StatusOK, nil
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
		SetupPolicy:      effectivePolicy,
		SetupActionLabel: service.SetupActionLabel,
		Mode:             mode,
		Status:           "ready",
		Message:          "Setup is available for this service.",
		Drops:            setupDropResponses(app.SetupDrops, "ready"),
	}

	if mode != setupActionModeApply {
		resp.Message = "Setup was validated but not executed because setup actions are in dry-run mode."
		return resp, http.StatusAccepted, nil
	}

	completed := make([]setupDropResponse, 0, len(app.SetupDrops))
	waiting := false
	for _, drop := range app.SetupDrops {
		run, alreadyComplete, state, statePath, stateErr := s.startSetupRun(service, app, drop)
		if stateErr != nil {
			return serviceSetupResponse{}, setupHTTPStatus(stateErr), stateErr
		}
		if alreadyComplete {
			completed = append(completed, setupDropResponseFromRun(drop, run))
			continue
		}
		evidence, err := s.runSetupDrop(ctx, service, app, drop)
		if err != nil {
			if isSetupWaitingForOwnerActivation(err) {
				waiting = true
				run.Evidence = ownerActivationWaitingEvidence(service.Key, app.Name, drop.Name)
				run, stateErr = s.finishSetupRun(state, statePath, run, models.SetupRunStatusWaiting, models.BootstrapPhaseOwnerActivated, "Setup drop is waiting for PocketID Owner activation.", nil)
				if stateErr != nil {
					return serviceSetupResponse{}, setupHTTPStatus(stateErr), stateErr
				}
				completed = append(completed, setupDropResponseFromRun(drop, run))
				continue
			}
			if _, stateErr := s.finishSetupRun(state, statePath, run, models.SetupRunStatusFailed, models.BootstrapPhaseConfigured, "Setup drop failed.", err); stateErr != nil {
				return serviceSetupResponse{}, setupHTTPStatus(stateErr), stateErr
			}
			return serviceSetupResponse{}, setupHTTPStatus(err), err
		}
		run.Evidence = evidence
		run, stateErr = s.finishSetupRun(state, statePath, run, models.SetupRunStatusCompleted, models.BootstrapPhaseVerified, "Setup drop completed.", nil)
		if stateErr != nil {
			return serviceSetupResponse{}, setupHTTPStatus(stateErr), stateErr
		}
		completed = append(completed, setupDropResponseFromRun(drop, run))
	}

	if waiting {
		resp.Status = "waiting"
		resp.Message = "Setup is waiting for PocketID Owner activation."
		resp.Drops = completed
		return resp, http.StatusAccepted, nil
	}

	resp.Status = "completed"
	resp.Message = "Setup completed."
	resp.Drops = completed
	return resp, http.StatusOK, nil
}

func isSetupWaitingForOwnerActivation(err *skerrors.StackKitError) bool {
	return err != nil && (err.Code == "immich_pocketid_owner_missing" || err.Code == "vaultwarden_pocketid_owner_missing")
}

func ownerActivationWaitingEvidence(serviceKey, appName, dropName string) map[string]string {
	evidence := map[string]string{
		"ownerProvisioning":      "waiting-pocketid-owner",
		"ownerLogin":             initialAccessOwnerLogin,
		"appLocalSessionHandoff": "waiting-owner-activation",
	}
	if serviceKey == "photos" && appName == "immich" && dropName == "immich-owner-bootstrap" {
		evidence["credentialRole"] = "technical-admin-bootstrap"
		evidence["technicalAdmin"] = "stackkit-admin-created"
		evidence["appLocalOwner"] = "waiting-pocketid-owner"
		evidence["outerAuthBoundary"] = "tinyauth-pocketid"
		evidence["pocketidOAuth"] = "prepared"
		evidence["oidcClientId"] = immichPocketIDClientID
		evidence["autoRegister"] = "false"
		evidence["autoLaunch"] = "true"
	}
	if serviceKey == "vault" && appName == "vaultwarden" && dropName == "vaultwarden-admin-handoff" {
		evidence["credentialRole"] = "break-glass-admin-token"
		evidence["adminTokenPosture"] = "verified-break-glass"
		evidence["adminTokenStorage"] = "argon2id-phc-runtime"
		evidence["appLocalSignups"] = "disabled"
		evidence["plaintextAdminTokenEnv"] = "absent"
		evidence["outerAuthBoundary"] = "tinyauth-pocketid"
		evidence["appLocalOwner"] = "waiting-pocketid-owner"
		evidence["readyToUseContentStatus"] = "waiting-owner-activation"
	}
	return evidence
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
		dynamicProtected:  dynamicExists && baseHubDynamicConfigProtected(dynamicData),
		dynamicExists:     dynamicExists,
		networkMode:       stringTFVar(tfvars, "network_mode", "bridge"),
	}, nil
}

func (s *Server) loadInitialAccessState() (initialAccessState, *skerrors.StackKitError) {
	baseState, err := s.loadBaseHubProtectionState()
	if err != nil {
		return initialAccessState{}, err
	}
	markerPath := initialAccessMarkerPath(s.config.BaseDir)
	_, markerErr := os.Stat(markerPath)
	consumed := markerErr == nil
	if markerErr != nil && !os.IsNotExist(markerErr) {
		return initialAccessState{}, skerrors.NewDeploymentError(
			"initial_access_marker_state_failed",
			"failed to read initial access reveal state",
			skerrors.WithField("path", markerPath),
			skerrors.WithCause(markerErr),
		)
	}
	return initialAccessState{
		protected:     baseState.effectiveProtected(),
		consumed:      consumed,
		markerPath:    markerPath,
		adminEmail:    firstNonEmptyString(s.config.SetupAdminEmail, stringTFVar(baseState.tfvars, "admin_email", "")),
		adminPassword: firstNonEmptyString(s.config.SetupAdminPassword, stringTFVar(baseState.tfvars, "admin_password_plaintext", "")),
		selectedPaaS:  strings.ToLower(stringTFVar(baseState.tfvars, "paas", "coolify")),
	}, nil
}

func (s initialAccessState) toResponse(credentials *initialAccessCredentials) initialAccessResponse {
	available := s.protected && !s.consumed && s.adminEmail != "" && s.adminPassword != ""
	status := "locked"
	switch {
	case s.consumed:
		status = "consumed"
	case available:
		status = "available"
	case s.adminEmail == "" || s.adminPassword == "":
		status = "unavailable"
	}
	return initialAccessResponse{
		Status:             status,
		Mode:               "one-time",
		Protected:          s.protected,
		Available:          available,
		Consumed:           s.consumed,
		CredentialRole:     initialAccessCredentialRole,
		OwnerLogin:         initialAccessOwnerLogin,
		CredentialBoundary: initialAccessCredentialBoundary,
		Message:            initialAccessMessage(status),
		Credentials:        credentials,
	}
}

func (s initialAccessState) credentials() *initialAccessCredentials {
	return &initialAccessCredentials{
		AdminEmail:         s.adminEmail,
		AdminPassword:      s.adminPassword,
		SelectedPaaS:       selectedPaaSLabel(s.selectedPaaS),
		CredentialRole:     initialAccessCredentialRole,
		OwnerLogin:         initialAccessOwnerLogin,
		CredentialBoundary: initialAccessCredentialBoundary,
		IntendedFor:        []string{"TinyAuth gateway technical admin", selectedPaaSLabel(s.selectedPaaS) + " technical admin"},
	}
}

func initialAccessMessage(status string) string {
	switch status {
	case "available":
		return "Technical bootstrap credentials are ready to reveal once."
	case "consumed":
		return "Technical bootstrap credentials were already revealed once. Use the terminal output, recovery bundle, or wallet copy."
	case "unavailable":
		return "Technical bootstrap credentials are not available on this node."
	default:
		return "Protect Base Hub first, then log in through TinyAuth to reveal technical bootstrap credentials once."
	}
}

func selectedPaaSLabel(paas string) string {
	switch strings.ToLower(strings.TrimSpace(paas)) {
	case "komodo":
		return "Komodo"
	case "dokploy":
		return "Dokploy"
	default:
		return "Coolify"
	}
}

func initialAccessMarkerPath(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	return filepath.Join(baseDir, ".stackkit", "initial-access.revealed.json")
}

func writeInitialAccessMarker(path string, credentials *initialAccessCredentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	marker := map[string]any{
		"revealedAt":         time.Now().UTC().Format(time.RFC3339),
		"adminEmail":         credentials.AdminEmail,
		"selectedPaaS":       credentials.SelectedPaaS,
		"credentialRole":     credentials.CredentialRole,
		"ownerLogin":         credentials.OwnerLogin,
		"credentialBoundary": credentials.CredentialBoundary,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
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
          - "remote-user"
          - "remote-sub"
          - "remote-name"
          - "remote-email"
          - "remote-groups"
`, address)
}

func baseHubDynamicConfigProtected(data []byte) bool {
	lines := strings.Split(string(data), "\n")
	inBaseHubAuth := false
	baseHubAuthIndent := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingWhitespaceLen(line)
		if inBaseHubAuth {
			if indent <= baseHubAuthIndent && strings.HasSuffix(trimmed, ":") {
				inBaseHubAuth = false
			} else if strings.TrimSuffix(trimmed, ":") == "forwardAuth" {
				return true
			}
		}
		if strings.TrimSuffix(trimmed, ":") == "base-hub-auth" {
			inBaseHubAuth = true
			baseHubAuthIndent = indent
		}
	}
	return false
}

func leadingWhitespaceLen(s string) int {
	for i, r := range s {
		if r != ' ' && r != '\t' {
			return i
		}
	}
	return len(s)
}

func findSetupApp(bundle platformdeploy.BundleManifest, service servicecatalog.Service) (platformdeploy.AppManifest, bool) {
	candidates := setupAppCandidates(service)
	for _, app := range bundle.Apps {
		if setupAppNameMatches(app.Name, candidates) || setupAppNameMatches(app.ServiceKey, candidates) {
			return app, true
		}
	}
	for _, systemApp := range bundle.SystemApps {
		app := systemApp.AppManifest
		if setupAppNameMatches(app.Name, candidates) || setupAppNameMatches(app.ServiceKey, candidates) {
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
			Name:          drop.Name,
			Version:       drop.Version,
			Runner:        drop.Runner,
			Description:   drop.Description,
			Status:        status,
			RollbackNotes: drop.RollbackNotes,
		})
	}
	return out
}

func setupDropResponseFromRun(drop platformdeploy.SetupDropManifest, run models.SetupRunState) setupDropResponse {
	status := run.Status
	if status == "" {
		status = "ready"
	}
	rollbackNotes := run.RollbackNotes
	if len(rollbackNotes) == 0 {
		rollbackNotes = drop.RollbackNotes
	}
	return setupDropResponse{
		Name:          drop.Name,
		RunID:         run.RunID,
		Version:       drop.Version,
		Runner:        drop.Runner,
		Description:   drop.Description,
		Status:        status,
		Phase:         run.Phase,
		Attempts:      run.Attempts,
		Message:       run.Message,
		Error:         run.Error,
		FailureClass:  run.FailureClass,
		Evidence:      run.Evidence,
		Logs:          run.Logs,
		RollbackNotes: rollbackNotes,
		LastRequested: run.LastRequested,
		LastStarted:   run.LastStarted,
		LastFinished:  run.LastFinished,
	}
}

func (s *Server) setupActionMode() string {
	switch strings.ToLower(strings.TrimSpace(s.config.SetupActionMode)) {
	case setupActionModeApply:
		return setupActionModeApply
	default:
		return setupActionModeDryRun
	}
}

func (s *Server) startSetupRun(service servicecatalog.Service, app platformdeploy.AppManifest, drop platformdeploy.SetupDropManifest) (models.SetupRunState, bool, *models.DeploymentState, string, *skerrors.StackKitError) {
	state, statePath, err := s.loadSetupRunState()
	if err != nil {
		return models.SetupRunState{}, false, nil, "", err
	}
	now := time.Now().UTC()
	idx := findSetupRunIndex(state.SetupRuns, service.Key, app.Name, drop.Name)
	if idx >= 0 && state.SetupRuns[idx].Status == models.SetupRunStatusCompleted {
		run := state.SetupRuns[idx]
		run.LastRequested = now
		run.Phase = models.BootstrapPhaseVerified
		run.Message = "Setup drop already completed; re-run is idempotent."
		if len(run.RollbackNotes) == 0 {
			run.RollbackNotes = drop.RollbackNotes
		}
		run = appendSetupRunLog(run, models.BootstrapPhaseVerified, "info", "Setup drop already completed; runner skipped.", now)
		state.SetupRuns[idx] = run
		if err := s.saveSetupRunState(state, statePath); err != nil {
			return models.SetupRunState{}, false, nil, "", err
		}
		return run, true, state, statePath, nil
	}

	run := models.SetupRunState{}
	if idx >= 0 {
		run = state.SetupRuns[idx]
	}
	if run.RunID == "" {
		run.RunID = uuid.NewString()
	}
	run.ServiceKey = service.Key
	run.AppName = app.Name
	run.DropName = drop.Name
	run.Policy = app.SetupPolicy
	run.Status = models.SetupRunStatusRunning
	run.Phase = models.BootstrapPhasePrepared
	run.Attempts++
	run.Message = "Setup drop started."
	run.Error = ""
	run.FailureClass = ""
	run.Evidence = nil
	run.RollbackNotes = drop.RollbackNotes
	if run.Attempts <= 1 {
		run.Logs = nil
	}
	run = appendSetupRunLog(run, models.BootstrapPhaseDesired, "info", "Setup drop requested.", now)
	run = appendSetupRunLog(run, models.BootstrapPhasePrepared, "info", "Setup drop prepared for execution.", now)
	run.LastRequested = now
	run.LastStarted = now
	if idx >= 0 {
		state.SetupRuns[idx] = run
	} else {
		state.SetupRuns = append(state.SetupRuns, run)
	}
	if err := s.saveSetupRunState(state, statePath); err != nil {
		return models.SetupRunState{}, false, nil, "", err
	}
	return run, false, state, statePath, nil
}

func (s *Server) finishSetupRun(state *models.DeploymentState, statePath string, run models.SetupRunState, status, phase, message string, failure *skerrors.StackKitError) (models.SetupRunState, *skerrors.StackKitError) {
	if state == nil {
		return models.SetupRunState{}, skerrors.NewDeploymentError("setup_state_missing", "setup state is unavailable")
	}
	idx := findSetupRunIndex(state.SetupRuns, run.ServiceKey, run.AppName, run.DropName)
	if idx < 0 {
		state.SetupRuns = append(state.SetupRuns, run)
		idx = len(state.SetupRuns) - 1
	}
	next := state.SetupRuns[idx]
	if next.RunID == "" {
		next.RunID = run.RunID
	}
	next.ServiceKey = run.ServiceKey
	next.AppName = run.AppName
	next.DropName = run.DropName
	next.Policy = run.Policy
	next.Status = status
	next.Phase = phase
	next.Message = message
	next.LastFinished = time.Now().UTC()
	if failure != nil {
		next.Error = failure.Message
		next.FailureClass = setupFailureClass(failure)
		next.Evidence = nil
		next = appendSetupRunLog(next, phase, "error", failure.Message, next.LastFinished)
	} else {
		next.Error = ""
		next.FailureClass = ""
		next.Evidence = cloneStringMap(run.Evidence)
		next = appendSetupRunLog(next, phase, "info", message, next.LastFinished)
	}
	state.SetupRuns[idx] = next
	if err := s.saveSetupRunState(state, statePath); err != nil {
		return models.SetupRunState{}, err
	}
	return next, nil
}

func appendSetupRunLog(run models.SetupRunState, phase, level, message string, timestamp time.Time) models.SetupRunState {
	if strings.TrimSpace(message) == "" {
		return run
	}
	run.Logs = append(run.Logs, models.SetupRunLogEntry{
		Timestamp: timestamp,
		Phase:     phase,
		Level:     level,
		Message:   message,
	})
	return run
}

func setupFailureClass(err *skerrors.StackKitError) string {
	if err == nil {
		return ""
	}
	if strings.TrimSpace(err.Code) != "" {
		return err.Code
	}
	if strings.TrimSpace(string(err.Category)) != "" {
		return string(err.Category)
	}
	return "setup_failed"
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (s *Server) loadSetupRunState() (*models.DeploymentState, string, *skerrors.StackKitError) {
	baseDir := strings.TrimSpace(s.config.BaseDir)
	if baseDir == "" {
		baseDir = "."
	}
	statePath := filepath.Join(baseDir, ".stackkit", "state.yaml")
	loader := config.NewLoader(baseDir)
	state, err := loader.LoadDeploymentState(filepath.Join(".stackkit", "state.yaml"))
	if err != nil {
		return nil, statePath, skerrors.NewDeploymentError(
			"setup_state_load_failed",
			"failed to load setup run state",
			skerrors.WithField("path", statePath),
			skerrors.WithCause(err),
		)
	}
	if state == nil {
		state = &models.DeploymentState{}
	}
	return state, statePath, nil
}

func (s *Server) saveSetupRunState(state *models.DeploymentState, statePath string) *skerrors.StackKitError {
	baseDir := strings.TrimSpace(s.config.BaseDir)
	if baseDir == "" {
		baseDir = "."
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0750); err != nil {
		return skerrors.NewDeploymentError(
			"setup_state_dir_failed",
			"failed to prepare setup run state directory",
			skerrors.WithField("path", filepath.Dir(statePath)),
			skerrors.WithCause(err),
		)
	}
	loader := config.NewLoader(baseDir)
	if err := loader.SaveDeploymentState(state, filepath.Join(".stackkit", "state.yaml")); err != nil {
		return skerrors.NewDeploymentError(
			"setup_state_save_failed",
			"failed to persist setup run state",
			skerrors.WithField("path", statePath),
			skerrors.WithCause(err),
		)
	}
	return nil
}

func findSetupRunIndex(runs []models.SetupRunState, serviceKey, appName, dropName string) int {
	for i, run := range runs {
		if (run.ServiceKey == serviceKey || run.ServiceKey == "") && run.AppName == appName && run.DropName == dropName {
			return i
		}
	}
	return -1
}

func (s *Server) runSetupDrop(ctx context.Context, service servicecatalog.Service, app platformdeploy.AppManifest, drop platformdeploy.SetupDropManifest) (map[string]string, *skerrors.StackKitError) {
	switch drop.Name {
	case "cloudreve-owner-bootstrap":
		return s.runCloudreveOwnerBootstrap(ctx)
	case "immich-owner-bootstrap":
		return s.runImmichOwnerBootstrap(ctx)
	case "vaultwarden-admin-handoff":
		return s.runVaultwardenAdminHandoff(ctx, app)
	default:
		return nil, skerrors.NewValidationError(
			"setup_runner_not_implemented",
			"this setup drop does not have a node-local runner yet",
			skerrors.WithField("service", service.Key),
			skerrors.WithField("app", app.Name),
			skerrors.WithField("drop", drop.Name),
			skerrors.WithField("runner", drop.Runner),
		)
	}
}

func (s *Server) runCloudreveOwnerBootstrap(ctx context.Context) (map[string]string, *skerrors.StackKitError) {
	owner, ownerErr := s.resolvePocketIDOwner(ctx)
	if ownerErr != nil {
		return nil, ownerErr
	}
	ownerEmail := strings.TrimSpace(owner.Email)
	if ownerEmail == "" {
		return nil, skerrors.NewValidationError(
			"files_pocketid_owner_missing",
			"Cloudreve Owner bootstrap requires an activated PocketID Owner user",
			skerrors.WithSuggestion("Create the PocketID Owner/passkey first, then re-run the Files setup action"),
		)
	}

	login, userID, bridgeErr := s.prepareCloudreveOwnerSession(ctx, ownerEmail)
	if bridgeErr != nil {
		return nil, bridgeErr
	}
	var parsed cloudreveLoginResponse
	if err := json.Unmarshal(login, &parsed); err != nil {
		return nil, skerrors.NewDependencyError(
			"files_cloudreve_session_parse_failed",
			"Cloudreve Owner bootstrap could not parse the prepared session response",
			skerrors.WithCause(err),
		)
	}
	token := strings.TrimSpace(parsed.Token.AccessToken)
	if token == "" {
		return nil, skerrors.NewDependencyError(
			"files_cloudreve_session_token_missing",
			"Cloudreve Owner bootstrap did not receive a usable session token",
		)
	}
	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupCloudreveURL, "http://cloudreve:5212"), "/")
	client := &http.Client{Timeout: 20 * time.Second}
	if err := ensureCloudreveOwnerDemoContent(ctx, client, baseURL, token); err != nil {
		return nil, cloudreveStackKitError("files_cloudreve_owner_demo_seed_failed", "failed to seed Cloudreve demo content for the PocketID Owner", err)
	}

	return map[string]string{
		"credentialRole":          "technical-admin-bootstrap",
		"appLocalAccount":         "stackkit-admin-created",
		"demoData":                "seeded-when-enabled",
		"outerAuthBoundary":       "tinyauth-pocketid",
		"ownerLogin":              initialAccessOwnerLogin,
		"ownerEmail":              ownerEmail,
		"identityBridge":          "stackkit-cloudreve-local-session",
		"appLocalSessionHandoff":  "stackkit-session-bridge-prepared",
		"bridgeVerification":      "stackkit-cloudreve-session-bridge",
		"bridgeCurrentUser":       userID,
		"cloudreveSessionUser":    userID,
		"seededFolder":            cloudreveDemoFolderName,
		"seededFile":              cloudreveDemoFileName,
		"readyToUseContentStatus": "pending-browser-evidence",
	}, nil
}

func (s *Server) runVaultwardenAdminHandoff(ctx context.Context, app platformdeploy.AppManifest) (map[string]string, *skerrors.StackKitError) {
	_, tfvars, err := loadBaseHubTFVars(s.config.BaseDir)
	if err != nil {
		return nil, skerrors.NewValidationError(
			"vaultwarden_tfvars_unavailable",
			"Vaultwarden admin handoff requires generated StackKit inputs",
			skerrors.WithCause(err),
			skerrors.WithSuggestion("Run stackkit generate/apply so deploy/terraform.tfvars.json is present in the node workspace"),
		)
	}
	if !boolTFVar(tfvars, "enable_vaultwarden", false) {
		return nil, skerrors.NewValidationError(
			"vaultwarden_disabled",
			"Vaultwarden is not enabled in this StackKit deployment",
			skerrors.WithSuggestion("Enable Vaultwarden and re-apply before running its bootstrap action"),
		)
	}
	token := strings.TrimSpace(stringTFVar(tfvars, "vaultwarden_admin_token", ""))
	if token == "" {
		return nil, skerrors.NewValidationError(
			"vaultwarden_admin_token_missing",
			"Vaultwarden admin token is not present in generated StackKit inputs",
			skerrors.WithSuggestion("Re-run stackkit generate so the Vaultwarden admin token can be generated and persisted"),
		)
	}
	if !strings.HasPrefix(strings.TrimSpace(stringTFVar(tfvars, "vaultwarden_admin_token_phc", "")), "$argon2id$") {
		return nil, skerrors.NewValidationError(
			"vaultwarden_admin_token_phc_missing",
			"Vaultwarden admin token handoff requires generated Argon2id PHC runtime material",
			skerrors.WithSuggestion("Re-run stackkit generate so the Vaultwarden ADMIN_TOKEN is stored as a PHC hash in the runtime container"),
		)
	}
	if err := verifyVaultwardenHandoffCompose(app.ComposeYAML); err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupVaultwardenURL, "http://vaultwarden:80"), "/")
	client := &http.Client{Timeout: 20 * time.Second}
	if err := vaultwardenAdminHealth(ctx, client, baseURL); err != nil {
		return nil, err
	}
	adminCookies, loginErr := vaultwardenAdminLogin(ctx, client, baseURL, token)
	if loginErr != nil {
		return nil, loginErr
	}
	evidence := map[string]string{
		"credentialRole":         "break-glass-admin-token",
		"ownerLogin":             initialAccessOwnerLogin,
		"adminTokenPosture":      "verified-break-glass",
		"adminTokenStorage":      "argon2id-phc-runtime",
		"appLocalSignups":        "disabled",
		"plaintextAdminTokenEnv": "absent",
		"outerAuthBoundary":      "tinyauth-pocketid",
	}
	owner, ownerErr := s.resolveVaultwardenPocketIDOwner(ctx)
	if ownerErr != nil {
		return nil, ownerErr
	}
	ownerEvidence, inviteErr := vaultwardenInvitePocketIDOwner(ctx, client, baseURL, adminCookies, owner)
	if inviteErr != nil {
		return nil, inviteErr
	}
	for key, value := range ownerEvidence {
		evidence[key] = value
	}
	return evidence, nil
}

func verifyVaultwardenHandoffCompose(composeYAML string) *skerrors.StackKitError {
	compose := strings.TrimSpace(composeYAML)
	if compose == "" {
		return skerrors.NewValidationError(
			"vaultwarden_compose_missing",
			"Vaultwarden admin handoff requires generated compose evidence",
			skerrors.WithSuggestion("Re-run stackkit generate/apply so the Vaultwarden platform manifest includes the generated compose bundle"),
		)
	}
	if !strings.Contains(compose, "ADMIN_TOKEN_B64:") {
		return skerrors.NewValidationError(
			"vaultwarden_admin_token_b64_missing",
			"Vaultwarden compose must pass the generated PHC admin token through ADMIN_TOKEN_B64",
			skerrors.WithSuggestion("Re-run stackkit generate so Vaultwarden uses the PHC+B64 runtime token transport"),
		)
	}
	if !strings.Contains(compose, "SIGNUPS_ALLOWED: \"false\"") && !strings.Contains(compose, "SIGNUPS_ALLOWED: 'false'") && !strings.Contains(compose, "SIGNUPS_ALLOWED: false") {
		return skerrors.NewValidationError(
			"vaultwarden_signups_not_disabled",
			"Vaultwarden compose must disable app-local public signups for the BaseKit default",
			skerrors.WithSuggestion("Set Vaultwarden public signups to false and keep Owner access behind TinyAuth/PocketID"),
		)
	}
	for _, line := range strings.Split(compose, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "ADMIN_TOKEN:") {
			return skerrors.NewValidationError(
				"vaultwarden_plaintext_admin_token_env",
				"Vaultwarden compose must not persist plaintext ADMIN_TOKEN as an environment value",
				skerrors.WithSuggestion("Use ADMIN_TOKEN_B64 with the generated Argon2id PHC token and decode it only inside the container start command"),
			)
		}
	}
	return nil
}

func vaultwardenAdminHealth(ctx context.Context, client *http.Client, baseURL string) *skerrors.StackKitError {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/alive", nil)
	if err != nil {
		return skerrors.NewValidationError("vaultwarden_admin_url_invalid", "Vaultwarden setup URL is invalid", skerrors.WithCause(err))
	}
	resp, err := client.Do(req)
	if err != nil {
		return skerrors.NewDependencyError("vaultwarden_admin_unreachable", "failed to reach Vaultwarden health endpoint", skerrors.WithCause(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return skerrors.NewDependencyError(
			"vaultwarden_admin_unhealthy",
			"Vaultwarden health endpoint is not ready",
			skerrors.WithField("status", resp.StatusCode),
		)
	}
	return nil
}

func vaultwardenAdminLogin(ctx context.Context, client *http.Client, baseURL, token string) ([]*http.Cookie, *skerrors.StackKitError) {
	form := url.Values{"token": []string{token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/admin", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, skerrors.NewValidationError("vaultwarden_admin_url_invalid", "Vaultwarden admin URL is invalid", skerrors.WithCause(err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, skerrors.NewDependencyError("vaultwarden_admin_login_unreachable", "failed to reach Vaultwarden admin login", skerrors.WithCause(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, skerrors.NewAuthError(
			"vaultwarden_admin_login_failed",
			"Vaultwarden rejected the generated admin token",
			skerrors.WithField("status", resp.StatusCode),
			skerrors.WithSuggestion("Rotate the generated Vaultwarden admin token and re-run the setup action"),
		)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "VW_ADMIN" && strings.TrimSpace(cookie.Value) != "" {
			return resp.Cookies(), nil
		}
	}
	if !strings.Contains(string(body), "Vaultwarden Admin Panel") {
		return nil, skerrors.NewDependencyError(
			"vaultwarden_admin_login_unverified",
			"Vaultwarden admin login completed without a verifiable admin session",
			skerrors.WithField("status", resp.StatusCode),
		)
	}
	return resp.Cookies(), nil
}

func (s *Server) resolveVaultwardenPocketIDOwner(ctx context.Context) (pocketIDUser, *skerrors.StackKitError) {
	owner, err := s.resolvePocketIDOwner(ctx)
	if err == nil {
		return owner, nil
	}
	if err.Code == "immich_pocketid_owner_missing" {
		return pocketIDUser{}, skerrors.NewValidationError(
			"vaultwarden_pocketid_owner_missing",
			"Vaultwarden Owner invite requires an activated PocketID Owner user",
			skerrors.WithSuggestion("Create the PocketID Owner/passkey first, then re-run the Vault setup action"),
		)
	}
	return owner, err
}

func vaultwardenInvitePocketIDOwner(ctx context.Context, client *http.Client, baseURL string, cookies []*http.Cookie, owner pocketIDUser) (map[string]string, *skerrors.StackKitError) {
	ownerEmail := strings.TrimSpace(owner.Email)
	if ownerEmail == "" {
		return map[string]string{
			"appLocalOwner":           "technical-admin-bootstrap-only",
			"ownerProvisioning":       "skipped-pocketid-disabled",
			"appLocalSessionHandoff":  "not-configured-pocketid-disabled",
			"readyToUseContentStatus": "not-configured-pocketid-disabled",
		}, nil
	}
	payload, err := json.Marshal(map[string]string{"email": ownerEmail})
	if err != nil {
		return nil, skerrors.NewValidationError("vaultwarden_owner_invite_payload_failed", "failed to prepare Vaultwarden Owner invite payload", skerrors.WithCause(err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/admin/invite", bytes.NewReader(payload))
	if err != nil {
		return nil, skerrors.NewValidationError("vaultwarden_owner_invite_url_invalid", "Vaultwarden invite URL is invalid", skerrors.WithCause(err))
	}
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, skerrors.NewDependencyError("vaultwarden_owner_invite_unreachable", "failed to reach Vaultwarden Owner invite endpoint", skerrors.WithCause(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	var inviteStatus string
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		inviteStatus = "created"
	case resp.StatusCode == http.StatusConflict:
		inviteStatus = "already-exists"
	default:
		return nil, skerrors.NewDependencyError(
			"vaultwarden_owner_invite_failed",
			"Vaultwarden rejected the PocketID Owner invite",
			skerrors.WithField("status", resp.StatusCode),
			skerrors.WithField("body", truncateForField(string(body))),
			skerrors.WithSuggestion("Check the generated Vaultwarden admin token and invite settings, then retry the Vault setup action"),
		)
	}
	evidence := map[string]string{
		"appLocalOwner":           "pocketid-owner-preprovisioned",
		"ownerEmail":              ownerEmail,
		"ownerProvisioning":       "vaultwarden-admin-invite-" + inviteStatus,
		"appLocalSessionHandoff":  "vaultwarden-invite-prepared",
		"readyToUseContentStatus": "owner-completes-vaultwarden-invite",
		"vaultwardenInvite":       inviteStatus,
	}
	if id := strings.TrimSpace(owner.ID); id != "" {
		evidence["pocketidOwnerId"] = id
	}
	return evidence, nil
}

func (s *Server) runImmichOwnerBootstrap(ctx context.Context) (map[string]string, *skerrors.StackKitError) {
	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupImmichURL, "http://immich:2283"), "/")
	email := strings.TrimSpace(s.config.SetupAdminEmail)
	password := strings.TrimSpace(s.config.SetupAdminPassword)
	if email == "" || password == "" {
		return nil, skerrors.NewValidationError(
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
		return nil, skerrors.NewDependencyError("immich_config_failed", "failed to read Immich server config", skerrors.WithCause(err))
	}
	if !config.IsInitialized {
		payload := map[string]string{
			"email":    email,
			"password": password,
			"name":     "StackKit Admin",
		}
		if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/auth/admin-sign-up", payload, "", nil); err != nil {
			return nil, skerrors.NewDependencyError("immich_admin_signup_failed", "failed to create Immich owner", skerrors.WithCause(err))
		}
	}

	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/auth/login", map[string]string{
		"email":    email,
		"password": password,
	}, "", &login); err != nil {
		return nil, skerrors.NewAuthError("immich_login_failed", "failed to log in to Immich with StackKit admin credentials", skerrors.WithCause(err))
	}
	if strings.TrimSpace(login.AccessToken) == "" {
		return nil, skerrors.NewAuthError("immich_login_missing_token", "Immich login did not return an access token")
	}

	token := login.AccessToken
	name := "StackKit Admin"
	if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/users/me", map[string]string{
		"name":     name,
		"password": password,
	}, token, nil); err != nil {
		return nil, skerrors.NewDependencyError("immich_profile_update_failed", "failed to update Immich owner profile", skerrors.WithCause(err))
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/users/me/onboarding", map[string]bool{
		"isOnboarded": true,
	}, token, nil); err != nil {
		return nil, skerrors.NewDependencyError("immich_user_onboarding_failed", "failed to complete Immich user onboarding", skerrors.WithCause(err))
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/system-metadata/admin-onboarding", map[string]bool{
		"isOnboarded": true,
	}, token, nil); err != nil {
		return nil, skerrors.NewDependencyError("immich_admin_onboarding_failed", "failed to complete Immich admin onboarding", skerrors.WithCause(err))
	}

	evidence, err := s.configureImmichPocketIDOAuth(ctx, client, baseURL, token)
	if err != nil {
		return nil, err
	}
	owner, ownerErr := s.resolvePocketIDOwner(ctx)
	if ownerErr != nil {
		return nil, ownerErr
	}
	ownerToken, ownerEvidence, ownerSetupErr := ensureImmichPocketIDOwner(ctx, client, baseURL, token, email, owner)
	if ownerSetupErr != nil {
		return nil, ownerSetupErr
	}
	if immichDemoDataEnabled(s.config.BaseDir) {
		if err := seedImmichDemoData(ctx, client, baseURL, ownerToken); err != nil {
			return nil, skerrors.NewDependencyError("immich_demo_seed_failed", "failed to seed Immich beta demo photo", skerrors.WithCause(err))
		}
	}

	for key, value := range ownerEvidence {
		evidence[key] = value
	}
	evidence["credentialRole"] = "technical-admin-bootstrap"
	evidence["technicalAdmin"] = "stackkit-admin-created"
	if evidence["appLocalOwner"] == "" {
		evidence["appLocalOwner"] = "technical-admin-bootstrap-only"
	}
	evidence["demoData"] = "seeded-when-enabled"
	evidence["ownerLogin"] = initialAccessOwnerLogin
	return evidence, nil
}

func immichDemoDataEnabled(baseDir string) bool {
	_, tfvars, err := loadBaseHubTFVars(baseDir)
	if err != nil {
		return true
	}
	return boolTFVar(tfvars, "demo_data_enabled", true)
}

func (s *Server) resolvePocketIDOwner(ctx context.Context) (pocketIDUser, *skerrors.StackKitError) {
	_, tfvars, err := loadBaseHubTFVars(s.config.BaseDir)
	if err != nil || !boolTFVar(tfvars, "enable_pocketid", false) {
		return pocketIDUser{}, nil
	}

	staticAPIKey := strings.TrimSpace(stringTFVar(tfvars, "pocketid_static_api_key", ""))
	if staticAPIKey == "" {
		return pocketIDUser{}, skerrors.NewValidationError(
			"immich_pocketid_static_api_key_missing",
			"Immich Owner handoff requires the generated PocketID STATIC_API_KEY",
			skerrors.WithSuggestion("Re-run stackkit generate so PocketID static API material is persisted in terraform.tfvars.json"),
		)
	}

	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupPocketIDURL, "http://pocketid:1411"), "/")
	client := &http.Client{Timeout: 20 * time.Second}
	status, body, err := pocketIDJSONRequest(ctx, client, http.MethodGet, baseURL+"/api/users", staticAPIKey, nil, nil)
	if err != nil {
		return pocketIDUser{}, skerrors.NewDependencyError("immich_pocketid_owner_lookup_failed", "failed to look up PocketID users for Immich Owner handoff", skerrors.WithCause(err))
	}
	if status < 200 || status >= 300 {
		return pocketIDUser{}, skerrors.NewDependencyError(
			"immich_pocketid_owner_lookup_unavailable",
			"PocketID rejected the Owner lookup for Immich handoff",
			skerrors.WithField("status", status),
			skerrors.WithField("body", truncateForField(body)),
		)
	}

	users := parsePocketIDUsers(body)
	if owner, ok := selectPocketIDOwner(users); ok {
		return owner, nil
	}
	return pocketIDUser{}, skerrors.NewValidationError(
		"immich_pocketid_owner_missing",
		"Immich Owner handoff requires an activated PocketID Owner user",
		skerrors.WithSuggestion("Create the PocketID Owner/passkey first, then re-run the Photos setup action"),
	)
}

func parsePocketIDUsers(body string) []pocketIDUser {
	var envelope struct {
		Data []pocketIDUser `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err == nil && len(envelope.Data) > 0 {
		return envelope.Data
	}
	var users []pocketIDUser
	if err := json.Unmarshal([]byte(body), &users); err == nil {
		return users
	}
	return nil
}

func selectPocketIDOwner(users []pocketIDUser) (pocketIDUser, bool) {
	for _, user := range users {
		if pocketIDUserUsable(user) && strings.EqualFold(user.Username, "owner") {
			return user, true
		}
	}
	for _, user := range users {
		if pocketIDUserUsable(user) && user.IsAdmin && !strings.HasPrefix(strings.ToLower(user.Username), "static-api-user-") {
			return user, true
		}
	}
	for _, user := range users {
		if pocketIDUserUsable(user) && !strings.HasPrefix(strings.ToLower(user.Username), "static-api-user-") {
			return user, true
		}
	}
	return pocketIDUser{}, false
}

func pocketIDUserUsable(user pocketIDUser) bool {
	return !user.Disabled && strings.TrimSpace(user.Email) != ""
}

func pocketIDOwnerDisplayName(owner pocketIDUser) string {
	if strings.TrimSpace(owner.DisplayName) != "" {
		return strings.TrimSpace(owner.DisplayName)
	}
	name := strings.TrimSpace(strings.TrimSpace(owner.FirstName) + " " + strings.TrimSpace(owner.LastName))
	if name != "" {
		return name
	}
	if strings.TrimSpace(owner.Username) != "" {
		return strings.TrimSpace(owner.Username)
	}
	return strings.TrimSpace(owner.Email)
}

func ensureImmichPocketIDOwner(ctx context.Context, client *http.Client, baseURL, adminToken, technicalAdminEmail string, owner pocketIDUser) (string, map[string]string, *skerrors.StackKitError) {
	ownerEmail := strings.TrimSpace(owner.Email)
	if ownerEmail == "" {
		return adminToken, map[string]string{
			"appLocalOwner":          "technical-admin-bootstrap-only",
			"ownerProvisioning":      "skipped-pocketid-disabled",
			"appLocalSessionHandoff": "not-configured-pocketid-disabled",
		}, nil
	}

	ownerName := pocketIDOwnerDisplayName(owner)
	evidence := map[string]string{
		"appLocalOwner":          "pocketid-owner-preprovisioned",
		"ownerEmail":             ownerEmail,
		"ownerProvisioning":      "pocketid-owner-email-preprovisioned",
		"appLocalSessionHandoff": "oidc-email-link-prepared",
	}
	if strings.TrimSpace(owner.ID) != "" {
		evidence["pocketidOwnerId"] = strings.TrimSpace(owner.ID)
	}

	var users []immichAdminUser
	if err := immichRequest(ctx, client, baseURL, http.MethodGet, "/api/admin/users", nil, adminToken, &users); err != nil {
		return "", nil, skerrors.NewDependencyError("immich_owner_user_list_failed", "failed to list Immich users for PocketID Owner handoff", skerrors.WithCause(err))
	}

	var existing immichAdminUser
	for _, user := range users {
		if strings.EqualFold(strings.TrimSpace(user.Email), ownerEmail) {
			existing = user
			break
		}
	}

	if strings.EqualFold(ownerEmail, strings.TrimSpace(technicalAdminEmail)) {
		evidence["technicalAdminSameAsOwner"] = "true"
		if existing.ID != "" && (!existing.IsAdmin || existing.ShouldChangePassword || existing.Name != ownerName) {
			if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/admin/users/"+url.PathEscape(existing.ID), map[string]any{
				"name":                 ownerName,
				"storageLabel":         "owner",
				"shouldChangePassword": false,
				"isAdmin":              true,
			}, adminToken, nil); err != nil {
				return "", nil, skerrors.NewDependencyError("immich_owner_update_failed", "failed to align Immich Owner admin privileges", skerrors.WithCause(err))
			}
			evidence["ownerProvisioning"] = "existing-technical-admin-promoted"
		}
		if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/users/me/onboarding", map[string]bool{
			"isOnboarded": true,
		}, adminToken, nil); err != nil {
			return "", nil, skerrors.NewDependencyError("immich_owner_onboarding_failed", "failed to complete Immich Owner onboarding", skerrors.WithCause(err))
		}
		return adminToken, evidence, nil
	}

	ownerPassword := generatedImmichOwnerBootstrapPassword()
	payload := map[string]any{
		"email":                ownerEmail,
		"password":             ownerPassword,
		"name":                 ownerName,
		"storageLabel":         "owner",
		"shouldChangePassword": false,
		"notify":               false,
		"isAdmin":              true,
	}
	if existing.ID == "" {
		var created immichAdminUser
		if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/admin/users", payload, adminToken, &created); err != nil {
			return "", nil, skerrors.NewDependencyError("immich_owner_create_failed", "failed to create Immich PocketID Owner user", skerrors.WithCause(err))
		}
		evidence["ownerProvisioning"] = "created-pocketid-owner"
	} else {
		if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/admin/users/"+url.PathEscape(existing.ID), payload, adminToken, nil); err != nil {
			return "", nil, skerrors.NewDependencyError("immich_owner_update_failed", "failed to update Immich PocketID Owner user", skerrors.WithCause(err))
		}
		evidence["ownerProvisioning"] = "updated-pocketid-owner"
		if strings.TrimSpace(existing.OAuthID) != "" {
			evidence["ownerOAuthLink"] = "already-linked"
		}
	}

	var ownerLogin struct {
		AccessToken string `json:"accessToken"`
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/auth/login", map[string]string{
		"email":    ownerEmail,
		"password": ownerPassword,
	}, "", &ownerLogin); err != nil {
		return "", nil, skerrors.NewAuthError("immich_owner_login_failed", "failed to log in to Immich with the generated Owner bootstrap credential", skerrors.WithCause(err))
	}
	if strings.TrimSpace(ownerLogin.AccessToken) == "" {
		return "", nil, skerrors.NewAuthError("immich_owner_login_missing_token", "Immich Owner login did not return an access token")
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/users/me/onboarding", map[string]bool{
		"isOnboarded": true,
	}, ownerLogin.AccessToken, nil); err != nil {
		return "", nil, skerrors.NewDependencyError("immich_owner_onboarding_failed", "failed to complete Immich Owner onboarding", skerrors.WithCause(err))
	}
	return ownerLogin.AccessToken, evidence, nil
}

func generatedImmichOwnerBootstrapPassword() string {
	return "StackKit!" + strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
}

func (s *Server) configureImmichPocketIDOAuth(ctx context.Context, client *http.Client, baseURL, token string) (map[string]string, *skerrors.StackKitError) {
	evidence := map[string]string{
		"outerAuthBoundary": "tinyauth-pocketid",
	}

	_, tfvars, err := loadBaseHubTFVars(s.config.BaseDir)
	if err != nil {
		evidence["pocketidOAuth"] = "skipped-no-stackkit-tfvars"
		return evidence, nil
	}
	if !boolTFVar(tfvars, "enable_pocketid", false) {
		evidence["pocketidOAuth"] = "skipped-pocketid-disabled"
		return evidence, nil
	}

	staticAPIKey := strings.TrimSpace(stringTFVar(tfvars, "pocketid_static_api_key", ""))
	if staticAPIKey == "" {
		return nil, skerrors.NewValidationError(
			"immich_pocketid_static_api_key_missing",
			"Immich PocketID OAuth bootstrap requires the generated PocketID STATIC_API_KEY",
			skerrors.WithSuggestion("Re-run stackkit generate so PocketID static API material is persisted in terraform.tfvars.json"),
		)
	}

	issuerURL := strings.TrimRight(firstNonEmptyString(stringTFVar(tfvars, "pocketid_app_url", ""), serviceURLForDomain(tfvars, "id")), "/")
	photosURL := strings.TrimRight(serviceURLForDomain(tfvars, "photos"), "/")
	if issuerURL == "" || photosURL == "" {
		return nil, skerrors.NewValidationError(
			"immich_oidc_urls_missing",
			"Immich PocketID OAuth bootstrap requires generated PocketID and Photos URLs",
			skerrors.WithSuggestion("Re-run stackkit generate so domain-derived service URLs are present"),
		)
	}

	secret, secretErr := s.ensurePocketIDImmichClient(ctx, staticAPIKey, photosURL)
	if secretErr != nil {
		return nil, secretErr
	}

	var config map[string]any
	if err := immichRequest(ctx, client, baseURL, http.MethodGet, "/api/system-config", nil, token, &config); err != nil {
		return nil, skerrors.NewDependencyError("immich_system_config_read_failed", "failed to read Immich system config for PocketID OAuth", skerrors.WithCause(err))
	}
	oauth, _ := config["oauth"].(map[string]any)
	if oauth == nil {
		oauth = map[string]any{}
	}
	oauth["enabled"] = true
	oauth["issuerUrl"] = issuerURL
	oauth["clientId"] = immichPocketIDClientID
	oauth["clientSecret"] = secret
	oauth["tokenEndpointAuthMethod"] = "client_secret_post"
	oauth["scope"] = "openid email profile"
	oauth["autoRegister"] = false
	oauth["autoLaunch"] = true
	oauth["buttonText"] = "Continue with PocketID"
	config["oauth"] = oauth

	if err := immichRequest(ctx, client, baseURL, http.MethodPut, "/api/system-config", config, token, nil); err != nil {
		return nil, skerrors.NewDependencyError("immich_system_config_write_failed", "failed to configure Immich PocketID OAuth", skerrors.WithCause(err))
	}

	evidence["pocketidOAuth"] = "enabled"
	evidence["oidcClientId"] = immichPocketIDClientID
	evidence["oidcIssuer"] = issuerURL
	evidence["autoRegister"] = "false"
	evidence["autoLaunch"] = "true"
	evidence["appLocalSessionHandoff"] = "oidc-configured"
	return evidence, nil
}

func (s *Server) ensurePocketIDImmichClient(ctx context.Context, staticAPIKey, photosURL string) (string, *skerrors.StackKitError) {
	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupPocketIDURL, "http://pocketid:1411"), "/")
	client := &http.Client{Timeout: 20 * time.Second}
	clientURL := baseURL + "/api/oidc/clients/" + url.PathEscape(immichPocketIDClientID)
	status, body, err := pocketIDJSONRequest(ctx, client, http.MethodGet, clientURL, staticAPIKey, nil, nil)
	if err != nil {
		return "", skerrors.NewDependencyError("immich_pocketid_client_lookup_failed", "failed to look up the PocketID Immich OIDC client", skerrors.WithCause(err))
	}
	if status == http.StatusNotFound {
		payload := map[string]any{
			"id":                       immichPocketIDClientID,
			"name":                     "Immich",
			"callbackURLs":             []string{photosURL + "/auth/login", photosURL + "/user-settings", "app.immich:///oauth-callback"},
			"logoutCallbackURLs":       []string{},
			"isPublic":                 false,
			"pkceEnabled":              false,
			"requiresReauthentication": false,
			"isGroupRestricted":        false,
		}
		status, body, err = pocketIDJSONRequest(ctx, client, http.MethodPost, baseURL+"/api/oidc/clients", staticAPIKey, payload, nil)
		if err != nil {
			return "", skerrors.NewDependencyError("immich_pocketid_client_create_failed", "failed to create the PocketID Immich OIDC client", skerrors.WithCause(err))
		}
	}
	if status < 200 || status >= 300 {
		return "", skerrors.NewDependencyError(
			"immich_pocketid_client_unavailable",
			"PocketID rejected the Immich OIDC client lookup/create request",
			skerrors.WithField("status", status),
			skerrors.WithField("body", truncateForField(body)),
		)
	}

	var secretResp struct {
		Secret string `json:"secret"`
	}
	status, body, err = pocketIDJSONRequest(ctx, client, http.MethodPost, clientURL+"/secret", staticAPIKey, nil, &secretResp)
	if err != nil {
		return "", skerrors.NewDependencyError("immich_pocketid_client_secret_failed", "failed to create the PocketID Immich OIDC client secret", skerrors.WithCause(err))
	}
	if status < 200 || status >= 300 || strings.TrimSpace(secretResp.Secret) == "" {
		return "", skerrors.NewDependencyError(
			"immich_pocketid_client_secret_unavailable",
			"PocketID did not return a usable Immich OIDC client secret",
			skerrors.WithField("status", status),
			skerrors.WithField("body", truncateForField(body)),
		)
	}
	return secretResp.Secret, nil
}

func pocketIDJSONRequest(ctx context.Context, client *http.Client, method, rawURL, apiKey string, payload any, out any) (int, string, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, "", err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return resp.StatusCode, "", readErr
	}
	text := strings.TrimSpace(string(data))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, text, err
		}
	}
	return resp.StatusCode, text, nil
}

func serviceURLForDomain(tfvars map[string]any, slug string) string {
	domain := strings.TrimSpace(stringTFVar(tfvars, "domain", ""))
	if domain == "" {
		return ""
	}
	proto := "http"
	if boolTFVar(tfvars, "enable_https", false) {
		proto = "https"
	}
	return fmt.Sprintf("%s://%s.%s", proto, slug, domain)
}

func truncateForField(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 1000 {
		return value
	}
	return value[:1000] + "...(truncated)"
}

func seedImmichDemoData(ctx context.Context, client *http.Client, baseURL, token string) error {
	var stats struct {
		Total int `json:"total"`
	}
	if err := immichRequest(ctx, client, baseURL, http.MethodPost, "/api/search/statistics", map[string]any{}, token, &stats); err != nil {
		return err
	}
	if stats.Total > 0 {
		return nil
	}
	return uploadImmichDemoPhoto(ctx, client, baseURL, token)
}

func uploadImmichDemoPhoto(ctx context.Context, client *http.Client, baseURL, token string) error {
	image, err := immichDemoPhotoPNG()
	if err != nil {
		return err
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	now := time.Now().UTC().Format(time.RFC3339)
	fields := map[string]string{
		"deviceAssetId":  "stackkit-demo-photo-1",
		"deviceId":       "stackkit-demo",
		"fileCreatedAt":  now,
		"fileModifiedAt": now,
		"isFavorite":     "true",
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile("assetData", "stackkit-demo-photo.png")
	if err != nil {
		return err
	}
	if _, err := part.Write(image); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/assets", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("POST /api/assets returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

func immichDemoPhotoPNG() ([]byte, error) {
	const (
		width  = 640
		height = 360
	)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r := uint8(24 + (x * 88 / width))
			g := uint8(92 + (y * 112 / height))
			b := uint8(150 + ((x + y) * 72 / (width + height)))
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	for y := 56; y < 138; y++ {
		for x := 482; x < 564; x++ {
			dx, dy := x-523, y-97
			if dx*dx+dy*dy <= 41*41 {
				img.SetRGBA(x, y, color.RGBA{R: 255, G: 214, B: 90, A: 255})
			}
		}
	}
	for y := 214; y < height; y++ {
		for x := 0; x < width; x++ {
			if y > 280-(x/7) && y > 180+((x-360)*(x-360))/3600 {
				img.SetRGBA(x, y, color.RGBA{R: 31, G: 112, B: 92, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
