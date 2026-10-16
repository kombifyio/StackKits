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

const NavidromeOwnerBootstrapAction = "navidrome-owner-bootstrap"

type NavidromeOwnerRequest struct {
	Username                    string
	Password                    string
	ExpectedVersion             string
	AllowFirstOwnerRegistration bool
}

type NavidromeOwnerResult struct {
	AccountRef    string `json:"accountRef"`
	Username      string `json:"username"`
	Version       string `json:"version"`
	AdminVerified bool   `json:"adminVerified"`
	LibraryReady  bool   `json:"libraryReady"`
	ScanRequested bool   `json:"scanRequested"`
}

// BootstrapNavidromeOwner uses the pinned application's first-admin guard,
// ordinary login and administrator/library readback. It never changes an
// existing password or creates a library outside the admitted /music mount.
// Navidrome has no server-side JWT logout API; its temporary JWT is kept only
// inside this call and discarded, never returned as evidence or client custody.
func BootstrapNavidromeOwner(ctx context.Context, client *http.Client, baseURL string, request NavidromeOwnerRequest) (NavidromeOwnerResult, error) {
	if ctx == nil || client == nil || client.Transport == nil {
		return NavidromeOwnerResult{}, errors.New("Navidrome setup requires the admitted HTTP transport and bounded context")
	}
	if strings.TrimSpace(request.Username) == "" || len(request.Username) > 128 || strings.ContainsAny(request.Username, "\r\n\x00") || len(request.Password) == 0 || len(request.Password) > 4096 || strings.ContainsAny(request.Password, "\r\n\x00") || request.ExpectedVersion == "" {
		return NavidromeOwnerResult{}, errors.New("Navidrome setup requires explicit private owner credentials and the applied version")
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return NavidromeOwnerResult{}, errors.New("Navidrome setup requires the admitted application endpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	session := &http.Client{Transport: client.Transport, Timeout: client.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path, token, contentType string, payload []byte, out any) (int, error) {
		req, err := http.NewRequestWithContext(ctx, method, base.String()+path, bytes.NewReader(payload))
		if err != nil {
			return 0, errors.New("cannot construct Navidrome setup request")
		}
		req.Header.Set("Accept", "application/json")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			req.Header.Set("X-ND-Authorization", "Bearer "+token)
		}
		resp, err := session.Do(req)
		if err != nil {
			return 0, errors.New("Navidrome setup request failed")
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		defer clear(raw)
		if err != nil || len(raw) > 1<<20 {
			return resp.StatusCode, errors.New("Navidrome setup response exceeded its boundary")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resp.StatusCode, fmt.Errorf("Navidrome setup refused (HTTP %d)", resp.StatusCode)
		}
		if out != nil && json.Unmarshal(raw, out) != nil {
			return resp.StatusCode, errors.New("Navidrome setup response was invalid")
		}
		return resp.StatusCode, nil
	}
	credentials, err := json.Marshal(map[string]string{"username": request.Username, "password": request.Password})
	if err != nil {
		return NavidromeOwnerResult{}, errors.New("cannot construct private Navidrome credentials")
	}
	defer clear(credentials)
	var login struct {
		ID            string `json:"id"`
		Username      string `json:"username"`
		IsAdmin       bool   `json:"isAdmin"`
		Token         string `json:"token"`
		SubsonicSalt  string `json:"subsonicSalt"`
		SubsonicToken string `json:"subsonicToken"`
	}
	defer func() { login.Token = ""; login.SubsonicToken = ""; login.SubsonicSalt = "" }()
	status, err := call(http.MethodPost, "/auth/login", "", "application/json", credentials, &login)
	if err != nil && status == http.StatusUnauthorized && request.AllowFirstOwnerRegistration {
		// Upstream /createAdmin refuses registration once any user exists.
		// A refusal is final: there is no default login, reset or user CRUD fallback.
		_, err = call(http.MethodPost, "/auth/createAdmin", "", "application/json", credentials, &login)
	}
	if err != nil {
		return NavidromeOwnerResult{}, err
	}
	if login.ID == "" || login.Username != request.Username || !login.IsAdmin || login.Token == "" || strings.ContainsAny(login.Token, "\r\n") {
		return NavidromeOwnerResult{}, errors.New("Navidrome did not authenticate the exact administrator")
	}
	var owner struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if _, err := call(http.MethodGet, "/api/user/"+url.PathEscape(login.ID), login.Token, "", nil, &owner); err != nil {
		return NavidromeOwnerResult{}, err
	}
	if owner.ID != login.ID || owner.UserName != request.Username || !owner.IsAdmin {
		return NavidromeOwnerResult{}, errors.New("Navidrome administrator readback differs from the authenticated owner")
	}
	var libraries []struct {
		ID   int    `json:"id"`
		Path string `json:"path"`
	}
	if _, err := call(http.MethodGet, "/api/library", login.Token, "", nil, &libraries); err != nil {
		return NavidromeOwnerResult{}, err
	}
	libraryReady := false
	for _, library := range libraries {
		libraryReady = libraryReady || (library.ID > 0 && library.Path == "/music")
	}
	if !libraryReady {
		return NavidromeOwnerResult{}, errors.New("Navidrome has not registered the admitted read-only music library")
	}
	// The upstream login issues the Subsonic token/salt as well as its JWT.
	// Use that session rather than implementing another credential derivation.
	// POST keeps those values out of URLs, diagnostics and evidence.
	if login.SubsonicSalt == "" || login.SubsonicToken == "" {
		return NavidromeOwnerResult{}, errors.New("Navidrome did not issue its native music API session")
	}
	form := []byte(url.Values{"u": {request.Username}, "t": {login.SubsonicToken}, "s": {login.SubsonicSalt}, "v": {"1.16.1"}, "c": {"stackkit-setup"}, "f": {"json"}}.Encode())
	defer clear(form)
	var ping struct {
		Response struct {
			Status        string `json:"status"`
			ServerVersion string `json:"serverVersion"`
		} `json:"subsonic-response"`
	}
	if _, err := call(http.MethodPost, "/rest/ping.view", "", "application/x-www-form-urlencoded", form, &ping); err != nil {
		return NavidromeOwnerResult{}, err
	}
	version := strings.Fields(ping.Response.ServerVersion)
	if ping.Response.Status != "ok" || len(version) == 0 || version[0] != request.ExpectedVersion {
		return NavidromeOwnerResult{}, errors.New("Navidrome API version differs from the applied workload")
	}
	var scan struct {
		Response struct {
			Status string `json:"status"`
		} `json:"subsonic-response"`
	}
	if _, err := call(http.MethodPost, "/rest/startScan.view", "", "application/x-www-form-urlencoded", form, &scan); err != nil {
		return NavidromeOwnerResult{}, err
	}
	if scan.Response.Status != "ok" {
		return NavidromeOwnerResult{}, errors.New("Navidrome did not accept its native library scan")
	}
	return NavidromeOwnerResult{AccountRef: login.ID, Username: login.Username, Version: version[0], AdminVerified: true, LibraryReady: true, ScanRequested: true}, nil
}
