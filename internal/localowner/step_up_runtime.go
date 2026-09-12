package localowner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/pocketid"
)

const StepUpCallbackPath = "/api/v1/identity/step-up/callback"
const stepUpSecretPath = ".stackkit/custody/owner-step-up-client-secret"

// PrepareStepUp binds a dedicated confidential PKCE client to an explicitly configured
// HTTPS server origin. Existing clients are verified, never silently rewritten.
func (s *Service) PrepareStepUp(ctx context.Context, origin string) (StepUpTrust, error) {
	return s.stepUpTrust(ctx, origin, true)
}

// ReadStepUpTrust verifies current owner, client and public signing keys without
// creating or changing identity resources. The remote peer enrollment owner may
// project this trust only through its existing authenticated custody contract.
func (s *Service) ReadStepUpTrust(ctx context.Context, origin string) (StepUpTrust, error) {
	return s.stepUpTrust(ctx, origin, false)
}

func (s *Service) stepUpTrust(ctx context.Context, origin string, create bool) (StepUpTrust, error) {
	origin, err := NormalizeStepUpOrigin(origin)
	if err != nil {
		return StepUpTrust{}, ErrStepUpRejected
	}
	binding, err := s.Verify(ctx)
	if err != nil {
		return StepUpTrust{}, err
	}
	owner, client, err := s.ready(ctx)
	if err != nil {
		return StepUpTrust{}, err
	}
	// Serialize first registration and secret custody across server processes.
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return StepUpTrust{}, err
	}
	defer root.Close()
	if create {
		tx, err := root.BeginTransaction()
		if err != nil {
			return StepUpTrust{}, err
		}
		defer tx.Close()
		lock, err := tx.TryAcquireOutputLock(stepUpSecretPath)
		if err != nil {
			return StepUpTrust{}, err
		}
		defer lock.Release()
	}
	registered, err := client.GetOIDCClient(ctx, StepUpClientID)
	if create && errors.Is(err, pocketid.ErrNotFound) {
		registered, err = client.RegisterOIDCClient(ctx, pocketid.RegisterClientRequest{
			ID: StepUpClientID, Name: "StackKits Owner action approval", CallbackURLs: []string{origin + StepUpCallbackPath},
			IsPublic: false, PkceEnabled: true, RequiresReauthentication: true, IsGroupRestricted: true,
		})
		if err != nil {
			return StepUpTrust{}, ErrStepUpRejected
		}
		if err = persistStepUpSecret(root, s.workspaceRoot, registered.Secret); err != nil {
			return StepUpTrust{}, err
		}
		groups, groupErr := exactRequiredGroupIDs(ctx, client)
		if groupErr != nil {
			return StepUpTrust{}, groupErr
		}
		if _, err = client.UpdateOIDCClientAllowedUserGroups(ctx, StepUpClientID, groups); err != nil {
			return StepUpTrust{}, ErrStepUpRejected
		}
		registered, err = client.GetOIDCClient(ctx, StepUpClientID)
	}
	if err != nil || registered == nil || registered.ID != StepUpClientID || registered.IsPublic ||
		len(registered.Credentials.FederatedIdentities) != 0 ||
		!registered.PkceEnabled || !registered.RequiresReauthentication || !registered.IsGroupRestricted ||
		!slices.Equal(registered.CallbackURLs, []string{origin + StepUpCallbackPath}) {
		return StepUpTrust{}, ErrStepUpRejected
	}
	if _, secretErr := readStepUpSecret(s.workspaceRoot); secretErr != nil {
		if !create || !errors.Is(secretErr, os.ErrNotExist) {
			return StepUpTrust{}, secretErr
		}
		// A client created before interrupted secret persistence is ours only
		// after the exact confidential/PKCE/callback projection above matches.
		secret, rotateErr := client.CreateOIDCClientSecret(ctx, StepUpClientID)
		if rotateErr != nil {
			return StepUpTrust{}, ErrStepUpRejected
		}
		if err = persistStepUpSecret(root, s.workspaceRoot, secret); err != nil {
			return StepUpTrust{}, err
		}
	}
	groups, err := exactRequiredGroupIDs(ctx, client)
	if err == nil && create && len(registered.AllowedUserGroups) == 0 {
		// Resume the same owned client's interrupted first group assignment.
		registered, err = client.UpdateOIDCClientAllowedUserGroups(ctx, StepUpClientID, groups)
	}
	if err != nil || registered == nil || !samePocketIDGroupIDs(registered.AllowedUserGroups, groups) {
		return StepUpTrust{}, ErrStepUpRejected
	}
	runtime, err := localevidence.LoadBasementRuntimeCustody(s.workspaceRoot)
	if err != nil {
		return StepUpTrust{}, err
	}
	var keys jose.JSONWebKeySet
	if err = stepUpJSON(ctx, http.MethodGet, pocketIDLocalAPI+"/.well-known/jwks.json", nil, &keys); err != nil {
		return StepUpTrust{}, err
	}
	return StepUpTrust{Issuer: "https://id." + runtime.Domain, Subject: binding.PocketIDSubject, OwnerRef: binding.OwnerRef, HomeSiteRef: owner.Binding.SiteRef, Keys: keys}, nil
}

// ExchangeStepUpCode uses only the fixed local PocketID endpoint. Neither an
// incoming token nor discovery metadata may redirect a request to another host.
func (s *Service) ExchangeStepUpCode(ctx context.Context, origin, code, verifier string, expected RemoteActionApprovalBinding) (json.RawMessage, error) {
	origin, err := NormalizeStepUpOrigin(origin)
	if err != nil {
		return nil, err
	}
	trust, err := s.ReadStepUpTrust(ctx, origin)
	if err != nil {
		return nil, err
	}
	if code == "" || len(code) > 4096 || len(verifier) < 43 || len(verifier) > 128 {
		return nil, ErrStepUpRejected
	}
	secret, err := readStepUpSecret(s.workspaceRoot)
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {StepUpClientID}, "client_secret": {secret}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {origin + StepUpCallbackPath}}
	var response struct {
		IDToken string `json:"id_token"`
	}
	if err = stepUpJSON(ctx, http.MethodPost, pocketIDLocalAPI+"/api/oidc/token", form, &response); err != nil {
		return nil, err
	}
	receipt, _ := json.Marshal(RemoteActionApproval{IDToken: response.IDToken})
	if err = VerifyRemoteActionApprovalClaims(receipt, expected, trust, s.now()); err != nil {
		return nil, err
	}
	return receipt, nil
}

func readStepUpSecret(workspaceRoot string) (string, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return "", err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return "", err
	}
	file, err := view.Open(stepUpSecretPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err = backupcustody.RequirePrivatePath(filepath.Join(workspaceRoot, filepath.FromSlash(stepUpSecretPath)), false); err != nil {
		return "", err
	}
	body, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(body) < 24 || len(body) > 4096 || strings.TrimSpace(string(body)) != string(body) {
		return "", ErrStepUpRejected
	}
	return string(body), nil
}

func persistStepUpSecret(root *confinedfs.Root, workspaceRoot, secret string) error {
	if len(secret) < 24 || len(secret) > 4096 || strings.TrimSpace(secret) != secret {
		return ErrStepUpRejected
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	if _, err = view.WriteAtomic0600NoReplace(stepUpSecretPath, []byte(secret)); err != nil {
		return err
	}
	return backupcustody.ProtectPrivatePath(filepath.Join(workspaceRoot, filepath.FromSlash(stepUpSecretPath)), false)
}

// VerifyRemoteActionApproval requires the live Home owner/client projection and
// consumes the exact approval before the caller dispatches any mutation.
func VerifyRemoteActionApproval(ctx context.Context, workspaceRoot, origin string, receipt json.RawMessage, expected RemoteActionApprovalBinding) error {
	service, err := NewService(workspaceRoot)
	if err != nil {
		return err
	}
	trust, err := service.ReadStepUpTrust(ctx, origin)
	if err != nil {
		return err
	}
	return ConsumeRemoteActionApproval(workspaceRoot, receipt, expected, trust, time.Now())
}

// NormalizeStepUpOrigin keeps registration, the browser Origin and OAuth callback
// on one canonical HTTPS origin. Unsupported host forms fail during startup.
func NormalizeStepUpOrigin(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") || u.Path != "" || u.Opaque != "" || strings.HasSuffix(u.Host, ":") {
		return "", ErrStepUpRejected
	}
	host := strings.ToLower(u.Hostname())
	if address, parseErr := netip.ParseAddr(host); parseErr == nil && address.Zone() == "" {
		host = address.String()
		if address.Is6() {
			host = "[" + host + "]"
		}
	} else {
		lastLabel := host[strings.LastIndexByte(host, '.')+1:]
		// Browsers also parse shortened, octal and hexadecimal IPv4 forms.
		// Require the canonical IP parser above instead of a different Origin.
		if strings.Trim(lastLabel, "0123456789") == "" || strings.HasPrefix(lastLabel, "0x") {
			return "", ErrStepUpRejected
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return "", ErrStepUpRejected
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", ErrStepUpRejected
				}
			}
		}
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", ErrStepUpRejected
		}
		if number != 443 {
			host += ":" + strconv.Itoa(number)
		}
	}
	return "https://" + host, nil
}

func stepUpJSON(ctx context.Context, method, endpoint string, form url.Values, output any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return ErrStepUpRejected
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return ErrStepUpRejected
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrStepUpRejected
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 65537))
	if decoder.Decode(output) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrStepUpRejected
	}
	return nil
}
