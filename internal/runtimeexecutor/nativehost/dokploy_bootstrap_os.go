package nativehost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"gopkg.in/yaml.v3"
)

const dokployDirectory = ".stackkit/runtime/dokploy"
const dokployProject = "stackkit-dokploy"
const dokployCoreModuleRef = "stackkits-basement-core-lite-runtime"

// dokployApplicationsDirectory holds the node's workload recipes. Dokploy runs
// `docker compose` inside its own container, so it sees this directory read-only
// at the same path to read each workload's absolute env_file references.
const dokployApplicationsDirectory = ".stackkit/runtime/applications"

type dokployCustody struct {
	Version          string `json:"version"`
	OwnerRef         string `json:"ownerRef"`
	SiteRef          string `json:"siteRef"`
	NodeRef          string `json:"nodeRef"`
	Domain           string `json:"domain"`
	DatabasePassword string `json:"databasePassword"`
	AuthSecret       string `json:"authSecret"`
	OwnerEmail       string `json:"ownerEmail"`
	OwnerPassword    string `json:"ownerPassword"`
	OwnerUserID      string `json:"ownerUserId,omitempty"`
	OrganizationID   string `json:"organizationId,omitempty"`
	APIKey           string `json:"apiKey,omitempty"`
	ProjectID        string `json:"projectId,omitempty"`
	EnvironmentID    string `json:"environmentId,omitempty"`
}

type osDokployBootstrapOperations struct{ workspace string }

func NewOSDokployBootstrapOperations(workspace string) (DokployBootstrapOperations, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil || strings.TrimSpace(workspace) == "" {
		return nil, errors.New("Dokploy bootstrap requires its owner workspace")
	}
	root, err := confinedfs.Open(absolute)
	if err != nil {
		return nil, err
	}
	root.Close()
	return &osDokployBootstrapOperations{workspace: absolute}, nil
}

func (o *osDokployBootstrapOperations) ApplyDokploy(ctx context.Context, d architecturev2renderer.DokployBootstrapDocument) (DokployBootstrapObservation, error) {
	var empty DokployBootstrapObservation
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil {
		return empty, err
	}
	if owner.Binding.SiteRef != d.SiteRef || owner.Binding.NodeRef != d.NodeRef {
		return empty, errors.New("Dokploy bootstrap names another owner node")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return empty, errors.New("Dokploy installation runs on its own Linux node as the installed lifecycle owner")
	}
	if localevidence.IsPlaceholderOwnerEmail(owner.PocketID.Email) {
		return empty, errors.New("Dokploy requires established Kit custody and the root owner's email")
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(o.workspace)
	if err != nil || address.Domain != d.DomainBase || address.ServiceHost("dokploy") != d.Host() {
		return empty, errors.New("Dokploy address differs from its owner-signed Kit identity custody")
	}
	if _, err := localevidence.LoadBasementRuntimeCustody(o.workspace); err != nil {
		return empty, fmt.Errorf("Dokploy requires the established Basement node custody: %w", err)
	}
	root, err := confinedfs.Open(o.workspace)
	if err != nil {
		return empty, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return empty, err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(dokployDirectory)
	if err != nil {
		return empty, err
	}
	defer lock.Release()
	for _, directory := range []string{dokployDirectory, dokployApplicationsDirectory} {
		if err := tx.MkdirAll(directory, 0700); err != nil {
			return empty, err
		}
	}
	view, err := root.View(dokployDirectory)
	if err != nil {
		return empty, err
	}
	state, err := o.loadOrCreateCustody(ctx, view, owner.OwnerRef, owner.PocketID.Email, d)
	if err != nil {
		return empty, err
	}
	if err := o.prepare(view, state, d); err != nil {
		return empty, err
	}
	directory := filepath.Join(o.workspace, filepath.FromSlash(dokployDirectory))
	if _, err := o.compose(ctx, directory, "up", "-d", "--wait", "--wait-timeout", "240", "postgres"); err != nil {
		return empty, fmt.Errorf("start Dokploy database: %w", err)
	}
	if _, err := o.compose(ctx, directory, "up", "-d", "--wait", "--wait-timeout", "300", "dokploy"); err != nil {
		return empty, fmt.Errorf("start Dokploy: %w", err)
	}
	endpoint, err := o.endpoint(ctx, directory)
	if err != nil {
		return empty, err
	}
	if err := waitPlatformHTTP(ctx, endpoint, "/api/health", "Dokploy"); err != nil {
		return empty, err
	}
	if err := o.ensureOwner(ctx, view, &state, endpoint, "https://"+d.Host()); err != nil {
		return empty, err
	}
	observed := DokployBootstrapObservation{OwnerEmail: state.OwnerEmail, APIPrivate: true, APIKeyAuthorized: true, Ingress: "kit-router", Login: "kit-forward-auth-pocketid,dokploy-local-owner"}
	if err := dokployEnsureProject(ctx, endpoint, state.APIKey, "stackkits-"+d.NodeRef, &state.ProjectID, &state.EnvironmentID); err != nil {
		return empty, err
	}
	if err := saveDokployCustody(view, state); err != nil {
		return empty, err
	}
	observed.EnvironmentID = state.EnvironmentID
	owners, err := dokployOwners(ctx, endpoint, state.APIKey)
	if err != nil {
		return empty, err
	}
	// Dokploy refuses every further sign-up once an owner exists, so a single
	// owner held in node custody also proves closed registration.
	if len(owners) != 1 || !strings.EqualFold(owners[0], state.OwnerEmail) {
		return empty, errors.New("Dokploy organization has an owner outside node custody; inspect before applying")
	}
	observed.SingleOwner, observed.RegistrationClosed = true, true
	for _, component := range d.Components {
		raw, err := o.compose(ctx, directory, "ps", "--quiet", component.ID)
		if err != nil {
			return empty, err
		}
		id := strings.TrimSpace(string(raw))
		if len(id) != 64 {
			return empty, errors.New("Dokploy component has no single running identity")
		}
		actual, err := o.run(ctx, []string{"inspect", "--format", "{{.Config.Image}}", id}, directory)
		if err != nil || strings.TrimSpace(string(actual)) != component.ImageRef+"@"+component.ImageDigest {
			return empty, errors.New("Dokploy running image differs from its selected pin")
		}
		if component.ID == "dokploy" {
			observed.Image = strings.TrimSpace(string(actual))
		} else {
			observed.DatabaseImage = strings.TrimSpace(string(actual))
		}
	}
	swarm, err := o.run(ctx, []string{"info", "--format", "{{.Swarm.LocalNodeState}}"}, o.workspace)
	if err != nil {
		return empty, err
	}
	observed.Swarm = strings.TrimSpace(string(swarm)) != "inactive"
	proxies, err := o.run(ctx, []string{"ps", "--all", "--quiet", "--filter", "name=dokploy-traefik"}, o.workspace)
	if err != nil || strings.TrimSpace(string(proxies)) != "" {
		return empty, errors.New("Dokploy must not run its own Traefik beside the Kit router")
	}
	coreProject, coreNetwork, err := dokployCoreRuntime()
	if err != nil {
		return empty, err
	}
	if d.Route.ID != "" {
		if err := verifyKitRouterDeniesAnonymous(ctx, o.workspace, "Dokploy", coreProject, coreNetwork, d.Route.Host, func(ctx context.Context, args []string) ([]byte, error) {
			return o.run(ctx, args, o.workspace)
		}); err != nil {
			return empty, err
		}
		observed.UIProtected = true
	}
	observed.Status = "ready"
	return observed, nil
}

// ensureOwner creates the owner once, then only signs in with custody. The
// key is saved before any later step so a retry never issues a second one.
func (o *osDokployBootstrapOperations) ensureOwner(ctx context.Context, view confinedfs.View, state *dokployCustody, endpoint, origin string) error {
	if state.APIKey != "" {
		if err := dokployKeyCall(ctx, endpoint, state.APIKey, "GET", "/api/project.all", nil, nil); err != nil {
			return errors.New("Dokploy node API key no longer authorizes; preserve custody and repair it before applying")
		}
		return nil
	}
	session, err := newDokployOwnerSession(endpoint, origin)
	if err != nil {
		return err
	}
	userID := state.OwnerUserID
	if userID == "" {
		userID, err = session.signUp(ctx, state.OwnerEmail, state.OwnerPassword)
		if errors.Is(err, errDokployOwnerExists) {
			// A sign-up whose custody save was lost created this owner with the
			// custody password; only that password signs it in.
			if userID, err = session.signIn(ctx, state.OwnerEmail, state.OwnerPassword); err != nil {
				return errors.New("Dokploy already has an owner without node custody; inspect before applying")
			}
		}
		if err != nil {
			return err
		}
		state.OwnerUserID = userID
		if err := saveDokployCustody(view, *state); err != nil {
			return err
		}
	}
	if signedIn, err := session.signIn(ctx, state.OwnerEmail, state.OwnerPassword); err != nil || signedIn != userID {
		return errors.New("Dokploy owner sign-in differs from node custody")
	}
	organization, err := session.organizationID(ctx)
	if err != nil {
		return err
	}
	key, err := session.createAPIKey(ctx, organization)
	if err != nil {
		return err
	}
	state.OrganizationID, state.APIKey = organization, key
	return saveDokployCustody(view, *state)
}

func (o *osDokployBootstrapOperations) loadOrCreateCustody(ctx context.Context, view confinedfs.View, ownerRef, ownerEmail string, d architecturev2renderer.DokployBootstrapDocument) (dokployCustody, error) {
	state, err := loadDokployCustody(view)
	if err == nil {
		// The Dokploy owner keeps the e-mail it was created with; a later Pocket
		// ID e-mail change does not change that local login.
		if state.OwnerRef != ownerRef || state.SiteRef != d.SiteRef || state.NodeRef != d.NodeRef || state.Domain != d.DomainBase {
			return state, errors.New("Dokploy private custody differs from the selected node")
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return state, err
	}
	raw, err := o.run(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + dokployProject, "--quiet"}, o.workspace)
	if err != nil {
		return state, err
	}
	if strings.TrimSpace(string(raw)) != "" {
		return state, errors.New("Dokploy refuses to adopt an installation without its node custody")
	}
	volumes, err := o.run(ctx, []string{"volume", "ls", "--quiet", "--filter", "label=com.docker.compose.project=" + dokployProject}, o.workspace)
	if err != nil {
		return state, err
	}
	if strings.TrimSpace(string(volumes)) != "" {
		return state, errors.New("Dokploy refuses to reuse installation volumes without their node custody")
	}
	state = dokployCustody{Version: "stackkit.dokploy-custody/v1", OwnerRef: ownerRef, SiteRef: d.SiteRef, NodeRef: d.NodeRef, Domain: d.DomainBase, OwnerEmail: ownerEmail}
	for _, value := range []*string{&state.DatabasePassword, &state.AuthSecret, &state.OwnerPassword} {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return state, err
		}
		*value = hex.EncodeToString(b)
	}
	return state, saveDokployCustody(view, state)
}

func loadDokployCustody(view confinedfs.View) (dokployCustody, error) {
	var state dokployCustody
	file, err := view.Open("custody.json")
	if err != nil {
		return state, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return state, errors.New("Dokploy custody must remain private")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	defer clear(raw)
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &state) != nil || state.Version != "stackkit.dokploy-custody/v1" {
		return state, errors.New("Dokploy private custody is invalid")
	}
	for _, value := range []string{state.DatabasePassword, state.AuthSecret, state.OwnerPassword, state.OwnerEmail} {
		if value == "" || strings.ContainsAny(value, "\r\n\x00 '") {
			return state, errors.New("Dokploy private bootstrap material is incomplete; preserve custody and repair it before applying")
		}
	}
	if (state.APIKey == "") != (state.OrganizationID == "") || (state.ProjectID == "") != (state.EnvironmentID == "") {
		return state, errors.New("Dokploy API key, organization and project custody are inconsistent")
	}
	return state, nil
}

func saveDokployCustody(view confinedfs.View, state dokployCustody) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	defer clear(raw)
	_, err = view.WriteAtomic0600("custody.json", raw)
	return err
}

func dokployCoreRuntime() (project, network string, err error) {
	_, project, supported := NativeComposeProject(dokployCoreModuleRef)
	if !supported {
		return "", "", errors.New("Dokploy Core has no native installed project")
	}
	network, err = standaloneComposeCoreNetwork(dokployCoreModuleRef)
	return project, network, err
}

// prepare renders the private environment files and the Compose recipe.
// Dokploy finds its own network by name and skips its Swarm overlay; it runs
// no Traefik, and its UI is published only by the Kit router's forward-auth.
func (o *osDokployBootstrapOperations) prepare(view confinedfs.View, s dokployCustody, d architecturev2renderer.DokployBootstrapDocument) error {
	_, coreNetwork, err := dokployCoreRuntime()
	if err != nil {
		return err
	}
	files := map[string]string{
		"postgres.env": "POSTGRES_USER=dokploy\nPOSTGRES_DB=dokploy\nPOSTGRES_PASSWORD=" + s.DatabasePassword + "\n",
		// Node derives its heap ceiling from the container limit; Dokploy needs
		// about 1 GiB of heap at startup, so it is set explicitly.
		"dokploy.env": "DATABASE_URL=postgres://dokploy:" + s.DatabasePassword + "@postgres:5432/dokploy\nBETTER_AUTH_SECRET=" + s.AuthSecret +
			"\nBETTER_AUTH_URL=https://" + d.Host() + "\nNODE_OPTIONS=--max-old-space-size=1024\n",
	}
	for name, content := range files {
		if _, err := view.WriteAtomic0600(name, []byte(content)); err != nil {
			return err
		}
	}
	applications := filepath.Join(o.workspace, filepath.FromSlash(dokployApplicationsDirectory))
	labels := map[string]string{"stackkit.owner.ref": s.OwnerRef, "stackkit.owner.node": s.NodeRef, "stackkit.owner.module": architecturev2renderer.DokployBootstrapModuleRef, "traefik.enable": "false"}
	services := map[string]any{}
	for _, c := range d.Components {
		entry := map[string]any{"image": c.ImageRef + "@" + c.ImageDigest, "restart": "unless-stopped", "mem_limit": c.MemoryLimit, "labels": labels, "networks": []string{"dokploy-control"}}
		switch c.ID {
		case "postgres":
			entry["env_file"] = []string{"./postgres.env"}
			entry["volumes"] = []string{"dokploy-postgres:/var/lib/postgresql/data"}
			entry["healthcheck"] = map[string]any{"test": []string{"CMD", "pg_isready", "-U", "dokploy", "-d", "dokploy"}, "interval": "5s", "retries": 20, "timeout": "3s"}
		case "dokploy":
			// The UI is published only through the authored route, whose exact host
			// TinyAuth admits.
			if d.Route.ID != "" {
				router := standaloneComposeRouteLabels(d.Route, false)
				for key, value := range labels {
					if key != "traefik.enable" {
						router[key] = value
					}
				}
				entry["labels"] = router
			}
			entry["env_file"] = []string{"./dokploy.env"}
			entry["ports"] = []string{"127.0.0.1::3000"}
			entry["networks"] = []string{"dokploy-control", "kit-core", "dokploy-network"}
			entry["volumes"] = []string{"/var/run/docker.sock:/var/run/docker.sock", "dokploy-data:/etc/dokploy", applications + ":" + applications + ":ro"}
			entry["depends_on"] = map[string]any{"postgres": map[string]string{"condition": "service_healthy"}}
			entry["healthcheck"] = map[string]any{"test": []string{"CMD-SHELL", "curl -fs http://127.0.0.1:3000/api/health"}, "interval": "5s", "start_period": "60s", "retries": 36, "timeout": "3s"}
		}
		services[c.ID] = entry
	}
	raw, err := yaml.Marshal(map[string]any{"name": dokployProject, "services": services,
		"volumes":  map[string]any{"dokploy-postgres": map[string]any{}, "dokploy-data": map[string]any{}},
		"networks": map[string]any{"dokploy-control": map[string]any{}, "kit-core": map[string]any{"external": true, "name": coreNetwork}, "dokploy-network": map[string]any{"name": "dokploy-network", "attachable": true}}})
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600("compose.yaml", raw)
	return err
}

func (o *osDokployBootstrapOperations) run(ctx context.Context, args []string, directory string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	cmd.Dir = directory
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DOCKER_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Dokploy Docker operation failed (%s): %s: %w", args[0], lastStderrLine(stderr.String()), err)
	}
	if stdout.Len() > 2<<20 {
		return nil, errors.New("Dokploy Docker output exceeds its bound")
	}
	return stdout.Bytes(), nil
}

func (o *osDokployBootstrapOperations) compose(ctx context.Context, directory string, args ...string) ([]byte, error) {
	return o.run(ctx, append([]string{"compose", "--project-name", dokployProject, "--file", filepath.Join(directory, "compose.yaml")}, args...), directory)
}

func (o *osDokployBootstrapOperations) endpoint(ctx context.Context, directory string) (string, error) {
	raw, err := o.compose(ctx, directory, "port", "dokploy", "3000")
	if err != nil {
		return "", err
	}
	address := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(address, "127.0.0.1:") {
		return "", errors.New("Dokploy API must remain loopback-only")
	}
	return "http://" + address, nil
}

// lastStderrLine keeps the Docker refusal reason without echoing the full
// output. Compose prints no environment values on error.
func lastStderrLine(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > 240 {
		line = line[:240]
	}
	return line
}

// DokployOwnerLogin returns the local owner login of this node's Dokploy from
// node custody. Native Dokploy SSO is licensed, so this login follows the Kit
// router's PocketID forward-auth.
func DokployOwnerLogin(workspace string) (email, password string, err error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	view, err := root.View(dokployDirectory)
	if err != nil {
		return "", "", errors.New("this node has no bootstrapped Dokploy")
	}
	state, err := loadDokployCustody(view)
	if err != nil {
		return "", "", errors.New("this node has no bootstrapped Dokploy")
	}
	if state.OwnerUserID == "" {
		return "", "", errors.New("Dokploy owner is not created yet; run stackkit apply")
	}
	return state.OwnerEmail, state.OwnerPassword, nil
}
