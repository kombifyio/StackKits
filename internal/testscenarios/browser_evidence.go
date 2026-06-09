package testscenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	BrowserEvidenceStatusPass = "pass"
	BrowserEvidenceStatusFail = "fail"

	MaxBrowserCheckDurationSeconds = 15 * 60

	MinBrowserScreenshotWidth  = 320
	MinBrowserScreenshotHeight = 240
)

var RequiredBaseKitBetaBrowserChecks = []string{
	"pocketid-owner-passkey",
	"tinyauth-owner-session",
	"photos-demo-content",
	"files-demo-content",
	"vault-auth-boundary",
}

var RequiredBaseKitBetaSetupDrops = []string{
	"kuma-platform-bootstrap",
	"cloudreve-owner-bootstrap",
	"vaultwarden-admin-handoff",
	"immich-owner-bootstrap",
}

var RequiredBaseKitBetaOwnerSetupServices = []string{
	"photos",
	"files",
	"vault",
}

var RequiredBaseKitBetaOwnerSetupDropsByService = map[string]string{
	"photos": "immich-owner-bootstrap",
	"files":  "cloudreve-owner-bootstrap",
	"vault":  "vaultwarden-admin-handoff",
}

var AllowedBaseKitBetaBrowserFailurePhases = []string{
	"wrapper",
	"command-preflight",
	"browser-preflight",
	"fresh-vm-rollout",
	"setup-state-export",
	"homelab-artifact",
	"browser-capture",
	"manifest-validation",
}

type BrowserEvidence struct {
	ScenarioID     string                 `json:"scenarioId"`
	RunID          string                 `json:"runId,omitempty"`
	Status         string                 `json:"status"`
	GeneratedAt    string                 `json:"generatedAt,omitempty"`
	Error          string                 `json:"error,omitempty"`
	FailurePhase   string                 `json:"failurePhase,omitempty"`
	OwnerEmail     string                 `json:"ownerEmail,omitempty"`
	OwnerUsername  string                 `json:"ownerUsername,omitempty"`
	BrowserChannel string                 `json:"browserChannel,omitempty"`
	BrowserURL     string                 `json:"browserUrl"`
	Checks         []BrowserEvidenceCheck `json:"checks"`
	Screenshots    []BrowserScreenshot    `json:"screenshots"`
	Diagnostics    BrowserDiagnostics     `json:"diagnostics,omitempty"`
}

type BrowserEvidenceCheck struct {
	Name            string            `json:"name"`
	ServiceKey      string            `json:"serviceKey,omitempty"`
	Status          string            `json:"status"`
	URL             string            `json:"url"`
	ExpectedText    string            `json:"expectedText,omitempty"`
	ObservedText    string            `json:"observedText,omitempty"`
	Screenshot      string            `json:"screenshot,omitempty"`
	DurationSeconds int               `json:"durationSeconds,omitempty"`
	Evidence        map[string]string `json:"evidence,omitempty"`
}

type BrowserScreenshot struct {
	Name       string `json:"name"`
	ServiceKey string `json:"serviceKey,omitempty"`
	Path       string `json:"path"`
	URL        string `json:"url,omitempty"`
}

type BrowserDiagnostics struct {
	Browser      *BrowserRuntimeDiagnostics      `json:"browser,omitempty"`
	SetupState   *BrowserSetupStateDiagnostics   `json:"setupState,omitempty"`
	SetupActions []BrowserSetupActionDiagnostics `json:"setupActions,omitempty"`
	Wrapper      *BrowserWrapperDiagnostics      `json:"wrapper,omitempty"`
}

type BrowserRuntimeDiagnostics struct {
	Channel                      string `json:"channel,omitempty"`
	RequestedChannel             string `json:"requestedChannel,omitempty"`
	Headless                     string `json:"headless,omitempty"`
	Viewport                     string `json:"viewport,omitempty"`
	UserAgent                    string `json:"userAgent,omitempty"`
	BrowserVersion               string `json:"browserVersion,omitempty"`
	WebAuthnVirtualAuthenticator string `json:"webAuthnVirtualAuthenticator,omitempty"`
}

type BrowserSetupStateDiagnostics struct {
	Status        string                            `json:"status"`
	Source        string                            `json:"source,omitempty"`
	SourcePath    string                            `json:"sourcePath,omitempty"`
	SetupRunCount string                            `json:"setupRunCount,omitempty"`
	Drops         map[string]BrowserSetupDropStatus `json:"drops,omitempty"`
	Error         string                            `json:"error,omitempty"`
}

type BrowserSetupActionDiagnostics struct {
	Service           string `json:"service"`
	HTTPStatus        string `json:"httpStatus"`
	OK                string `json:"ok"`
	DurationSeconds   string `json:"durationSeconds,omitempty"`
	RunID             string `json:"runId,omitempty"`
	Attempts          string `json:"attempts,omitempty"`
	Status            string `json:"status,omitempty"`
	DropName          string `json:"dropName,omitempty"`
	DropStatus        string `json:"dropStatus,omitempty"`
	DropPhase         string `json:"dropPhase,omitempty"`
	FailureClass      string `json:"failureClass,omitempty"`
	LastRequested     string `json:"lastRequested,omitempty"`
	LastStarted       string `json:"lastStarted,omitempty"`
	LastFinished      string `json:"lastFinished,omitempty"`
	LogCount          string `json:"logCount,omitempty"`
	RollbackNoteCount string `json:"rollbackNoteCount,omitempty"`
	Message           string `json:"message,omitempty"`
}

type BrowserWrapperDiagnostics struct {
	Phase               string                                  `json:"phase"`
	EvidenceRoot        string                                  `json:"evidenceRoot"`
	PreflightReportPath string                                  `json:"preflightReportPath,omitempty"`
	HomelabPath         string                                  `json:"homelabPath,omitempty"`
	NativeCommand       *BrowserWrapperNativeCommandDiagnostics `json:"nativeCommand,omitempty"`
}

type BrowserWrapperNativeCommandDiagnostics struct {
	Name           string   `json:"name"`
	FilePath       string   `json:"filePath"`
	Arguments      []string `json:"arguments,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	FailureClass   string   `json:"failureClass,omitempty"`
	ExitCode       *int     `json:"exitCode,omitempty"`
	HostIssue      string   `json:"hostIssue,omitempty"`
}

type BrowserSetupDropStatus struct {
	RunID             string            `json:"runId,omitempty"`
	Status            string            `json:"status"`
	Phase             string            `json:"phase,omitempty"`
	ServiceKey        string            `json:"serviceKey,omitempty"`
	FailureClass      string            `json:"failureClass,omitempty"`
	Attempts          string            `json:"attempts,omitempty"`
	LastRequested     string            `json:"lastRequested,omitempty"`
	LastStarted       string            `json:"lastStarted,omitempty"`
	LastFinished      string            `json:"lastFinished,omitempty"`
	LogCount          string            `json:"logCount,omitempty"`
	RollbackNoteCount string            `json:"rollbackNoteCount,omitempty"`
	Evidence          map[string]string `json:"evidence,omitempty"`
}

func ValidateBaseKitBetaBrowserEvidence(e BrowserEvidence) error {
	if strings.TrimSpace(e.ScenarioID) != "SK-S1" {
		return fmt.Errorf("browser evidence scenarioId = %q, want SK-S1", e.ScenarioID)
	}
	if strings.TrimSpace(e.RunID) == "" {
		return fmt.Errorf("browser evidence must include runId")
	}
	if strings.TrimSpace(e.Status) != BrowserEvidenceStatusPass {
		return fmt.Errorf("browser evidence status = %q, want pass", e.Status)
	}
	if err := requireRFC3339("browser evidence generatedAt", e.GeneratedAt); err != nil {
		return err
	}
	if strings.TrimSpace(e.OwnerEmail) == "" || !strings.Contains(e.OwnerEmail, "@") {
		return fmt.Errorf("browser evidence ownerEmail must be email-shaped")
	}
	if strings.TrimSpace(e.OwnerUsername) != "" && strings.Contains(e.OwnerUsername, "@") {
		return fmt.Errorf("browser evidence ownerUsername must be a username, not an email address")
	}
	if strings.TrimSpace(e.BrowserChannel) == "" {
		return fmt.Errorf("browser evidence must include browserChannel")
	}
	if err := requireBrowserURL("browserUrl", e.BrowserURL); err != nil {
		return err
	}

	checksByName := map[string]BrowserEvidenceCheck{}
	for _, check := range e.Checks {
		name := strings.TrimSpace(check.Name)
		if name == "" {
			return fmt.Errorf("browser evidence contains a check without name")
		}
		if _, exists := checksByName[name]; exists {
			return fmt.Errorf("browser evidence contains duplicate check %q", name)
		}
		if err := validateBrowserCheck(check, e.OwnerEmail); err != nil {
			return err
		}
		checksByName[name] = check
	}
	for _, required := range RequiredBaseKitBetaBrowserChecks {
		if _, ok := checksByName[required]; !ok {
			return fmt.Errorf("browser evidence missing required check %q", required)
		}
	}

	screenshotsByPath := map[string]bool{}
	for _, screenshot := range e.Screenshots {
		if err := validateBrowserScreenshot(screenshot); err != nil {
			return err
		}
		screenshotsByPath[strings.TrimSpace(screenshot.Path)] = true
		if screenshot.URL != "" {
			if err := requireBrowserURL("screenshot "+screenshot.Name+" url", screenshot.URL); err != nil {
				return err
			}
		}
	}
	for _, check := range checksByName {
		if check.Screenshot == "" {
			return fmt.Errorf("browser evidence check %q must reference a screenshot", check.Name)
		}
		if !screenshotsByPath[check.Screenshot] {
			return fmt.Errorf("browser evidence check %q references missing screenshot %q", check.Name, check.Screenshot)
		}
	}
	if err := validateBrowserDiagnostics(e.Diagnostics, e.BrowserChannel); err != nil {
		return err
	}

	return nil
}

func ValidateBaseKitBetaBrowserEvidenceFailure(e BrowserEvidence) error {
	if strings.TrimSpace(e.ScenarioID) != "SK-S1" {
		return fmt.Errorf("browser evidence failure scenarioId = %q, want SK-S1", e.ScenarioID)
	}
	if strings.TrimSpace(e.RunID) == "" {
		return fmt.Errorf("browser evidence failure must include runId")
	}
	if strings.TrimSpace(e.Status) != BrowserEvidenceStatusFail {
		return fmt.Errorf("browser evidence failure status = %q, want fail", e.Status)
	}
	if err := requireRFC3339("browser evidence failure generatedAt", e.GeneratedAt); err != nil {
		return err
	}
	if strings.TrimSpace(e.OwnerEmail) == "" || !strings.Contains(e.OwnerEmail, "@") {
		return fmt.Errorf("browser evidence failure ownerEmail must be email-shaped")
	}
	if strings.TrimSpace(e.OwnerUsername) != "" && strings.Contains(e.OwnerUsername, "@") {
		return fmt.Errorf("browser evidence failure ownerUsername must be a username, not an email address")
	}
	if strings.TrimSpace(e.BrowserChannel) == "" {
		return fmt.Errorf("browser evidence failure must include browserChannel")
	}
	if err := requireBrowserURL("browser failure browserUrl", e.BrowserURL); err != nil {
		return err
	}
	if strings.TrimSpace(e.Error) == "" && !hasFailedBrowserCheck(e.Checks) {
		return fmt.Errorf("browser evidence failure must include error or at least one failed check")
	}
	if strings.TrimSpace(e.FailurePhase) == "" && len(e.Checks) == 0 {
		return fmt.Errorf("browser evidence failure with no checks must include failurePhase")
	}
	if failurePhase := strings.TrimSpace(e.FailurePhase); failurePhase != "" && !isAllowedBrowserFailurePhase(failurePhase) {
		return fmt.Errorf("browser evidence failure failurePhase = %q, want one of %s", failurePhase, strings.Join(AllowedBaseKitBetaBrowserFailurePhases, ", "))
	}
	if err := validateBrowserFailureWrapperDiagnostics(e.Diagnostics.Wrapper, e.FailurePhase); err != nil {
		return err
	}
	if err := validateBrowserFailureChecks(e.Checks); err != nil {
		return err
	}
	for _, screenshot := range e.Screenshots {
		if err := validateBrowserScreenshot(screenshot); err != nil {
			return err
		}
		if screenshot.URL != "" {
			if err := requireBrowserURL("browser failure screenshot "+screenshot.Name+" url", screenshot.URL); err != nil {
				return err
			}
		}
	}
	return nil
}

func LoadBrowserEvidence(path string) (BrowserEvidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BrowserEvidence{}, fmt.Errorf("read browser evidence %s: %w", path, err)
	}
	var evidence BrowserEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return BrowserEvidence{}, fmt.Errorf("parse browser evidence %s: %w", path, err)
	}
	return evidence, nil
}

func ValidateBaseKitBetaBrowserEvidenceFiles(root string, e BrowserEvidence) error {
	if err := ValidateBaseKitBetaBrowserEvidence(e); err != nil {
		return err
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve browser evidence root %s: %w", root, err)
	}
	for _, screenshot := range e.Screenshots {
		path, err := resolveBrowserEvidencePath(absRoot, screenshot.Path)
		if err != nil {
			return fmt.Errorf("browser evidence screenshot %q: %w", screenshot.Name, err)
		}
		if err := validateScreenshotFile(path); err != nil {
			return fmt.Errorf("browser evidence screenshot %q: %w", screenshot.Name, err)
		}
	}
	if err := validateSetupStateDiagnosticFile(absRoot, e.Diagnostics.SetupState); err != nil {
		return err
	}
	return nil
}

func validateBrowserCheck(check BrowserEvidenceCheck, ownerEmail string) error {
	if strings.TrimSpace(check.Status) != BrowserEvidenceStatusPass {
		return fmt.Errorf("browser evidence check %q status = %q, want pass", check.Name, check.Status)
	}
	if err := requireBrowserURL("check "+check.Name+" url", check.URL); err != nil {
		return err
	}
	if check.DurationSeconds <= 0 {
		return fmt.Errorf("browser evidence check %q must record durationSeconds", check.Name)
	}
	if check.DurationSeconds > MaxBrowserCheckDurationSeconds {
		return fmt.Errorf("browser evidence check %q durationSeconds = %d, exceeds 15 minute budget", check.Name, check.DurationSeconds)
	}
	if strings.TrimSpace(check.ExpectedText) == "" {
		return fmt.Errorf("browser evidence check %q must record expectedText", check.Name)
	}
	if strings.TrimSpace(check.ObservedText) == "" {
		return fmt.Errorf("browser evidence check %q must record observedText", check.Name)
	}
	if err := validateBrowserCheckContentEvidence(check, ownerEmail); err != nil {
		return err
	}
	return nil
}

func validateBrowserCheckContentEvidence(check BrowserEvidenceCheck, ownerEmail string) error {
	switch check.Name {
	case "pocketid-owner-passkey":
		if check.Evidence["verification"] != "webauthn-virtual-authenticator" {
			return fmt.Errorf("browser evidence check %q must prove PocketID passkey creation through the WebAuthn virtual authenticator", check.Name)
		}
		count, ok := positiveEvidenceInteger(check.Evidence["passkeyCredentials"])
		if !ok || count < 1 {
			return fmt.Errorf("browser evidence check %q passkeyCredentials = %q, want >= 1", check.Name, check.Evidence["passkeyCredentials"])
		}
		if strings.TrimSpace(check.Evidence["authenticatorProtocol"]) != "ctap2" {
			return fmt.Errorf("browser evidence check %q authenticatorProtocol = %q, want ctap2", check.Name, check.Evidence["authenticatorProtocol"])
		}
		if strings.TrimSpace(check.Evidence["authenticatorTransport"]) == "" {
			return fmt.Errorf("browser evidence check %q must record authenticatorTransport", check.Name)
		}
	case "tinyauth-owner-session":
		if check.Evidence["verification"] != "tinyauth-forwardauth-session" ||
			check.Evidence["authBoundary"] != "tinyauth-pocketid" {
			return fmt.Errorf("browser evidence check %q must prove the TinyAuth/PocketID Owner session through the ForwardAuth endpoint", check.Name)
		}
		cookieCount, ok := positiveEvidenceInteger(check.Evidence["sessionCookieCount"])
		if !ok || cookieCount < 1 {
			return fmt.Errorf("browser evidence check %q sessionCookieCount = %q, want >= 1", check.Name, check.Evidence["sessionCookieCount"])
		}
		status, ok := positiveEvidenceInteger(check.Evidence["forwardAuthStatus"])
		if !ok || status < 200 || status > 299 {
			return fmt.Errorf("browser evidence check %q forwardAuthStatus = %q, want 2xx", check.Name, check.Evidence["forwardAuthStatus"])
		}
		if strings.TrimSpace(check.Evidence["sessionCookieNames"]) == "" {
			return fmt.Errorf("browser evidence check %q must record TinyAuth sessionCookieNames", check.Name)
		}
		if strings.TrimSpace(check.Evidence["sessionCookieDomains"]) == "" {
			return fmt.Errorf("browser evidence check %q must record TinyAuth sessionCookieDomains", check.Name)
		}
		signal := strings.TrimSpace(check.Evidence["ownerSessionSignal"])
		switch signal {
		case "forwardauth-2xx", "logout", "signed-in", "owner":
		default:
			return fmt.Errorf("browser evidence check %q ownerSessionSignal = %q, want TinyAuth authenticated Owner-session signal", check.Name, signal)
		}
		if err := requireBrowserURL("check "+check.Name+" authUrl", check.Evidence["authUrl"]); err != nil {
			return err
		}
		if err := requireBrowserURL("check "+check.Name+" sessionUrl", check.Evidence["sessionUrl"]); err != nil {
			return err
		}
		if err := requireBrowserURL("check "+check.Name+" forwardAuthEndpoint", check.Evidence["forwardAuthEndpoint"]); err != nil {
			return err
		}
	case "photos-demo-content":
		if check.Evidence["demoContent"] != "immich-demo-assets" {
			return fmt.Errorf("browser evidence check %q must prove Immich seeded demo assets", check.Name)
		}
		if check.Evidence["verification"] != "immich-search-metadata" {
			return fmt.Errorf("browser evidence check %q must prove the StackKit Immich demo asset through metadata search", check.Name)
		}
		if check.Evidence["ownerVerification"] != "immich-users-me" {
			return fmt.Errorf("browser evidence check %q must prove the Immich browser session Owner through /api/users/me", check.Name)
		}
		sessionOwnerEmail := strings.TrimSpace(check.Evidence["immichOwnerEmail"])
		if sessionOwnerEmail == "" || !strings.EqualFold(sessionOwnerEmail, strings.TrimSpace(ownerEmail)) {
			return fmt.Errorf("browser evidence check %q immichOwnerEmail = %q, want Owner %q", check.Name, sessionOwnerEmail, ownerEmail)
		}
		if check.Evidence["demoAssetDeviceId"] != "stackkit-demo" ||
			check.Evidence["demoAssetDeviceAssetId"] != "stackkit-demo-photo-1" ||
			check.Evidence["demoAssetFile"] != "stackkit-demo-photo.png" {
			return fmt.Errorf("browser evidence check %q must prove the StackKit Immich demo photo identity", check.Name)
		}
		count, ok := positiveEvidenceInteger(check.Evidence["immichDemoAssets"])
		if !ok {
			return fmt.Errorf("browser evidence check %q immichDemoAssets = %q, want positive integer", check.Name, check.Evidence["immichDemoAssets"])
		}
		if count < 1 {
			return fmt.Errorf("browser evidence check %q immichDemoAssets = %d, want >= 1", check.Name, count)
		}
	case "files-demo-content":
		if check.Evidence["demoContent"] != "cloudreve-demo-file" ||
			check.Evidence["seededFolder"] != "StackKit Demo" ||
			check.Evidence["seededFile"] != "README.txt" {
			return fmt.Errorf("browser evidence check %q must prove Cloudreve StackKit Demo/README.txt content", check.Name)
		}
		if check.Evidence["verification"] != "cloudreve-browser-session-api" {
			return fmt.Errorf("browser evidence check %q must prove Cloudreve content through the browser session API", check.Name)
		}
		if check.Evidence["identityBridge"] != "stackkit-cloudreve-local-session" ||
			check.Evidence["bridgeVerification"] != "stackkit-cloudreve-session-bridge" {
			return fmt.Errorf("browser evidence check %q must prove the StackKit Files session bridge created the Cloudreve session", check.Name)
		}
		bridgeUser := strings.TrimSpace(check.Evidence["bridgeCurrentUser"])
		sessionUser := strings.TrimSpace(check.Evidence["cloudreveSessionUser"])
		if bridgeUser == "" || sessionUser == "" || bridgeUser != sessionUser {
			return fmt.Errorf("browser evidence check %q bridgeCurrentUser = %q and cloudreveSessionUser = %q must match", check.Name, bridgeUser, sessionUser)
		}
	case "vault-auth-boundary":
		if check.Evidence["verification"] != "anonymous-vault-route-check" ||
			check.Evidence["authBoundary"] != "tinyauth-pocketid" ||
			check.Evidence["anonymousAccess"] != "rejected" {
			return fmt.Errorf("browser evidence check %q must prove anonymous Vault access is rejected by the TinyAuth/PocketID boundary", check.Name)
		}
		signal := strings.TrimSpace(check.Evidence["anonymousBoundarySignal"])
		if signal == "" {
			return fmt.Errorf("browser evidence check %q must record the anonymous Vault auth-boundary signal", check.Name)
		}
		switch signal {
		case "http-401", "http-403", "tinyauth", "pocketid", "auth-host":
		default:
			return fmt.Errorf("browser evidence check %q anonymousBoundarySignal = %q, want TinyAuth/PocketID or HTTP rejection signal", check.Name, signal)
		}
		status := strings.TrimSpace(check.Evidence["anonymousStatus"])
		if status == "" {
			return fmt.Errorf("browser evidence check %q must record anonymousStatus", check.Name)
		}
		if err := requireBrowserURL("check "+check.Name+" anonymousUrl", check.Evidence["anonymousUrl"]); err != nil {
			return err
		}
	}
	return nil
}

func validateBrowserDiagnostics(diagnostics BrowserDiagnostics, browserChannel string) error {
	if err := validateBrowserRuntimeDiagnostics(diagnostics.Browser, browserChannel); err != nil {
		return err
	}
	if diagnostics.SetupState == nil {
		return fmt.Errorf("browser evidence must include setupState diagnostics")
	}
	setupState := diagnostics.SetupState
	switch strings.TrimSpace(setupState.Status) {
	case "present":
	case "missing":
		return fmt.Errorf("browser evidence setupState diagnostics are missing exported SetupRun state")
	default:
		return fmt.Errorf("browser evidence setupState diagnostics status = %q, want present or missing", setupState.Status)
	}
	if strings.TrimSpace(setupState.SourcePath) == "" {
		return fmt.Errorf("browser evidence setupState diagnostics must include sourcePath")
	}
	if err := validateBrowserEvidenceRelativePath("setupState diagnostics", setupState.SourcePath, ""); err != nil {
		return err
	}
	for _, dropName := range RequiredBaseKitBetaSetupDrops {
		drop, ok := setupState.Drops[dropName]
		if !ok || strings.TrimSpace(drop.Status) == "" || drop.Status == "missing" {
			return fmt.Errorf("browser evidence setupState diagnostics missing setup drop %q", dropName)
		}
		if strings.TrimSpace(drop.Status) != "completed" {
			return fmt.Errorf("browser evidence setup drop %q status = %q, want completed", dropName, drop.Status)
		}
		if strings.TrimSpace(drop.Phase) != "verified" {
			return fmt.Errorf("browser evidence setup drop %q phase = %q, want verified", dropName, drop.Phase)
		}
		if err := validateBrowserSetupRunReference("browser evidence setup drop "+dropName, drop.RunID, drop.Attempts, drop.LastRequested, drop.LastStarted, drop.LastFinished); err != nil {
			return err
		}
		if err := validateBrowserSetupRunAuditTrail("browser evidence setup drop "+dropName, drop.LogCount, drop.RollbackNoteCount); err != nil {
			return err
		}
		if err := validateBrowserSetupStateEvidence("browser evidence setup drop "+dropName, dropName, drop.Evidence); err != nil {
			return err
		}
	}
	if err := validateBrowserSetupActions(diagnostics.SetupActions); err != nil {
		return err
	}
	return nil
}

func hasFailedBrowserCheck(checks []BrowserEvidenceCheck) bool {
	for _, check := range checks {
		if strings.TrimSpace(check.Status) == BrowserEvidenceStatusFail {
			return true
		}
	}
	return false
}

func validateBrowserFailureWrapperDiagnostics(wrapper *BrowserWrapperDiagnostics, failurePhase string) error {
	phase := strings.TrimSpace(failurePhase)
	if phase == "" {
		return nil
	}
	if wrapper == nil {
		return fmt.Errorf("browser evidence failure with failurePhase %q must include wrapper diagnostics", phase)
	}
	if strings.TrimSpace(wrapper.Phase) != phase {
		return fmt.Errorf("browser evidence failure wrapper phase = %q, want %q", wrapper.Phase, phase)
	}
	if strings.TrimSpace(wrapper.EvidenceRoot) == "" {
		return fmt.Errorf("browser evidence failure wrapper diagnostics must include evidenceRoot")
	}
	if strings.TrimSpace(wrapper.PreflightReportPath) == "" {
		return fmt.Errorf("browser evidence failure wrapper diagnostics must include preflightReportPath")
	}
	if strings.TrimSpace(wrapper.HomelabPath) == "" {
		return fmt.Errorf("browser evidence failure wrapper diagnostics must include homelabPath")
	}
	if err := validateBrowserNativeCommandDiagnostics(wrapper.NativeCommand, "browser evidence failure wrapper"); err != nil {
		return err
	}
	return nil
}

func validateBrowserNativeCommandDiagnostics(nativeCommand *BrowserWrapperNativeCommandDiagnostics, context string) error {
	if nativeCommand == nil {
		return nil
	}
	if strings.TrimSpace(nativeCommand.Name) == "" {
		return fmt.Errorf("%s nativeCommand must include name", context)
	}
	if strings.TrimSpace(nativeCommand.FilePath) == "" {
		return fmt.Errorf("%s nativeCommand must include filePath", context)
	}
	if nativeCommand.TimeoutSeconds <= 0 {
		return fmt.Errorf("%s nativeCommand must include timeoutSeconds", context)
	}
	if failureClass := strings.TrimSpace(nativeCommand.FailureClass); failureClass != "" && !isAllowedBrowserNativeCommandFailureClass(failureClass) {
		return fmt.Errorf("%s nativeCommand failureClass = %q, want one of start_failed, timeout, exit_nonzero", context, failureClass)
	}
	if hostIssue := strings.TrimSpace(nativeCommand.HostIssue); hostIssue != "" {
		if !isAllowedBrowserNativeCommandHostIssue(hostIssue) {
			return fmt.Errorf("%s nativeCommand hostIssue = %q, want windows-createprocessasuser-access-denied", context, hostIssue)
		}
		if strings.TrimSpace(nativeCommand.FailureClass) != "start_failed" {
			return fmt.Errorf("%s nativeCommand hostIssue = %q requires failureClass start_failed", context, hostIssue)
		}
	}
	if nativeCommand.ExitCode != nil && *nativeCommand.ExitCode < 0 {
		return fmt.Errorf("%s nativeCommand exitCode must be non-negative", context)
	}
	return nil
}

func isAllowedBrowserNativeCommandFailureClass(value string) bool {
	switch value {
	case "start_failed", "timeout", "exit_nonzero":
		return true
	default:
		return false
	}
}

func isAllowedBrowserNativeCommandHostIssue(value string) bool {
	switch value {
	case "windows-createprocessasuser-access-denied":
		return true
	default:
		return false
	}
}

func isAllowedBrowserFailurePhase(phase string) bool {
	for _, allowed := range AllowedBaseKitBetaBrowserFailurePhases {
		if phase == allowed {
			return true
		}
	}
	return false
}

func validateBrowserFailureChecks(checks []BrowserEvidenceCheck) error {
	checksByName := map[string]bool{}
	for _, check := range checks {
		name := strings.TrimSpace(check.Name)
		if name == "" {
			return fmt.Errorf("browser evidence failure contains a check without name")
		}
		if checksByName[name] {
			return fmt.Errorf("browser evidence failure contains duplicate check %q", name)
		}
		checksByName[name] = true
		switch strings.TrimSpace(check.Status) {
		case BrowserEvidenceStatusPass, BrowserEvidenceStatusFail:
		default:
			return fmt.Errorf("browser evidence failure check %q status = %q, want pass or fail", check.Name, check.Status)
		}
		if strings.TrimSpace(check.URL) != "" {
			if err := requireBrowserURL("browser failure check "+check.Name+" url", check.URL); err != nil {
				return err
			}
		}
		if check.DurationSeconds > MaxBrowserCheckDurationSeconds {
			return fmt.Errorf("browser evidence failure check %q durationSeconds = %d, exceeds 15 minute budget", check.Name, check.DurationSeconds)
		}
		if check.Screenshot != "" {
			if err := validateBrowserEvidenceRelativePath("browser evidence failure check "+check.Name+" screenshot", check.Screenshot, ".png,.jpg,.jpeg,.webp"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateBrowserRuntimeDiagnostics(browser *BrowserRuntimeDiagnostics, browserChannel string) error {
	if browser == nil {
		return fmt.Errorf("browser evidence must include browser runtime diagnostics")
	}
	expectedChannel := browserEvidenceChannelLabel(browserChannel)
	if strings.TrimSpace(browser.Channel) != expectedChannel {
		return fmt.Errorf("browser evidence runtime channel = %q, want %q", browser.Channel, expectedChannel)
	}
	if strings.TrimSpace(browser.RequestedChannel) == "" {
		return fmt.Errorf("browser evidence runtime must include requestedChannel")
	}
	switch strings.TrimSpace(browser.Headless) {
	case "true", "false":
	default:
		return fmt.Errorf("browser evidence runtime headless = %q, want true or false", browser.Headless)
	}
	if !validBrowserViewport(browser.Viewport) {
		return fmt.Errorf("browser evidence runtime viewport = %q, want WIDTHxHEIGHT at least %dx%d", browser.Viewport, MinBrowserScreenshotWidth, MinBrowserScreenshotHeight)
	}
	if strings.TrimSpace(browser.UserAgent) == "" {
		return fmt.Errorf("browser evidence runtime must include userAgent")
	}
	if strings.TrimSpace(browser.BrowserVersion) == "" {
		return fmt.Errorf("browser evidence runtime must include browserVersion")
	}
	if strings.TrimSpace(browser.WebAuthnVirtualAuthenticator) != "enabled" {
		return fmt.Errorf("browser evidence runtime webAuthnVirtualAuthenticator = %q, want enabled", browser.WebAuthnVirtualAuthenticator)
	}
	return nil
}

func browserEvidenceChannelLabel(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "default", "chromium", "playwright-chromium":
		return "playwright-chromium"
	default:
		return value
	}
}

func validBrowserViewport(raw string) bool {
	parts := strings.Split(strings.TrimSpace(raw), "x")
	if len(parts) != 2 {
		return false
	}
	width, ok := positiveEvidenceInteger(parts[0])
	if !ok {
		return false
	}
	height, ok := positiveEvidenceInteger(parts[1])
	if !ok {
		return false
	}
	return width >= MinBrowserScreenshotWidth && height >= MinBrowserScreenshotHeight
}

func validateBrowserSetupActions(actions []BrowserSetupActionDiagnostics) error {
	actionsByService := map[string]BrowserSetupActionDiagnostics{}
	for _, action := range actions {
		service := strings.TrimSpace(action.Service)
		if service == "" {
			return fmt.Errorf("browser evidence setupActions contains an action without service")
		}
		if _, exists := actionsByService[service]; exists {
			return fmt.Errorf("browser evidence setupActions contains duplicate service %q", service)
		}
		actionsByService[service] = action
	}
	for _, service := range RequiredBaseKitBetaOwnerSetupServices {
		action, ok := actionsByService[service]
		if !ok {
			return fmt.Errorf("browser evidence setupActions missing owner-activated service %q", service)
		}
		if strings.ToLower(strings.TrimSpace(action.OK)) != "true" {
			return fmt.Errorf("browser evidence setupAction %q ok = %q, want true", service, action.OK)
		}
		httpStatus, ok := positiveEvidenceInteger(action.HTTPStatus)
		if !ok || httpStatus < 200 || httpStatus > 299 {
			return fmt.Errorf("browser evidence setupAction %q httpStatus = %q, want 2xx", service, action.HTTPStatus)
		}
		durationSeconds, ok := positiveEvidenceInteger(action.DurationSeconds)
		if !ok || durationSeconds <= 0 {
			return fmt.Errorf("browser evidence setupAction %q must record durationSeconds", service)
		}
		if durationSeconds > MaxBrowserCheckDurationSeconds {
			return fmt.Errorf("browser evidence setupAction %q durationSeconds = %d, exceeds 15 minute budget", service, durationSeconds)
		}
		if strings.TrimSpace(action.Status) != "completed" {
			return fmt.Errorf("browser evidence setupAction %q status = %q, want completed", service, action.Status)
		}
		expectedDrop := RequiredBaseKitBetaOwnerSetupDropsByService[service]
		if strings.TrimSpace(action.DropName) != expectedDrop {
			return fmt.Errorf("browser evidence setupAction %q dropName = %q, want %q", service, action.DropName, expectedDrop)
		}
		if strings.TrimSpace(action.DropStatus) != "completed" {
			return fmt.Errorf("browser evidence setupAction %q dropStatus = %q, want completed", service, action.DropStatus)
		}
		if strings.TrimSpace(action.DropPhase) != "verified" {
			return fmt.Errorf("browser evidence setupAction %q dropPhase = %q, want verified", service, action.DropPhase)
		}
		if err := validateBrowserSetupRunReference("browser evidence setupAction "+service, action.RunID, action.Attempts, action.LastRequested, action.LastStarted, action.LastFinished); err != nil {
			return err
		}
		logCount, ok := positiveEvidenceInteger(action.LogCount)
		if !ok || logCount < 1 {
			return fmt.Errorf("browser evidence setupAction %q logCount = %q, want >= 1", service, action.LogCount)
		}
		rollbackNoteCount, ok := positiveEvidenceInteger(action.RollbackNoteCount)
		if !ok || rollbackNoteCount < 1 {
			return fmt.Errorf("browser evidence setupAction %q rollbackNoteCount = %q, want >= 1", service, action.RollbackNoteCount)
		}
	}
	return nil
}

func validateBrowserSetupRunReference(label, runID, attempts, lastRequested, lastStarted, lastFinished string) error {
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("%s must include runId", label)
	}
	attemptCount, ok := positiveEvidenceInteger(attempts)
	if !ok || attemptCount < 1 {
		return fmt.Errorf("%s attempts = %q, want >= 1", label, attempts)
	}
	if err := requireRFC3339(label+" lastRequested", lastRequested); err != nil {
		return err
	}
	if err := requireRFC3339(label+" lastStarted", lastStarted); err != nil {
		return err
	}
	if err := requireRFC3339(label+" lastFinished", lastFinished); err != nil {
		return err
	}
	return nil
}

func validateBrowserSetupRunAuditTrail(label, logCount, rollbackNoteCount string) error {
	logs, ok := positiveEvidenceInteger(logCount)
	if !ok || logs < 1 {
		return fmt.Errorf("%s logCount = %q, want >= 1", label, logCount)
	}
	rollbackNotes, ok := positiveEvidenceInteger(rollbackNoteCount)
	if !ok || rollbackNotes < 1 {
		return fmt.Errorf("%s rollbackNoteCount = %q, want >= 1", label, rollbackNoteCount)
	}
	return nil
}

func validateBrowserSetupStateEvidence(label, dropName string, evidence map[string]string) error {
	required := map[string]map[string]string{
		"cloudreve-owner-bootstrap": {
			"credentialRole":          "technical-admin-bootstrap",
			"appLocalAccount":         "stackkit-admin-created",
			"demoData":                "seeded-when-enabled",
			"outerAuthBoundary":       "tinyauth-pocketid",
			"ownerLogin":              "pocketid-passkey",
			"identityBridge":          "stackkit-cloudreve-local-session",
			"appLocalSessionHandoff":  "stackkit-session-bridge-prepared",
			"readyToUseContentStatus": "pending-browser-evidence",
		},
		"vaultwarden-admin-handoff": {
			"credentialRole":         "break-glass-admin-token",
			"ownerLogin":             "pocketid-passkey",
			"adminTokenPosture":      "verified-break-glass",
			"adminTokenStorage":      "argon2id-phc-runtime",
			"appLocalSignups":        "disabled",
			"plaintextAdminTokenEnv": "absent",
			"outerAuthBoundary":      "tinyauth-pocketid",
		},
		"immich-owner-bootstrap": {
			"credentialRole":         "technical-admin-bootstrap",
			"technicalAdmin":         "stackkit-admin-created",
			"appLocalOwner":          "pocketid-owner-preprovisioned",
			"demoData":               "seeded-when-enabled",
			"outerAuthBoundary":      "tinyauth-pocketid",
			"ownerLogin":             "pocketid-passkey",
			"pocketidOAuth":          "enabled",
			"oidcClientId":           "stackkit-immich",
			"autoRegister":           "false",
			"autoLaunch":             "true",
			"appLocalSessionHandoff": "oidc-email-link-prepared",
		},
	}
	for key, want := range required[dropName] {
		if evidence[key] != want {
			return fmt.Errorf("%s evidence[%s] = %q, want %q", label, key, evidence[key], want)
		}
	}
	if dropName == "immich-owner-bootstrap" {
		if strings.TrimSpace(evidence["ownerEmail"]) == "" || strings.TrimSpace(evidence["ownerProvisioning"]) == "" {
			return fmt.Errorf("%s must include Owner handoff evidence", label)
		}
		if !strings.HasPrefix(strings.TrimSpace(evidence["oidcIssuer"]), "http") {
			return fmt.Errorf("%s evidence[oidcIssuer] = %q, want URL evidence", label, evidence["oidcIssuer"])
		}
	}
	return nil
}

func positiveEvidenceInteger(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		value = value*10 + int(ch-'0')
	}
	return value, true
}

func validateBrowserScreenshot(screenshot BrowserScreenshot) error {
	if strings.TrimSpace(screenshot.Name) == "" {
		return fmt.Errorf("browser evidence contains a screenshot without name")
	}
	path := strings.TrimSpace(screenshot.Path)
	if path == "" {
		return fmt.Errorf("browser evidence screenshot %q has empty path", screenshot.Name)
	}
	if err := validateBrowserEvidenceRelativePath("browser evidence screenshot "+screenshot.Name, path, ".png,.jpg,.jpeg,.webp"); err != nil {
		return err
	}
	return nil
}

func validateBrowserEvidenceRelativePath(label, rawPath, allowedExtCSV string) error {
	if isAbsoluteEvidencePath(rawPath) {
		return fmt.Errorf("%s path must be relative", label)
	}
	clean := filepath.Clean(filepath.FromSlash(rawPath))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s path must stay under the evidence root", label)
	}
	if allowedExtCSV != "" {
		allowed := strings.Split(allowedExtCSV, ",")
		ext := strings.ToLower(filepath.Ext(clean))
		for _, candidate := range allowed {
			if ext == candidate {
				return nil
			}
		}
		return fmt.Errorf("%s path must end with %s", label, strings.ReplaceAll(allowedExtCSV, ",", ", "))
	}
	return nil
}

func resolveBrowserEvidencePath(absRoot, rawPath string) (string, error) {
	if err := validateBrowserScreenshot(BrowserScreenshot{Name: "path", Path: rawPath}); err != nil {
		return "", err
	}
	return resolveBrowserEvidenceRelativePath(absRoot, rawPath)
}

func resolveBrowserEvidenceRelativePath(absRoot, rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", fmt.Errorf("path is empty")
	}
	if err := validateBrowserEvidenceRelativePath("browser evidence artifact", rawPath, ""); err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(absRoot, filepath.FromSlash(strings.TrimSpace(rawPath))))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, candidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes evidence root")
	}
	return candidate, nil
}

func validateSetupStateDiagnosticFile(absRoot string, setupState *BrowserSetupStateDiagnostics) error {
	if setupState == nil {
		return fmt.Errorf("browser evidence setupState diagnostics file requires setupState diagnostics")
	}
	rawSourcePath := strings.TrimSpace(setupState.SourcePath)
	if rawSourcePath == "" {
		return fmt.Errorf("browser evidence setupState diagnostics must include sourcePath")
	}
	if err := validateBrowserEvidenceRelativePath("setupState diagnostics", rawSourcePath, ""); err != nil {
		return err
	}
	path, err := resolveBrowserEvidenceRelativePath(absRoot, rawSourcePath)
	if err != nil {
		return fmt.Errorf("browser evidence setupState diagnostics: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("browser evidence setupState file %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("browser evidence setupState file %s is empty", path)
	}
	drops, err := setupRunsByDropName(data)
	if err != nil {
		return fmt.Errorf("browser evidence setupState file %s: %w", path, err)
	}
	for _, dropName := range RequiredBaseKitBetaSetupDrops {
		drop, ok := drops[dropName]
		if !ok {
			return fmt.Errorf("browser evidence setupState file missing setup drop %q", dropName)
		}
		if strings.TrimSpace(drop.Status) != "completed" {
			return fmt.Errorf("browser evidence setupState file drop %q status = %q, want completed", dropName, drop.Status)
		}
		if strings.TrimSpace(drop.Phase) != "verified" {
			return fmt.Errorf("browser evidence setupState file drop %q phase = %q, want verified", dropName, drop.Phase)
		}
		if err := validateBrowserSetupRunReference("browser evidence setupState file drop "+dropName, drop.RunID, drop.Attempts, drop.LastRequested, drop.LastStarted, drop.LastFinished); err != nil {
			return err
		}
		if err := validateBrowserSetupRunAuditTrail("browser evidence setupState file drop "+dropName, drop.LogCount, drop.RollbackNoteCount); err != nil {
			return err
		}
		if err := validateBrowserSetupStateEvidence("browser evidence setupState file drop "+dropName, dropName, drop.Evidence); err != nil {
			return err
		}
	}
	return nil
}

func setupRunsByDropName(data []byte) (map[string]BrowserSetupDropStatus, error) {
	var state struct {
		SetupRuns []struct {
			DropName      string `yaml:"dropName"`
			RunID         string `yaml:"runId"`
			Status        string `yaml:"status"`
			Phase         string `yaml:"phase"`
			ServiceKey    string `yaml:"serviceKey"`
			FailureClass  string `yaml:"failureClass"`
			Attempts      string `yaml:"attempts"`
			LastRequested string `yaml:"lastRequested"`
			LastStarted   string `yaml:"lastStarted"`
			LastFinished  string `yaml:"lastFinished"`
			Logs          []struct {
				Message string `yaml:"message"`
			} `yaml:"logs"`
			RollbackNotes []string          `yaml:"rollbackNotes"`
			Evidence      map[string]string `yaml:"evidence"`
		} `yaml:"setupRuns"`
	}
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse setupState yaml: %w", err)
	}
	drops := map[string]BrowserSetupDropStatus{}
	for _, run := range state.SetupRuns {
		dropName := strings.TrimSpace(run.DropName)
		if dropName == "" {
			continue
		}
		drops[dropName] = BrowserSetupDropStatus{
			RunID:             strings.TrimSpace(run.RunID),
			Status:            strings.TrimSpace(run.Status),
			Phase:             strings.TrimSpace(run.Phase),
			ServiceKey:        strings.TrimSpace(run.ServiceKey),
			FailureClass:      strings.TrimSpace(run.FailureClass),
			Attempts:          strings.TrimSpace(run.Attempts),
			LastRequested:     strings.TrimSpace(run.LastRequested),
			LastStarted:       strings.TrimSpace(run.LastStarted),
			LastFinished:      strings.TrimSpace(run.LastFinished),
			LogCount:          fmt.Sprintf("%d", len(run.Logs)),
			RollbackNoteCount: fmt.Sprintf("%d", len(run.RollbackNotes)),
			Evidence:          run.Evidence,
		}
	}
	return drops, nil
}

func validateScreenshotFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s is empty", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, 64)
	n, err := io.ReadFull(file, header)
	if err != nil && err != io.ErrUnexpectedEOF {
		return fmt.Errorf("read %s header: %w", path, err)
	}
	header = header[:n]
	if !hasSupportedScreenshotSignature(header) {
		return fmt.Errorf("%s is not a PNG, JPEG, or WebP screenshot", path)
	}
	width, height, err := screenshotDimensions(file, header)
	if err != nil {
		return fmt.Errorf("%s dimensions: %w", path, err)
	}
	if width < MinBrowserScreenshotWidth || height < MinBrowserScreenshotHeight {
		return fmt.Errorf("%s dimensions = %dx%d, want at least %dx%d", path, width, height, MinBrowserScreenshotWidth, MinBrowserScreenshotHeight)
	}
	return nil
}

func hasSupportedScreenshotSignature(header []byte) bool {
	if bytes.HasPrefix(header, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return true
	}
	if bytes.HasPrefix(header, []byte{0xff, 0xd8, 0xff}) {
		return true
	}
	return len(header) >= 12 && bytes.HasPrefix(header, []byte("RIFF")) && string(header[8:12]) == "WEBP"
}

func screenshotDimensions(file *os.File, header []byte) (int, int, error) {
	if width, height, ok, err := webpDimensions(header); ok || err != nil {
		return width, height, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, 0, fmt.Errorf("seek image start: %w", err)
	}
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, fmt.Errorf("decode image config: %w", err)
	}
	if format != "png" && format != "jpeg" {
		return 0, 0, fmt.Errorf("unsupported screenshot format %q", format)
	}
	return config.Width, config.Height, nil
}

func webpDimensions(header []byte) (int, int, bool, error) {
	if !bytes.HasPrefix(header, []byte("RIFF")) || len(header) < 16 || string(header[8:12]) != "WEBP" {
		return 0, 0, false, nil
	}
	chunk := string(header[12:16])
	switch chunk {
	case "VP8X":
		if len(header) < 30 {
			return 0, 0, true, fmt.Errorf("short VP8X header")
		}
		width := 1 + littleEndian24(header[24:27])
		height := 1 + littleEndian24(header[27:30])
		return width, height, true, nil
	case "VP8L":
		if len(header) < 25 {
			return 0, 0, true, fmt.Errorf("short VP8L header")
		}
		if header[20] != 0x2f {
			return 0, 0, true, fmt.Errorf("invalid VP8L signature")
		}
		b0, b1, b2, b3 := header[21], header[22], header[23], header[24]
		width := 1 + int(b0) + int(b1&0x3f)<<8
		height := 1 + int(b1>>6) + int(b2)<<2 + int(b3&0x0f)<<10
		return width, height, true, nil
	case "VP8 ":
		if len(header) < 30 {
			return 0, 0, true, fmt.Errorf("short VP8 header")
		}
		if header[23] != 0x9d || header[24] != 0x01 || header[25] != 0x2a {
			return 0, 0, true, fmt.Errorf("invalid VP8 start code")
		}
		width := littleEndian16(header[26:28]) & 0x3fff
		height := littleEndian16(header[28:30]) & 0x3fff
		return width, height, true, nil
	default:
		return 0, 0, true, fmt.Errorf("unsupported WebP chunk %q", chunk)
	}
}

func littleEndian16(data []byte) int {
	return int(data[0]) | int(data[1])<<8
}

func littleEndian24(data []byte) int {
	return int(data[0]) | int(data[1])<<8 | int(data[2])<<16
}

func isAbsoluteEvidencePath(path string) bool {
	if filepath.IsAbs(filepath.FromSlash(path)) {
		return true
	}
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return true
	}
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func requireBrowserURL(field, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", field)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s scheme = %q, want http or https", field, parsed.Scheme)
	}
	return nil
}

func requireRFC3339(field, raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fmt.Errorf("%s must be RFC3339", field)
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return fmt.Errorf("%s must be RFC3339: %w", field, err)
	}
	return nil
}
