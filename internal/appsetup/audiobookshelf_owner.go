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

const AudiobookshelfOwnerBootstrapAction = "audiobookshelf-owner-bootstrap"

type AudiobookshelfOwnerRequest struct {
	Username, Password, ExpectedVersion string
	AllowFirstOwnerRegistration         bool
	OIDC                                *AudiobookshelfOIDCRequest
}

type AudiobookshelfOwnerResult struct {
	AccountRef    string `json:"accountRef"`
	LibraryRef    string `json:"libraryRef"`
	Version       string `json:"version"`
	AdminVerified bool   `json:"adminVerified"`
	LibraryReady  bool   `json:"libraryReady"`
	ScanRequested bool   `json:"scanRequested"`
}

// BootstrapAudiobookshelfOwner uses the upstream first-root guard and normal
// APIs. It never resets an existing password, edits an existing library, or
// operates outside the admitted read-only /audiobooks mount. Its short-lived
// API session is logged out before any successful result is returned.
func BootstrapAudiobookshelfOwner(ctx context.Context, client *http.Client, baseURL string, request AudiobookshelfOwnerRequest) (result AudiobookshelfOwnerResult, returnErr error) {
	if ctx == nil || client == nil || client.Transport == nil {
		return result, errors.New("Audiobookshelf setup requires the admitted HTTP transport and bounded context")
	}
	if strings.TrimSpace(request.Username) == "" || len(request.Username) > 128 || strings.ContainsAny(request.Username, "\r\n\x00") || len(request.Password) == 0 || len(request.Password) > 4096 || strings.ContainsAny(request.Password, "\r\n\x00") || request.ExpectedVersion == "" {
		return result, errors.New("Audiobookshelf setup requires explicit private owner credentials and the applied version")
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return result, errors.New("Audiobookshelf setup requires the admitted application endpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	session := &http.Client{Transport: client.Transport, Timeout: client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(ctx context.Context, method, path, token, refresh string, payload any, out any) error {
		var body []byte
		if payload != nil {
			var err error
			body, err = json.Marshal(payload)
			if err != nil {
				return errors.New("cannot construct Audiobookshelf setup request")
			}
		}
		defer clear(body)
		req, err := http.NewRequestWithContext(ctx, method, base.String()+path, bytes.NewReader(body))
		if err != nil {
			return errors.New("cannot construct Audiobookshelf setup request")
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Return-Tokens", "true")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if refresh != "" {
			req.Header.Set("X-Refresh-Token", refresh)
		}
		resp, err := session.Do(req)
		if err != nil {
			return fmt.Errorf("Audiobookshelf setup %s %s request failed", method, strings.SplitN(path, "?", 2)[0])
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		defer clear(raw)
		if err != nil || len(raw) > 1<<20 {
			return errors.New("Audiobookshelf setup response exceeded its boundary")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("Audiobookshelf setup %s refused (HTTP %d)", path, resp.StatusCode)
		}
		if out != nil && json.Unmarshal(raw, out) != nil {
			return errors.New("Audiobookshelf setup response was invalid")
		}
		return nil
	}
	var status struct {
		App, ServerVersion string
		IsInit             bool
	}
	if err := call(ctx, http.MethodGet, "/status", "", "", nil, &status); err != nil {
		return result, err
	}
	if status.App != "audiobookshelf" || status.ServerVersion != request.ExpectedVersion {
		return result, errors.New("Audiobookshelf API version differs from the applied workload")
	}
	credentials := map[string]string{"username": request.Username, "password": request.Password}
	defer delete(credentials, "password")
	if !status.IsInit {
		if !request.AllowFirstOwnerRegistration {
			return result, errors.New("Audiobookshelf first owner registration was not admitted")
		}
		// Upstream refuses /init once any root user exists. No password-reset
		// or user-administration fallback follows a refusal.
		if err := call(ctx, http.MethodPost, "/init", "", "", map[string]any{"newRoot": credentials}, nil); err != nil {
			return result, err
		}
	}
	type account struct {
		ID, Username, Type string
		IsActive           bool
	}
	var login struct {
		User struct {
			account
			AccessToken, RefreshToken string
		}
	}
	if err := call(ctx, http.MethodPost, "/login", "", "", credentials, &login); err != nil {
		return result, err
	}
	defer func() {
		if login.User.RefreshToken != "" {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer stop()
			if err := call(cleanup, http.MethodPost, "/logout", "", login.User.RefreshToken, nil, nil); err != nil {
				result = AudiobookshelfOwnerResult{}
				returnErr = errors.Join(returnErr, err)
			}
		}
		login.User.AccessToken, login.User.RefreshToken = "", ""
	}()
	admin := func(a account) bool {
		return a.ID != "" && a.Username == request.Username && a.IsActive && (a.Type == "root" || a.Type == "admin")
	}
	if !admin(login.User.account) || login.User.AccessToken == "" || login.User.RefreshToken == "" || strings.ContainsAny(login.User.AccessToken+login.User.RefreshToken, "\r\n") {
		return result, errors.New("Audiobookshelf did not authenticate the exact active administrator")
	}
	var owner account
	if err := call(ctx, http.MethodGet, "/api/me", login.User.AccessToken, "", nil, &owner); err != nil {
		return result, err
	}
	if !admin(owner) || owner.ID != login.User.ID {
		return result, errors.New("Audiobookshelf administrator readback differs from the authenticated owner")
	}
	if request.OIDC != nil {
		if err := configureAudiobookshelfOIDC(ctx, call, login.User.AccessToken, *request.OIDC); err != nil {
			return result, err
		}
	}
	type library struct {
		ID, MediaType string
		Folders       []struct{ FullPath string }
	}
	var libraries struct{ Libraries []library }
	readLibraries := func() error {
		return call(ctx, http.MethodGet, "/api/libraries", login.User.AccessToken, "", nil, &libraries)
	}
	if err := readLibraries(); err != nil {
		return result, err
	}
	selectLibrary := func() (string, error) {
		id := ""
		for _, lib := range libraries.Libraries {
			for _, folder := range lib.Folders {
				if folder.FullPath != "/audiobooks" {
					continue
				}
				if lib.ID == "" || lib.MediaType != "book" || len(lib.Folders) != 1 || id != "" {
					return "", errors.New("Audiobookshelf library is ambiguous or exceeds the admitted audiobook mount")
				}
				id = lib.ID
			}
		}
		return id, nil
	}
	id, err := selectLibrary()
	if err != nil {
		return result, err
	}
	if id == "" {
		payload := map[string]any{"name": "Audiobooks", "mediaType": "book", "folders": []map[string]string{{"fullPath": "/audiobooks"}}, "settings": map[string]bool{"audiobooksOnly": true, "epubsAllowScriptedContent": false}}
		if err := call(ctx, http.MethodPost, "/api/libraries", login.User.AccessToken, "", payload, nil); err != nil {
			return result, err
		}
		if err := readLibraries(); err != nil {
			return result, err
		}
		id, err = selectLibrary()
		if err != nil || id == "" {
			return result, errors.New("Audiobookshelf did not register exactly the admitted audiobook library")
		}
	}
	if err := call(ctx, http.MethodPost, "/api/libraries/"+url.PathEscape(id)+"/scan", login.User.AccessToken, "", nil, nil); err != nil {
		return result, err
	}
	return AudiobookshelfOwnerResult{AccountRef: owner.ID, LibraryRef: id, Version: status.ServerVersion, AdminVerified: true, LibraryReady: true, ScanRequested: true}, nil
}
