package appsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const cloudreveResponseBodyLimit = 1 << 20

// NewCloudreveHTTPClient returns the bounded client used by the local
// Cloudreve setup adapter. Credentials and bearer tokens must not be sent to
// an environment-controlled proxy or followed across a redirect.
func NewCloudreveHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Cloudreve setup refuses HTTP redirects")
		},
	}
}

// CloudreveAPIError is the bounded error returned by the shared Cloudreve
// transport. Response bodies are never retained, because they can contain
// session or credential material.
type CloudreveAPIError struct {
	HTTPStatus int
	Code       int
	Message    string
	Cause      error
}

func (e *CloudreveAPIError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "Cloudreve API request failed"
	}
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s (HTTP %d)", message, e.HTTPStatus)
	}
	return message
}

func (e *CloudreveAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// CloudreveJSON sends one JSON request through the shared v4 envelope parser.
// The returned bytes are the envelope's data value, never the complete
// response body.
func CloudreveJSON(ctx context.Context, client *http.Client, method, baseURL, path, token string, payload any) (json.RawMessage, *CloudreveAPIError) {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, &CloudreveAPIError{Message: "failed to encode Cloudreve request", Cause: err}
		}
	}
	return CloudreveRaw(ctx, client, method, baseURL, path, token, "application/json", body)
}

// CloudreveRaw sends one request through the shared Cloudreve v4 envelope
// parser. It is also used for the existing binary file-content handoff.
func CloudreveRaw(ctx context.Context, client *http.Client, method, baseURL, path, token, contentType string, body []byte) (json.RawMessage, *CloudreveAPIError) {
	endpoint, err := cloudreveEndpointURL(baseURL, path)
	if err != nil {
		return nil, &CloudreveAPIError{Message: "invalid Cloudreve URL", Cause: err}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = NewCloudreveHTTPClient()
		defer client.CloseIdleConnections()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &CloudreveAPIError{Message: "invalid Cloudreve URL", Cause: err}
	}
	if len(body) > 0 && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &CloudreveAPIError{Message: "Cloudreve API is unreachable", Cause: err}
	}
	defer resp.Body.Close()
	rawBody, readErr := io.ReadAll(io.LimitReader(resp.Body, cloudreveResponseBodyLimit+1))
	if readErr != nil {
		return nil, &CloudreveAPIError{HTTPStatus: resp.StatusCode, Message: "failed to read Cloudreve response", Cause: readErr}
	}
	if len(rawBody) > cloudreveResponseBodyLimit {
		return nil, &CloudreveAPIError{HTTPStatus: resp.StatusCode, Message: "Cloudreve response exceeded the bounded setup limit"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, message := ParseCloudreveErrorBody(rawBody)
		return nil, &CloudreveAPIError{HTTPStatus: resp.StatusCode, Code: code, Message: message}
	}
	if len(bytes.TrimSpace(rawBody)) == 0 {
		return nil, nil
	}
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return nil, &CloudreveAPIError{HTTPStatus: resp.StatusCode, Message: "failed to parse Cloudreve response", Cause: err}
	}
	if envelope.Code != 0 {
		return nil, &CloudreveAPIError{HTTPStatus: resp.StatusCode, Code: envelope.Code, Message: envelope.Msg}
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return json.RawMessage(`{}`), nil
	}
	return envelope.Data, nil
}

// CloudreveAlreadyExists reports the registration idempotency response used
// by Cloudreve v4. It intentionally accepts the message form as well because
// older compatible builds have returned the same condition with a different
// HTTP status.
func CloudreveAlreadyExists(err *CloudreveAPIError) bool {
	if err == nil {
		return false
	}
	if err.Code == 40032 {
		return true
	}
	message := strings.ToLower(err.Message)
	return strings.Contains(message, "already") || strings.Contains(message, "exist") || strings.Contains(message, "in use")
}

// CloudreveOwnerNotFound reports only Cloudreve v4's documented user-not-found
// code. Other login errors, including free-form messages that happen to say
// "not found", must not trigger an account-creation mutation.
func CloudreveOwnerNotFound(err *CloudreveAPIError) bool {
	return err != nil && err.Code == 40021
}

// ParseCloudreveErrorBody extracts only the public code/message fields from a
// failed response. The body itself is never returned as an error field when
// an envelope is present.
func ParseCloudreveErrorBody(raw []byte) (int, string) {
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

func cloudreveEndpointURL(baseURL, path string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "", errors.New("Cloudreve setup requires an absolute HTTP(S) URL")
	}
	baseParsed, err := url.Parse(base)
	if err != nil || baseParsed.Scheme == "" || baseParsed.Host == "" {
		return "", errors.New("Cloudreve setup requires an absolute HTTP(S) URL")
	}
	if baseParsed.Scheme != "http" && baseParsed.Scheme != "https" {
		return "", errors.New("Cloudreve setup accepts only HTTP(S) URLs")
	}
	if baseParsed.User != nil || baseParsed.RawQuery != "" || baseParsed.Fragment != "" {
		return "", errors.New("Cloudreve setup URL cannot contain credentials, query, or fragment data")
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return "", errors.New("Cloudreve setup endpoint must be an absolute path")
	}
	endpoint, err := url.Parse(path)
	if err != nil || endpoint.Fragment != "" {
		return "", errors.New("Cloudreve setup endpoint is invalid")
	}
	baseParsed.Path = strings.TrimRight(baseParsed.Path, "/") + "/api/v4" + endpoint.Path
	baseParsed.RawPath = ""
	baseParsed.RawQuery = endpoint.RawQuery
	baseParsed.Fragment = ""
	return baseParsed.String(), nil
}
