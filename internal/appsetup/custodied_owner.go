package appsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	PaperlessOwnerLoginAction = "paperless-owner-login"
	GiteaOwnerLoginAction     = "gitea-owner-login"
	ForgejoOwnerLoginAction   = "forgejo-owner-login"
)

// CustodiedOwnerLoginAction needs no credential document: application startup
// already consumes the selected workload's owner-password custody.
func CustodiedOwnerLoginAction(action string) bool {
	switch action {
	case PaperlessOwnerLoginAction, GiteaOwnerLoginAction, ForgejoOwnerLoginAction, GuacamoleOwnerLoginAction:
		return true
	}
	return false
}

type CustodiedOwnerLoginRequest struct {
	Action   string
	Email    string
	Password []byte
}

type CustodiedOwnerLoginResult struct {
	AccountRef string
}

// VerifyCustodiedOwnerLogin observes the existing administrator using the
// pinned application's Basic-auth API. It never creates an account, changes a
// password or follows a redirect with credentials. Guacamole alone requires a
// temporary token, which is revoked before returning. The transport
// is the runtime adapter's already-admitted container/loopback session.
func VerifyCustodiedOwnerLogin(ctx context.Context, client *http.Client, baseURL string, request CustodiedOwnerLoginRequest) (CustodiedOwnerLoginResult, error) {
	if ctx == nil {
		return CustodiedOwnerLoginResult{}, errors.New("owner observation requires a bounded caller context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !CustodiedOwnerLoginAction(request.Action) || len(request.Password) == 0 || len(request.Password) > 4096 || strings.ContainsAny(string(request.Password), "\x00\r\n") {
		return CustodiedOwnerLoginResult{}, errors.New("owner login requires the selected application's existing password custody")
	}
	base, err := url.Parse(baseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || client == nil || client.Transport == nil {
		return CustodiedOwnerLoginResult{}, errors.New("owner login requires the admitted application HTTP endpoint and transport")
	}
	session := &http.Client{Transport: client.Transport, Timeout: client.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if request.Action == GuacamoleOwnerLoginAction {
		return verifyGuacamoleOwnerLogin(ctx, session, base, request.Password)
	}
	get := func(path string, destination any) error {
		endpoint := strings.TrimRight(base.String(), "/") + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return errors.New("cannot construct the application owner observation")
		}
		req.SetBasicAuth("owner", string(request.Password))
		req.Header.Set("Accept", "application/json")
		resp, err := session.Do(req)
		if err != nil {
			return errors.New("application owner authentication request failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("application owner authentication was refused (HTTP %d); retain the existing account and custody", resp.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if err != nil || len(raw) > 1<<20 {
			return errors.New("application owner observation exceeds its response boundary")
		}
		defer clear(raw)
		if err := json.Unmarshal(raw, destination); err != nil {
			return errors.New("application owner observation is invalid")
		}
		return nil
	}
	if request.Action == PaperlessOwnerLoginAction {
		var result struct {
			Results []struct {
				ID        int64  `json:"id"`
				Username  string `json:"username"`
				Active    bool   `json:"is_active"`
				Staff     bool   `json:"is_staff"`
				Superuser bool   `json:"is_superuser"`
			} `json:"results"`
		}
		if err := get("/api/users/?username__iexact=owner", &result); err != nil {
			return CustodiedOwnerLoginResult{}, err
		}
		for _, row := range result.Results {
			if row.ID > 0 && row.Username == "owner" && row.Active && row.Staff && row.Superuser {
				return CustodiedOwnerLoginResult{AccountRef: strconv.FormatInt(row.ID, 10)}, nil
			}
		}
		return CustodiedOwnerLoginResult{}, errors.New("Paperless did not verify the active custodied administrator")
	}
	if strings.TrimSpace(request.Email) == "" {
		return CustodiedOwnerLoginResult{}, errors.New("Git owner observation requires the existing local owner email")
	}
	var owner struct {
		ID            int64  `json:"id"`
		Username      string `json:"login"`
		Email         string `json:"email"`
		Active        bool   `json:"active"`
		Admin         bool   `json:"is_admin"`
		ProhibitLogin bool   `json:"prohibit_login"`
	}
	if err := get("/api/v1/user", &owner); err != nil {
		return CustodiedOwnerLoginResult{}, err
	}
	if owner.ID <= 0 || owner.Username != "owner" || owner.Email != strings.TrimSpace(request.Email) || !owner.Active || !owner.Admin || owner.ProhibitLogin {
		return CustodiedOwnerLoginResult{}, errors.New("Git application did not verify the active administrator bound to local owner custody")
	}
	return CustodiedOwnerLoginResult{AccountRef: strconv.FormatInt(owner.ID, 10)}, nil
}
