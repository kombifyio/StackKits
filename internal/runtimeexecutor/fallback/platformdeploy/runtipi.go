package platformdeploy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RuntipiAdapter uses the upstream v4 custom-app and app-lifecycle APIs. The
// caller supplies credentials from the installed node, never a hosted account.
// Creating the adapter does not install Runtipi or claim runtime compatibility.
type RuntipiAdapter struct {
	client apiClient
}

type runtipiOriginTransport struct{ base http.RoundTripper }

func (t runtipiOriginTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Origin", request.URL.Scheme+"://"+request.URL.Host)
	return t.base.RoundTrip(request)
}

func NewRuntipiAdapter(cfg HTTPConfig) *RuntipiAdapter {
	client := *cfg.httpClient()
	if client.Jar == nil {
		client.Jar, _ = cookiejar.New(nil)
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = runtipiOriginTransport{transport}
	// API redirects are not a login flow. In particular, never forward a
	// retained operator password to another origin through a 307/308 response.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cfg.Client = &client
	return &RuntipiAdapter{client: apiClient{cfg: cfg, authMode: authBearer}}
}

// request keeps operation and HTTP status for diagnosis, never a response body,
// request payload, endpoint URL or wrapped transport/decode error. The shared API
// client includes response snippets, so no Runtipi method exposes its errors.
func (a *RuntipiAdapter) request(ctx context.Context, method, path string, payload, out any) error {
	if ctx == nil {
		return errors.New("Runtipi API requires an operation context")
	}
	status, _, err := a.client.doJSON(ctx, method, path, payload, out)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("Runtipi API %s %s: %w", method, path, ctx.Err())
	}
	return fmt.Errorf("Runtipi API %s %s failed (HTTP %d)", method, path, status)
}

// Register creates the upstream's sole operator. A registration failure is not
// retried as account creation; the installation owner reconciles by logging in
// with the retained node credential before deciding whether registration is new.
func (a *RuntipiAdapter) Register(ctx context.Context, username, password string) error {
	return a.authenticate(ctx, "/api/auth/register", username, password)
}

func (a *RuntipiAdapter) Login(ctx context.Context, username, password string) error {
	return a.authenticate(ctx, "/api/auth/login", username, password)
}

func (a *RuntipiAdapter) authenticate(ctx context.Context, endpoint, username, password string) error {
	if username == "" || password == "" {
		return errors.New("Runtipi requires its retained node operator credential")
	}
	var result struct {
		Success       bool   `json:"success"`
		TotpSessionID string `json:"totpSessionId"`
	}
	err := a.request(ctx, http.MethodPost, endpoint, map[string]string{"username": username, "password": password}, &result)
	if err != nil {
		// Do not expose an authentication response or request in a node receipt.
		return fmt.Errorf("Runtipi operator authentication failed; reconcile the retained node credential: %w", err)
	}
	if !result.Success || result.TotpSessionID != "" {
		return errors.New("Runtipi operator requires an interactive second factor")
	}
	return nil
}

var runtipiAppName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)

// RuntipiAppObservation is platform readback, not a substitute for the node's
// image, application-readiness and persistent-data observations.
type RuntipiAppObservation struct {
	Info struct {
		URN string `json:"urn"`
	} `json:"info"`
	App *struct {
		Status string `json:"status"`
	} `json:"app"`
}

type RuntipiUserContext struct {
	IsConfigured bool `json:"isConfigured"`
	IsLoggedIn   bool `json:"isLoggedIn"`
	Version      struct {
		Current string `json:"current"`
	} `json:"version"`
}

func (a *RuntipiAdapter) UserContext(ctx context.Context) (RuntipiUserContext, error) {
	var result RuntipiUserContext
	err := a.request(ctx, http.MethodGet, "/api/user-context", nil, &result)
	return result, err
}

func runtipiURN(name string) (string, error) {
	if !runtipiAppName.MatchString(name) {
		return "", errors.New("Runtipi custom app requires a node-owned name of at most 50 lowercase characters")
	}
	return name + ":_user", nil
}

func (a *RuntipiAdapter) App(ctx context.Context, ref DeploymentRef) (RuntipiAppObservation, error) {
	expected, err := runtipiURN(ref.AppName)
	if err != nil || ref.Platform != "runtipi" || ref.ExternalID != expected {
		return RuntipiAppObservation{}, errors.New("Runtipi observation requires its exact retained custom-app identity")
	}
	var result RuntipiAppObservation
	err = a.request(ctx, http.MethodGet, "/api/apps/"+url.PathEscape(expected), nil, &result)
	if err == nil && result.Info.URN != expected {
		err = errors.New("Runtipi app readback differs from its retained identity")
	}
	return result, err
}

// ApplyCompose creates one custom app. It deliberately refuses an existing name:
// updating an installed app requires the retained DeploymentRef via UpdateCompose.
func (a *RuntipiAdapter) ApplyCompose(ctx context.Context, manifest AppManifest) (DeploymentRef, error) {
	urn, err := runtipiURN(manifest.Name)
	if err != nil {
		return DeploymentRef{}, err
	}
	config, err := runtipiCompose(manifest)
	if err != nil {
		return DeploymentRef{}, err
	}
	var result struct {
		AppURN string `json:"appUrn"`
	}
	if err := a.request(ctx, http.MethodPost, "/api/custom-apps", map[string]any{"name": manifest.Name, "config": config}, &result); err != nil {
		return DeploymentRef{}, &DeploymentIdentityUncertainError{Err: fmt.Errorf("Runtipi custom-app creation did not confirm its identity; reconcile before retrying: %w", err)}
	}
	ref := DeploymentRef{Platform: "runtipi", AppName: manifest.Name, ExternalID: urn, ComposeYAML: manifest.ComposeYAML}
	if result.AppURN != urn {
		return ref, &DeploymentIdentityUncertainError{Err: errors.New("Runtipi custom-app creation returned another identity")}
	}
	return ref, nil
}

func runtipiCompose(manifest AppManifest) (map[string]any, error) {
	if manifest.ComposeYAML == "" || manifest.ServiceKey == "" || manifest.Port < 1 || manifest.Port > 65535 {
		return nil, errors.New("Runtipi requires the generated Compose graph, exact entry service and internal port")
	}
	var config map[string]any
	if err := yaml.Unmarshal([]byte(manifest.ComposeYAML), &config); err != nil {
		return nil, errors.New("decode Runtipi workload Compose failed")
	}
	services, _ := config["services"].(map[string]any)
	main, ok := services[manifest.ServiceKey].(map[string]any)
	if !ok {
		return nil, errors.New("Runtipi main service is absent from the generated Compose graph")
	}
	// Upstream owns Compose project identity and routing. Retain all generated
	// storage, secret-file and resource declarations without materializing secrets.
	delete(config, "name")
	config["x-runtipi"] = map[string]any{"schema_version": 2}
	main["x-runtipi"] = map[string]any{"is_main": true, "internal_port": manifest.Port}
	return config, nil
}

func (a *RuntipiAdapter) UpdateCompose(ctx context.Context, ref DeploymentRef, manifest AppManifest) error {
	if manifest.Name != ref.AppName {
		return errors.New("Runtipi update cannot change its retained app identity")
	}
	if _, err := a.App(ctx, ref); err != nil {
		return err
	}
	config, err := runtipiCompose(manifest)
	if err != nil {
		return err
	}
	return a.request(ctx, http.MethodPatch, "/api/custom-apps/"+url.PathEscape(ref.ExternalID), map[string]any{"config": config}, nil)
}

// Control submits one upstream lifecycle operation. The caller must persist
// app identity before install; a successful queue submission is not readiness.
func (a *RuntipiAdapter) Control(ctx context.Context, ref DeploymentRef, action string) error {
	if _, err := a.App(ctx, ref); err != nil {
		return err
	}
	var payload any
	method := http.MethodPost
	switch action {
	case "install":
		return a.Install(ctx, ref, "")
	case "start", "stop", "restart":
	case "update":
		method = http.MethodPatch
		payload = map[string]bool{"performBackup": true}
	default:
		return fmt.Errorf("unsupported Runtipi lifecycle action %q", action)
	}
	return a.request(ctx, method, "/api/app-lifecycle/"+url.PathEscape(ref.ExternalID)+"/"+action, payload, nil)
}

// Install keeps ports and public exposure closed. A local subdomain asks the
// upstream router to own local ingress; an empty subdomain preserves the
// compiler-bound Kit router labels without creating a competing route.
func (a *RuntipiAdapter) Install(ctx context.Context, ref DeploymentRef, localSubdomain string) error {
	if _, err := a.App(ctx, ref); err != nil {
		return err
	}
	payload := map[string]any{"exposed": false, "exposedLocal": localSubdomain != "", "openPort": false, "enableAuth": true, "isVisibleOnGuestDashboard": false, "forcePull": false}
	if localSubdomain != "" {
		if !runtipiAppName.MatchString(localSubdomain) {
			return errors.New("Runtipi local subdomain differs from its closed app-name contract")
		}
		payload["localSubdomain"] = localSubdomain
	}
	return a.request(ctx, http.MethodPost, "/api/app-lifecycle/"+url.PathEscape(ref.ExternalID)+"/install", payload, nil)
}

func (a *RuntipiAdapter) ObserveDeployment(ctx context.Context, ref DeploymentRef) (DeploymentRef, error) {
	result, err := a.App(ctx, ref)
	if err != nil {
		return ref, err
	}
	ref.ObservedAt = time.Now().UTC()
	if result.App != nil {
		ref.ObservedStatus = strings.ToLower(result.App.Status)
	}
	if ref.ObservedStatus != "running" {
		switch ref.ObservedStatus {
		case "stopped", "starting", "stopping", "installing", "updating", "missing", "error":
		default:
			ref.ObservedStatus = "unexpected"
		}
		return ref, fmt.Errorf("Runtipi app is %q; platform queue acceptance is not runtime readiness", ref.ObservedStatus)
	}
	return ref, nil
}

// Uninstall retains upstream backups unless the installed lifecycle explicitly
// authorizes their deletion. Node-owned external volumes retain their custody.
func (a *RuntipiAdapter) Uninstall(ctx context.Context, ref DeploymentRef, removeBackups bool) error {
	if _, err := a.App(ctx, ref); err != nil {
		return err
	}
	return a.request(ctx, http.MethodDelete, "/api/app-lifecycle/"+url.PathEscape(ref.ExternalID)+"/uninstall", map[string]bool{"removeBackups": removeBackups}, nil)
}

var _ Adapter = (*RuntipiAdapter)(nil)
var _ DeploymentObserver = (*RuntipiAdapter)(nil)
