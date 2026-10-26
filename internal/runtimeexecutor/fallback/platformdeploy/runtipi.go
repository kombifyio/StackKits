package platformdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"slices"
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

type runtipiAPIError struct {
	method string
	path   string
	status int
}

func (e *runtipiAPIError) Error() string {
	return fmt.Sprintf("Runtipi API %s %s failed (HTTP %d)", e.method, e.path, e.status)
}

func runtipiAppNotFound(err error) bool {
	var apiErr *runtipiAPIError
	return errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound
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
	return &runtipiAPIError{method: method, path: path, status: status}
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
	Metadata struct {
		HasCustomConfig *bool `json:"hasCustomConfig"`
	} `json:"metadata"`
}

// Status uses only this API readback. Catalog info without an installed app
// row must never inherit a retained DeploymentRef's earlier running status.
func (o RuntipiAppObservation) Status() string {
	if o.App == nil {
		return "missing"
	}
	switch status := o.App.Status; status {
	case "running", "stopped", "missing", "installing", "uninstalling", "stopping", "starting", "updating", "resetting", "restarting", "backing_up", "restoring":
		return status
	default:
		return "unexpected"
	}
}

// WaitForAppStatus observes the asynchronous v4 lifecycle without resubmitting
// commands. A terminal status establishes no application health, image, data or
// update-success claim; those remain the node workload observer's responsibility.
func (a *RuntipiAdapter) WaitForAppStatus(ctx context.Context, ref DeploymentRef, expected ...string) (RuntipiAppObservation, error) {
	if ctx == nil || len(expected) == 0 {
		return RuntipiAppObservation{}, errors.New("Runtipi lifecycle wait requires its context and terminal statuses")
	}
	for _, status := range expected {
		if status != "running" && status != "stopped" && status != "missing" {
			return RuntipiAppObservation{}, errors.New("Runtipi lifecycle wait requires known terminal statuses")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		app, err := a.App(ctx, ref)
		if err != nil {
			// Custom-app uninstall deletes its source metadata as well as the
			// installed row, so /apps/:urn returns 404 instead of app:null.
			if slices.Contains(expected, "missing") && runtipiAppNotFound(err) {
				return RuntipiAppObservation{}, nil
			}
			return RuntipiAppObservation{}, err
		}
		status := app.Status()
		if slices.Contains(expected, status) {
			return app, nil
		}
		switch status {
		case "installing", "uninstalling", "stopping", "starting", "updating", "resetting", "restarting", "backing_up", "restoring":
		default:
			return app, fmt.Errorf("Runtipi lifecycle reached %q instead of %s; inspect the retained app before retrying", status, strings.Join(expected, "/"))
		}
		select {
		case <-ctx.Done():
			return app, fmt.Errorf("Runtipi lifecycle did not reach %s: %w", strings.Join(expected, "/"), ctx.Err())
		case <-ticker.C:
		}
	}
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
	app, err := a.App(ctx, ref)
	if err != nil {
		return err
	}
	if app.Status() != "stopped" && app.Status() != "missing" {
		return errors.New("Runtipi custom-app editing requires its stopped or missing state")
	}
	config, err := runtipiCompose(manifest)
	if err != nil {
		return err
	}
	// The v4.10.2 edit UI writes the installed recipe through this endpoint.
	// Start's ensureAppDir reads it; the repository update command does not.
	raw, err := yaml.Marshal(config)
	if err != nil {
		return errors.New("encode Runtipi custom-app recipe failed")
	}
	var result struct {
		Success bool `json:"success"`
	}
	if err := a.request(ctx, http.MethodPost, "/api/apps/"+url.PathEscape(ref.ExternalID)+"/config", map[string]string{"config": string(raw)}, &result); err != nil {
		return err
	}
	if !result.Success {
		return errors.New("Runtipi custom-app edit did not confirm its installed recipe")
	}
	return nil
}

// ComposeMatches reads the installed editable recipe, not an app-store source
// and not DeploymentRef.ComposeYAML, which is deliberately absent from custody
// JSON. This also detects recipe-format changes at an unchanged artifact pin.
func (a *RuntipiAdapter) ComposeMatches(ctx context.Context, ref DeploymentRef, manifest AppManifest) (bool, error) {
	if manifest.Name != ref.AppName {
		return false, errors.New("Runtipi recipe readback cannot change its retained app identity")
	}
	if _, err := a.App(ctx, ref); err != nil {
		return false, err
	}
	expected, err := runtipiCompose(manifest)
	if err != nil {
		return false, err
	}
	var result struct {
		Config *string `json:"config"`
	}
	if err := a.request(ctx, http.MethodGet, "/api/apps/"+url.PathEscape(ref.ExternalID)+"/config", nil, &result); err != nil {
		return false, err
	}
	if result.Config == nil {
		return false, nil
	}
	var actual map[string]any
	if json.Unmarshal([]byte(*result.Config), &actual) != nil || actual == nil {
		return false, errors.New("Runtipi installed recipe readback is malformed")
	}
	wanted, err := json.Marshal(expected)
	if err != nil {
		return false, errors.New("encode expected Runtipi recipe failed")
	}
	observed, err := json.Marshal(actual)
	if err != nil {
		return false, errors.New("encode observed Runtipi recipe failed")
	}
	return bytes.Equal(wanted, observed), nil
}

type RuntipiBackupObservation struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
}

func (a *RuntipiAdapter) backups(ctx context.Context, ref DeploymentRef) ([]RuntipiBackupObservation, error) {
	var result struct {
		Data []RuntipiBackupObservation `json:"data"`
	}
	err := a.request(ctx, http.MethodGet, "/api/backups/"+url.PathEscape(ref.ExternalID)+"?page=1&pageSize=100", nil, &result)
	return result.Data, err
}

// ActivateCompose follows the retained pin's custom-app edit/start path, never
// its app-store update command. The caller retains pending intent until its own
// running-image/configuration/data observations pass. The upstream archive
// covers Runtipi's app directories, not node-owned external Docker volumes;
// those keep the existing Kopia custody and restore obligations.
func (a *RuntipiAdapter) ActivateCompose(ctx context.Context, ref DeploymentRef, manifest AppManifest) (RuntipiBackupObservation, error) {
	if manifest.Name != ref.AppName {
		return RuntipiBackupObservation{}, errors.New("Runtipi activation cannot change its retained app identity")
	}
	if _, err := runtipiCompose(manifest); err != nil {
		return RuntipiBackupObservation{}, err
	}
	app, err := a.WaitForAppStatus(ctx, ref, "running", "stopped", "missing")
	if err != nil {
		return RuntipiBackupObservation{}, err
	}
	missing := app.Status() == "missing"
	var backup RuntipiBackupObservation
	if !missing {
		if app.Status() == "running" {
			if err := a.Control(ctx, ref, "stop"); err != nil {
				return backup, err
			}
			if _, err := a.WaitForAppStatus(ctx, ref, "stopped"); err != nil {
				return backup, err
			}
		}
		before, err := a.backups(ctx, ref)
		if err != nil {
			return backup, err
		}
		if err := a.request(ctx, http.MethodPost, "/api/backups/"+url.PathEscape(ref.ExternalID)+"/backup", nil, nil); err != nil {
			return backup, err
		}
		if _, err := a.WaitForAppStatus(ctx, ref, "stopped"); err != nil {
			return backup, err
		}
		after, err := a.backups(ctx, ref)
		if err != nil {
			return backup, err
		}
		for _, candidate := range after {
			if candidate.ID != "" && candidate.Size > 0 && !slices.ContainsFunc(before, func(old RuntipiBackupObservation) bool { return old.ID == candidate.ID }) {
				backup = candidate
				break
			}
		}
		if backup.ID == "" {
			return backup, errors.New("Runtipi pre-edit backup did not produce a new archive; retain pending reconciliation")
		}
	}
	if err := a.UpdateCompose(ctx, ref, manifest); err != nil {
		return backup, err
	}
	if missing {
		err = a.Install(ctx, ref, "")
	} else {
		err = a.Control(ctx, ref, "start")
	}
	if err != nil {
		return backup, err
	}
	_, err = a.WaitForAppStatus(ctx, ref, "running")
	return backup, err
}

// Control submits one upstream lifecycle operation. The caller must persist
// app identity before install; a successful queue submission is not readiness.
func (a *RuntipiAdapter) Control(ctx context.Context, ref DeploymentRef, action string) error {
	if action == "update" {
		return errors.New("Runtipi custom apps require stopped recipe editing and start; the app-store update command cannot activate their revisions")
	}
	if _, err := a.App(ctx, ref); err != nil {
		return err
	}
	var payload any
	method := http.MethodPost
	switch action {
	case "install":
		return a.Install(ctx, ref, "")
	case "start", "stop", "restart":
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
		ref.ObservedStatus = "unexpected"
		if runtipiAppNotFound(err) {
			ref.ObservedStatus = "missing"
			ref.ObservedAt = time.Now().UTC()
		}
		return ref, err
	}
	ref.ObservedAt = time.Now().UTC()
	ref.ObservedStatus = result.Status()
	if ref.ObservedStatus != "running" {
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
