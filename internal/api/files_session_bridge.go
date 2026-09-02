package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kombifyio/stackkits/internal/appsetup"
	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

const (
	cloudreveDemoFolderName  = "StackKit Demo"
	cloudreveDemoFileName    = "README.txt"
	cloudreveDemoFileBody    = "Welcome to your StackKit Files workspace.\nThis demo file is created automatically during the BaseKit beta bootstrap.\n"
	filesSessionBridgeHeader = "X-StackKit-Files-Bridge"
)

type cloudreveAPIError = appsetup.CloudreveAPIError
type cloudreveLoginResponse = appsetup.CloudreveLoginResponse

type cloudreveFileList struct {
	Files []struct {
		Type int    `json:"type"`
		Name string `json:"name"`
	} `json:"files"`
}

// cloudreveOwnerSession keeps the temporary technical session alive only
// until a caller either commits the explicit browser handoff or cleans it up.
// The shared appsetup adapter remains the sole Cloudreve authority.
type cloudreveOwnerSession struct {
	technical        appsetup.CloudreveOwnerResult
	client           *http.Client
	baseURL          string
	handoffCommitted bool
}

func (s *cloudreveOwnerSession) close() {
	if s != nil && s.client != nil {
		s.client.CloseIdleConnections()
	}
}

func (s *cloudreveOwnerSession) cleanup() *skerrors.StackKitError {
	if s == nil || s.handoffCommitted {
		return nil
	}
	return s.technical.Cleanup(context.Background(), s.client, s.baseURL)
}

func (s *cloudreveOwnerSession) handoffPayload() (json.RawMessage, string, *skerrors.StackKitError) {
	if s == nil {
		return nil, "", skerrors.NewDependencyError(
			"files_cloudreve_session_incomplete",
			"Cloudreve session response did not include a usable access token",
		)
	}
	if strings.TrimSpace(s.technical.AccessTokenForHandoff()) == "" {
		return nil, "", skerrors.NewDependencyError(
			"files_cloudreve_session_incomplete",
			"Cloudreve session response did not include a usable access token",
		)
	}
	return s.technical.LoginResponseForHandoff(), s.technical.UserID, nil
}

func (s *cloudreveOwnerSession) commitHandoff() {
	if s != nil {
		s.handoffCommitted = true
	}
}

func withCloudreveSessionCleanup(primary, cleanupErr *skerrors.StackKitError) *skerrors.StackKitError {
	if cleanupErr == nil {
		return primary
	}
	if primary == nil {
		return cleanupErr
	}
	if primary.Fields == nil {
		primary.Fields = make(map[string]interface{})
	}
	if cleanupErr.Code == "cloudreve_session_cleanup_unavailable" {
		primary.Fields["sessionCleanup"] = "unavailable"
	} else {
		primary.Fields["sessionCleanup"] = "failed"
	}
	return primary
}

func finishCloudreveOwnerSession(session *cloudreveOwnerSession, primary *skerrors.StackKitError) *skerrors.StackKitError {
	if session == nil {
		return primary
	}
	cleanupErr := session.cleanup()
	session.close()
	return withCloudreveSessionCleanup(primary, cleanupErr)
}

func (s *Server) handleFilesSessionBridge(w http.ResponseWriter, r *http.Request) {
	if !s.filesSessionBridgeRequestAuthorized(r) {
		writeStructuredError(w, r, http.StatusForbidden, skerrors.NewAuthError(
			"files_session_bridge_untrusted_route",
			"Files session bridge must be reached through the generated TinyAuth-protected Files route",
			skerrors.WithSuggestion("Open the generated Files URL instead of calling the node-local API port directly"),
		))
		return
	}

	forwardedEmail := forwardedIdentityEmail(r)
	if forwardedEmail == "" {
		writeStructuredError(w, r, http.StatusUnauthorized, skerrors.NewAuthError(
			"files_session_identity_missing",
			"Files session bridge requires TinyAuth/PocketID forwarded identity headers",
			skerrors.WithSuggestion("Open Files through the generated route so TinyAuth can authenticate the Owner first"),
		))
		return
	}

	owner, ownerErr := s.resolvePocketIDOwner(r.Context())
	if ownerErr != nil {
		writeStructuredError(w, r, setupHTTPStatus(ownerErr), ownerErr)
		return
	}
	ownerEmail := strings.TrimSpace(owner.Email)
	if ownerEmail == "" {
		writeStructuredError(w, r, http.StatusConflict, skerrors.NewValidationError(
			"files_pocketid_owner_missing",
			"Files session bridge requires an activated PocketID Owner user",
			skerrors.WithSuggestion("Create the PocketID Owner/passkey first, then open Files again"),
		))
		return
	}
	if !strings.EqualFold(forwardedEmail, ownerEmail) {
		writeStructuredError(w, r, http.StatusForbidden, skerrors.NewAuthError(
			"files_session_owner_mismatch",
			"TinyAuth identity does not match the activated PocketID Owner",
			skerrors.WithField("forwardedEmail", forwardedEmail),
			skerrors.WithField("ownerEmail", ownerEmail),
		))
		return
	}

	session, bridgeErr := s.prepareCloudreveOwnerSession(r.Context(), ownerEmail)
	if bridgeErr != nil {
		writeStructuredError(w, r, setupHTTPStatus(bridgeErr), bridgeErr)
		return
	}
	defer func() {
		if session != nil && !session.handoffCommitted {
			_ = finishCloudreveOwnerSession(session, nil)
		} else if session != nil {
			session.close()
		}
	}()
	login, userID, handoffErr := session.handoffPayload()
	if handoffErr != nil {
		writeStructuredError(w, r, setupHTTPStatus(handoffErr), handoffErr)
		return
	}
	if cloudreveDemoDataEnabled(s.config.BaseDir) {
		if err := ensureCloudreveOwnerDemoContent(r.Context(), session.client, session.baseURL, session.technical.AccessTokenForHandoff()); err != nil {
			bridgeErr := finishCloudreveOwnerSession(session, cloudreveStackKitError("files_cloudreve_owner_demo_seed_failed", "failed to seed Cloudreve demo content for the PocketID Owner", err))
			writeStructuredError(w, r, setupHTTPStatus(bridgeErr), bridgeErr)
			return
		}
	}
	nonce, nonceErr := newScriptNonce()
	if nonceErr != nil {
		writeStructuredError(w, r, http.StatusInternalServerError, skerrors.NewDeploymentError(
			"files_session_nonce_failed",
			"failed to prepare the Files session bridge response",
			skerrors.WithCause(nonceErr),
		))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'nonce-%s'; base-uri 'none'; frame-ancestors 'none'", nonce))
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(http.StatusOK)
	body := renderCloudreveSessionBridgeHTML(login, userID, nonce)
	if written, writeErr := w.Write(body); writeErr == nil && written == len(body) {
		session.commitHandoff()
	}
}

func (s *Server) filesSessionBridgeRequestAuthorized(r *http.Request) bool {
	expected := strings.TrimSpace(s.config.FilesSessionBridgeToken)
	if expected == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get(filesSessionBridgeHeader))
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func forwardedIdentityEmail(r *http.Request) string {
	for _, key := range []string{"remote-email", "Remote-Email", "X-Email", "X-Forwarded-Email"} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func (s *Server) prepareCloudreveOwnerSession(ctx context.Context, ownerEmail string) (*cloudreveOwnerSession, *skerrors.StackKitError) {
	password := s.config.SetupAdminPassword
	if strings.TrimSpace(password) == "" {
		return nil, skerrors.NewValidationError(
			"files_session_app_password_missing",
			"Files session bridge requires generated StackKit app-local bootstrap material",
			skerrors.WithSuggestion("Re-run stackkit generate/apply so STACKKIT_ADMIN_PASSWORD is available to stackkit-server"),
		)
	}
	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupCloudreveURL, "http://cloudreve:5212"), "/")
	client := appsetup.NewCloudreveHTTPClient()
	technical, setupErr := appsetup.BootstrapCloudreveOwner(ctx, client, baseURL, appsetup.CloudreveOwnerRequest{
		Email:                       ownerEmail,
		Password:                    password,
		Language:                    "en-US",
		ExpectedVersion:             appsetup.CloudrevePinnedVersion,
		AllowFirstOwnerRegistration: true,
	})
	if setupErr != nil {
		client.CloseIdleConnections()
		return nil, setupErr
	}
	session := &cloudreveOwnerSession{technical: technical, client: client, baseURL: baseURL}
	return session, nil
}

func ensureCloudreveOwnerAccount(ctx context.Context, client *http.Client, baseURL, email, password string) *cloudreveAPIError {
	return appsetup.EnsureCloudreveOwnerAccount(ctx, client, baseURL, email, password, "en-US")
}

func cloudreveLogin(ctx context.Context, client *http.Client, baseURL, email, password string) (json.RawMessage, cloudreveLoginResponse, *cloudreveAPIError) {
	return appsetup.CloudreveLogin(ctx, client, baseURL, email, password)
}

func ensureCloudreveOwnerDemoContent(ctx context.Context, client *http.Client, baseURL, token string) *cloudreveAPIError {
	rootURI := "cloudreve://my"
	folderURI := rootURI + "/" + url.PathEscape(cloudreveDemoFolderName)
	fileURI := folderURI + "/" + url.PathEscape(cloudreveDemoFileName)
	if err := ensureCloudreveFolder(ctx, client, baseURL, token, rootURI, cloudreveDemoFolderName, folderURI); err != nil {
		return err
	}
	if err := ensureCloudreveFile(ctx, client, baseURL, token, folderURI, cloudreveDemoFileName, fileURI); err != nil {
		return err
	}
	query := url.Values{"uri": {fileURI}}.Encode()
	_, err := cloudreveRaw(ctx, client, http.MethodPut, baseURL, "/file/content?"+query, token, "application/octet-stream", []byte(cloudreveDemoFileBody))
	return err
}

func ensureCloudreveFolder(ctx context.Context, client *http.Client, baseURL, token, parentURI, name, folderURI string) *cloudreveAPIError {
	return ensureCloudreveEntry(ctx, client, baseURL, token, parentURI, name, folderURI, 1, "folder")
}

func ensureCloudreveFile(ctx context.Context, client *http.Client, baseURL, token, parentURI, name, fileURI string) *cloudreveAPIError {
	return ensureCloudreveEntry(ctx, client, baseURL, token, parentURI, name, fileURI, 0, "file")
}

func ensureCloudreveEntry(ctx context.Context, client *http.Client, baseURL, token, parentURI, name, targetURI string, entryType int, cloudreveType string) *cloudreveAPIError {
	files, err := cloudreveListFiles(ctx, client, baseURL, token, parentURI)
	if err != nil {
		return err
	}
	for _, file := range files.Files {
		if file.Type == entryType && file.Name == name {
			return nil
		}
	}
	_, err = cloudreveJSON(ctx, client, http.MethodPost, baseURL, "/file/create", token, map[string]any{
		"type":            cloudreveType,
		"uri":             targetURI,
		"err_on_conflict": true,
	})
	if cloudreveAlreadyExists(err) {
		return nil
	}
	return err
}

func cloudreveListFiles(ctx context.Context, client *http.Client, baseURL, token, uri string) (cloudreveFileList, *cloudreveAPIError) {
	query := url.Values{"uri": {uri}, "page_size": {"200"}}.Encode()
	raw, err := cloudreveJSON(ctx, client, http.MethodGet, baseURL, "/file?"+query, token, nil)
	if err != nil {
		return cloudreveFileList{}, err
	}
	var files cloudreveFileList
	if err := json.Unmarshal(raw, &files); err != nil {
		return cloudreveFileList{}, &cloudreveAPIError{Message: "failed to parse Cloudreve file list", Cause: err}
	}
	return files, nil
}

func cloudreveJSON(ctx context.Context, client *http.Client, method, baseURL, path, token string, payload any) (json.RawMessage, *cloudreveAPIError) {
	return appsetup.CloudreveJSON(ctx, client, method, baseURL, path, token, payload)
}

func cloudreveRaw(ctx context.Context, client *http.Client, method, baseURL, path, token, contentType string, body []byte) (json.RawMessage, *cloudreveAPIError) {
	return appsetup.CloudreveRaw(ctx, client, method, baseURL, path, token, contentType, body)
}

func cloudreveAlreadyExists(err *cloudreveAPIError) bool {
	return appsetup.CloudreveAlreadyExists(err)
}

func parseCloudreveErrorBody(raw []byte) (int, string) {
	return appsetup.ParseCloudreveErrorBody(raw)
}

func cloudreveStackKitError(code, message string, err *cloudreveAPIError) *skerrors.StackKitError {
	if err == nil {
		return nil
	}
	fields := []skerrors.ErrorOption{}
	if err.HTTPStatus != 0 {
		fields = append(fields, skerrors.WithField("status", err.HTTPStatus))
	}
	if err.Code != 0 {
		fields = append(fields, skerrors.WithField("cloudreveCode", err.Code))
	}
	// Application bodies and transport causes can reflect session tokens or
	// credentials. Keep legacy handoff errors within the same bounded public
	// diagnostic contract as native owner setup.
	return skerrors.NewDependencyError(code, message, fields...)
}

func cloudreveDemoDataEnabled(baseDir string) bool {
	_, tfvars, err := loadBaseHubTFVars(baseDir)
	if err != nil {
		return true
	}
	return boolTFVar(tfvars, "demo_data_enabled", true)
}

func renderCloudreveSessionBridgeHTML(login json.RawMessage, userID, nonce string) []byte {
	userIDJSON, _ := json.Marshal(userID)
	loginJSON := jsonForInlineScript(login)
	currentUserJSON := jsonForInlineScript(userIDJSON)
	return []byte(fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="robots" content="noindex">
  <title>Opening Files</title>
</head>
<body>
  <script nonce="%s">
    const login = %s;
    const userId = %s;
    const key = "cloudreve_session";
    const bridgeKey = "stackkit_files_session_bridge";
    const current = JSON.parse(localStorage.getItem(key) || "{}");
    const sessions = current.sessions && typeof current.sessions === "object" ? current.sessions : {};
    sessions[userId] = Object.assign({}, login, { settings: login.user && login.user.settings ? login.user.settings : {} });
    localStorage.setItem(key, JSON.stringify(Object.assign({}, current, { current: userId, sessions })));
    localStorage.setItem(bridgeKey, JSON.stringify({ verification: "stackkit-cloudreve-session-bridge", current: userId }));
    window.location.replace("/home");
  </script>
</body>
</html>
`, htmlAttrEscape(nonce), loginJSON, currentUserJSON))
}

func newScriptNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func htmlAttrEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		`"`, "&quot;",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(value)
}

func jsonForInlineScript(raw []byte) string {
	text := string(raw)
	replacer := strings.NewReplacer(
		"<", `\u003c`,
		">", `\u003e`,
		"&", `\u0026`,
		"\u2028", `\u2028`,
		"\u2029", `\u2029`,
	)
	return replacer.Replace(text)
}
