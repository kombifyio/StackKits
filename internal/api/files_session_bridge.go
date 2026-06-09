package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	skerrors "github.com/kombifyio/stackkits/internal/errors"
)

const (
	cloudreveDemoFolderName  = "StackKit Demo"
	cloudreveDemoFileName    = "README.txt"
	cloudreveDemoFileBody    = "Welcome to your StackKit Files workspace.\nThis demo file is created automatically during the BaseKit beta bootstrap.\n"
	filesSessionBridgeHeader = "X-StackKit-Files-Bridge"
)

type cloudreveAPIError struct {
	HTTPStatus int
	Code       int
	Message    string
	Cause      error
}

type cloudreveLoginResponse struct {
	User struct {
		ID    json.RawMessage `json:"id"`
		Email string          `json:"email"`
	} `json:"user"`
	Token struct {
		AccessToken string `json:"access_token"`
	} `json:"token"`
}

type cloudreveFileList struct {
	Files []struct {
		Type int    `json:"type"`
		Name string `json:"name"`
	} `json:"files"`
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

	login, userID, bridgeErr := s.prepareCloudreveOwnerSession(r.Context(), ownerEmail)
	if bridgeErr != nil {
		writeStructuredError(w, r, setupHTTPStatus(bridgeErr), bridgeErr)
		return
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
	_, _ = w.Write(renderCloudreveSessionBridgeHTML(login, userID, nonce))
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

func (s *Server) prepareCloudreveOwnerSession(ctx context.Context, ownerEmail string) (json.RawMessage, string, *skerrors.StackKitError) {
	password := strings.TrimSpace(s.config.SetupAdminPassword)
	if password == "" {
		return nil, "", skerrors.NewValidationError(
			"files_session_app_password_missing",
			"Files session bridge requires generated StackKit app-local bootstrap material",
			skerrors.WithSuggestion("Re-run stackkit generate/apply so STACKKIT_ADMIN_PASSWORD is available to stackkit-server"),
		)
	}

	baseURL := strings.TrimRight(firstNonEmptyString(s.config.SetupCloudreveURL, "http://cloudreve:5212"), "/")
	client := &http.Client{Timeout: 20 * time.Second}
	login, parsed, err := cloudreveLogin(ctx, client, baseURL, ownerEmail, password)
	if err != nil {
		if createErr := ensureCloudreveOwnerAccount(ctx, client, baseURL, ownerEmail, password); createErr != nil {
			return nil, "", cloudreveStackKitError("files_cloudreve_owner_create_failed", "failed to prepare the Cloudreve app-local Owner account", createErr)
		}
		login, parsed, err = cloudreveLogin(ctx, client, baseURL, ownerEmail, password)
		if err != nil {
			return nil, "", cloudreveStackKitError("files_cloudreve_owner_login_failed", "failed to create a Cloudreve app-local session for the PocketID Owner", err)
		}
	}
	if !strings.EqualFold(strings.TrimSpace(parsed.User.Email), ownerEmail) {
		return nil, "", skerrors.NewAuthError(
			"files_cloudreve_owner_email_mismatch",
			"Cloudreve returned a session for a different email address",
			skerrors.WithField("ownerEmail", ownerEmail),
			skerrors.WithField("cloudreveEmail", parsed.User.Email),
		)
	}
	userID := jsonScalarString(parsed.User.ID)
	if userID == "" || strings.TrimSpace(parsed.Token.AccessToken) == "" {
		return nil, "", skerrors.NewDependencyError(
			"files_cloudreve_session_incomplete",
			"Cloudreve session response did not include a usable user id and access token",
		)
	}
	if cloudreveDemoDataEnabled(s.config.BaseDir) {
		if err := ensureCloudreveOwnerDemoContent(ctx, client, baseURL, parsed.Token.AccessToken); err != nil {
			return nil, "", cloudreveStackKitError("files_cloudreve_owner_demo_seed_failed", "failed to seed Cloudreve demo content for the PocketID Owner", err)
		}
	}
	return login, userID, nil
}

func ensureCloudreveOwnerAccount(ctx context.Context, client *http.Client, baseURL, email, password string) *cloudreveAPIError {
	payload := map[string]string{"email": email, "password": password, "language": "en-US"}
	_, err := cloudreveJSON(ctx, client, http.MethodPost, baseURL, "/user", "", payload)
	if err == nil || cloudreveAlreadyExists(err) {
		return nil
	}
	return err
}

func cloudreveLogin(ctx context.Context, client *http.Client, baseURL, email, password string) (json.RawMessage, cloudreveLoginResponse, *cloudreveAPIError) {
	payload := map[string]string{"email": email, "password": password}
	raw, err := cloudreveJSON(ctx, client, http.MethodPost, baseURL, "/session/token", "", payload)
	if err != nil {
		return nil, cloudreveLoginResponse{}, err
	}
	var parsed cloudreveLoginResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, cloudreveLoginResponse{}, &cloudreveAPIError{Message: "failed to parse Cloudreve session response", Cause: err}
	}
	return raw, parsed, nil
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
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, &cloudreveAPIError{Message: "failed to encode Cloudreve request", Cause: err}
		}
	}
	return cloudreveRaw(ctx, client, method, baseURL, path, token, "application/json", body)
}

func cloudreveRaw(ctx context.Context, client *http.Client, method, baseURL, path, token, contentType string, body []byte) (json.RawMessage, *cloudreveAPIError) {
	req, err := http.NewRequestWithContext(ctx, method, baseURL+"/api/v4"+path, bytes.NewReader(body))
	if err != nil {
		return nil, &cloudreveAPIError{Message: "invalid Cloudreve URL", Cause: err}
	}
	if len(body) > 0 && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &cloudreveAPIError{Message: "Cloudreve API is unreachable", Cause: err}
	}
	defer resp.Body.Close()
	rawBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, &cloudreveAPIError{HTTPStatus: resp.StatusCode, Message: "failed to read Cloudreve response", Cause: readErr}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, message := parseCloudreveErrorBody(rawBody)
		return nil, &cloudreveAPIError{HTTPStatus: resp.StatusCode, Code: code, Message: message}
	}
	if len(rawBody) == 0 {
		return nil, nil
	}
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return nil, &cloudreveAPIError{HTTPStatus: resp.StatusCode, Message: "failed to parse Cloudreve response", Cause: err}
	}
	if envelope.Code != 0 {
		return nil, &cloudreveAPIError{HTTPStatus: resp.StatusCode, Code: envelope.Code, Message: envelope.Msg}
	}
	if len(envelope.Data) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return envelope.Data, nil
}

func cloudreveAlreadyExists(err *cloudreveAPIError) bool {
	if err == nil {
		return false
	}
	if err.Code == 40032 {
		return true
	}
	msg := strings.ToLower(err.Message)
	return strings.Contains(msg, "already") || strings.Contains(msg, "exist") || strings.Contains(msg, "in use")
}

func parseCloudreveErrorBody(raw []byte) (int, string) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return 0, ""
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		message := strings.TrimSpace(envelope.Msg)
		if message != "" || envelope.Code != 0 {
			return envelope.Code, message
		}
	}
	return 0, text
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
	if strings.TrimSpace(err.Message) != "" {
		fields = append(fields, skerrors.WithField("cloudreveMessage", truncateForField(err.Message)))
	}
	if err.Cause != nil {
		fields = append(fields, skerrors.WithCause(err.Cause))
	}
	return skerrors.NewDependencyError(code, message, fields...)
}

func cloudreveDemoDataEnabled(baseDir string) bool {
	_, tfvars, err := loadBaseHubTFVars(baseDir)
	if err != nil {
		return true
	}
	return boolTFVar(tfvars, "demo_data_enabled", true)
}

func jsonScalarString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	return strings.Trim(string(raw), `"`)
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
    window.location.replace("/");
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
