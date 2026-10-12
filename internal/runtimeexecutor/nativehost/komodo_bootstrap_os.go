package nativehost

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

const komodoBootstrapDirectory = ".stackkit/runtime/komodo"
const komodoBootstrapProject = "stackkit-komodo"
const komodoBootstrapServerName = "stackkit-local"

type osKomodoBootstrapOperations struct{ workspace string }

type KomodoBootstrapNodeMismatchError struct{}

func (*KomodoBootstrapNodeMismatchError) Error() string {
	return "Komodo bootstrap names another owner node"
}

type KomodoBootstrapUpgradeDeniedError struct{}

func (*KomodoBootstrapUpgradeDeniedError) Error() string {
	return "Komodo control-plane image changes require a supported backup and explicit upgrade; preserve the existing node custody"
}

type komodoBootstrapCustody struct {
	Version             string `json:"version"`
	OwnerRef            string `json:"ownerRef"`
	SiteRef             string `json:"siteRef"`
	NodeRef             string `json:"nodeRef"`
	Domain              string `json:"domain"`
	AdminPassword       string `json:"adminPassword"`
	DatabasePassword    string `json:"databasePassword"`
	JWTSecret           string `json:"jwtSecret"`
	CorePrivateKey      string `json:"corePrivateKey"`
	CorePublicKey       string `json:"corePublicKey"`
	PeripheryPrivateKey string `json:"peripheryPrivateKey"`
	PeripheryPublicKey  string `json:"peripheryPublicKey"`
	APIKey              string `json:"apiKey,omitempty"`
	APISecret           string `json:"apiSecret,omitempty"`
	APIKeyState         string `json:"apiKeyState,omitempty"`
	ServerID            string `json:"serverId,omitempty"`
}

func NewOSKomodoBootstrapOperations(workspace string) (KomodoBootstrapOperations, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil || strings.TrimSpace(workspace) == "" {
		return nil, errors.New("Komodo bootstrap requires its owner workspace")
	}
	root, err := confinedfs.Open(absolute)
	if err != nil {
		return nil, err
	}
	root.Close()
	return &osKomodoBootstrapOperations{workspace: absolute}, nil
}

func (o *osKomodoBootstrapOperations) ApplyKomodo(ctx context.Context, document architecturev2renderer.KomodoBootstrapDocument) (KomodoBootstrapObservation, error) {
	if ctx == nil || o == nil {
		return KomodoBootstrapObservation{}, errors.New("Komodo bootstrap requires a context and workspace")
	}
	rawDocument, err := json.Marshal(document)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	if _, err = architecturev2renderer.ParseKomodoBootstrapDocument(rawDocument); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	if owner.Binding.SiteRef != document.SiteRef || owner.Binding.NodeRef != document.NodeRef {
		return KomodoBootstrapObservation{}, &KomodoBootstrapNodeMismatchError{}
	}
	if runtime.GOOS != "linux" {
		return KomodoBootstrapObservation{}, errors.New("Komodo bootstrap requires execution on the installed Linux node and its own Docker socket")
	}
	runtimeCustody, err := localevidence.LoadBasementRuntimeCustody(o.workspace)
	if err != nil || runtimeCustody.Domain != document.DomainBase {
		return KomodoBootstrapObservation{}, errors.New("Komodo bootstrap requires matching established Basement custody")
	}
	root, err := confinedfs.Open(o.workspace)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(komodoBootstrapDirectory)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	defer lock.Release()
	if err := tx.MkdirAll(komodoBootstrapDirectory, 0700); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	view, err := root.View(komodoBootstrapDirectory)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	state, err := o.loadOrCreateCustody(ctx, root, view, owner.OwnerRef, document)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	if state.APIKeyState == "creating" {
		return KomodoBootstrapObservation{}, errors.New("Komodo API-key creation has an uncertain outcome; inspect and repair its existing private custody before retrying")
	}
	if err := o.prepareBootstrapFiles(view, state, document); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	directory := filepath.Join(o.workspace, filepath.FromSlash(komodoBootstrapDirectory))
	if _, err := o.compose(ctx, directory, "up", "-d", "--wait", "--wait-timeout", "180", "mongo", "core"); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	endpoint, err := o.apiEndpoint(ctx, directory)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	coreVersion := ""
	for _, image := range document.Components {
		if image.ID == "core" {
			coreVersion = strings.TrimPrefix(image.ImageRef, "ghcr.io/moghtech/komodo-core:")
		}
	}
	if err := waitKomodoCoreAPI(ctx, client, endpoint, coreVersion); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	if state.APIKey == "" {
		var login struct {
			JWT string `json:"jwt"`
		}
		if err := komodoBootstrapRPC(ctx, client, endpoint, "/auth/login", "LoginLocalUser", map[string]any{"username": "stackkits-admin", "password": state.AdminPassword}, "", "", "", &login); err != nil {
			return KomodoBootstrapObservation{}, err
		}
		if login.JWT == "" {
			return KomodoBootstrapObservation{}, errors.New("Komodo bootstrap login returned no session")
		}
		state.APIKeyState = "creating"
		if err := saveKomodoBootstrapCustody(view, state); err != nil {
			return KomodoBootstrapObservation{}, err
		}
		var key struct {
			Key    string `json:"key"`
			Secret string `json:"secret"`
		}
		if err := komodoBootstrapRPC(ctx, client, endpoint, "/auth/manage", "CreateApiKey", map[string]any{"name": "stackkit-node-" + document.NodeRef, "expires": 0}, login.JWT, "", "", &key); err != nil {
			return KomodoBootstrapObservation{}, err
		}
		if key.Key == "" || key.Secret == "" {
			return KomodoBootstrapObservation{}, errors.New("Komodo bootstrap API key returned no private credentials")
		}
		state.APIKey, state.APISecret, state.APIKeyState = key.Key, key.Secret, "ready"
		if err := saveKomodoBootstrapCustody(view, state); err != nil {
			return KomodoBootstrapObservation{}, err
		}
	}
	if _, err := o.compose(ctx, directory, "up", "-d", "--wait", "--wait-timeout", "180", "periphery"); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	servers, err := waitKomodoLocalServer(ctx, client, endpoint, state.APIKey, state.APISecret)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	if state.ServerID != "" && state.ServerID != servers.ID {
		return KomodoBootstrapObservation{}, errors.New("Komodo registered node identity changed; preserve deployment custody and repair the existing control plane")
	}
	state.ServerID = servers.ID
	if err := saveKomodoBootstrapCustody(view, state); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	if err := o.verifyControlTLS(ctx, document.DomainBase); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	images := map[string]string{}
	for _, image := range document.Components {
		container, err := o.compose(ctx, directory, "ps", "--quiet", image.ID)
		if err != nil {
			return KomodoBootstrapObservation{}, err
		}
		id := strings.TrimSpace(string(container))
		if len(id) != 64 {
			return KomodoBootstrapObservation{}, errors.New("Komodo bootstrap has no single running component identity")
		}
		actual, err := o.run(ctx, []string{"inspect", "--format", "{{.Config.Image}}", id}, directory)
		if err != nil {
			return KomodoBootstrapObservation{}, err
		}
		images[image.ID] = strings.TrimSpace(string(actual))
		if images[image.ID] != image.ImageRef+"@"+image.ImageDigest {
			return KomodoBootstrapObservation{}, errors.New("Komodo component differs from its selected image pin")
		}
	}
	cfg := map[string]any{"platform": "komodo", "baseUrl": endpoint, "apiKey": state.APIKey, "apiSecret": state.APISecret, "serverId": state.ServerID, "bootstrapEvidence": komodoBootstrapEvidence()}
	cfgRaw, err := json.Marshal(cfg)
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	workspaceView, err := root.View(".")
	if err != nil {
		return KomodoBootstrapObservation{}, err
	}
	if _, err := workspaceView.WriteAtomic0600(".stackkit/platform.json", cfgRaw); err != nil {
		return KomodoBootstrapObservation{}, err
	}
	return KomodoBootstrapObservation{Status: "ready", ServerID: state.ServerID, CoreImage: images["core"], PeripheryImage: images["periphery"], PeripheryConnected: true, MinimumTLSVersion: "TLS1.3", PrivateAPI: true}, nil
}

func (o *osKomodoBootstrapOperations) loadOrCreateCustody(ctx context.Context, root *confinedfs.Root, view confinedfs.View, ownerRef string, document architecturev2renderer.KomodoBootstrapDocument) (komodoBootstrapCustody, error) {
	file, err := view.Open("custody.json")
	if err == nil {
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.Mode().Perm()&0077 != 0 {
			return komodoBootstrapCustody{}, errors.New("Komodo custody is not private")
		}
		raw, err := io.ReadAll(io.LimitReader(file, 65537))
		if err != nil || len(raw) > 65536 {
			return komodoBootstrapCustody{}, errors.New("Komodo custody exceeds its bounded schema")
		}
		var state komodoBootstrapCustody
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&state); err != nil {
			return state, errors.New("Komodo custody is invalid")
		}
		if state.Version != "stackkit.komodo-custody/v1" || state.OwnerRef != ownerRef || state.SiteRef != document.SiteRef || state.NodeRef != document.NodeRef || state.Domain != document.DomainBase ||
			state.AdminPassword == "" || state.DatabasePassword == "" || state.JWTSecret == "" || state.CorePrivateKey == "" || state.CorePublicKey == "" || state.PeripheryPrivateKey == "" || state.PeripheryPublicKey == "" {
			return state, errors.New("Komodo custody differs from the exact owner node")
		}
		workspaceView, err := root.View(".")
		if err != nil {
			return state, err
		}
		if _, err := workspaceView.Lstat(".stackkit/platform.json"); err == nil {
			cfg, err := platformdeploy.LoadOwnerKomodoConfig(o.workspace)
			if err != nil || state.APIKey == "" || cfg.APIKey != state.APIKey || cfg.Secret != state.APISecret || cfg.ServerID != state.ServerID {
				return state, errors.New("current platform configuration belongs to another control identity; request an explicit transition before bootstrap")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return state, err
		}
		if err := verifyExistingKomodoImages(view, document); err != nil {
			return state, err
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return komodoBootstrapCustody{}, err
	}
	workspaceView, err := root.View(".")
	if err != nil {
		return komodoBootstrapCustody{}, err
	}
	if _, err := workspaceView.Lstat(".stackkit/platform.json"); err == nil {
		return komodoBootstrapCustody{}, errors.New("an existing platform has no native Komodo custody; use the explicit manager transition before bootstrap")
	} else if !errors.Is(err, os.ErrNotExist) {
		return komodoBootstrapCustody{}, err
	}
	existing, err := o.run(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + komodoBootstrapProject, "--format", "{{.ID}}"}, o.workspace)
	if err != nil {
		return komodoBootstrapCustody{}, err
	}
	if strings.TrimSpace(string(existing)) != "" {
		return komodoBootstrapCustody{}, errors.New("an existing Komodo project has no matching custody; bootstrap refuses to adopt it")
	}
	state := komodoBootstrapCustody{Version: "stackkit.komodo-custody/v1", OwnerRef: ownerRef, SiteRef: document.SiteRef, NodeRef: document.NodeRef, Domain: document.DomainBase}
	for _, target := range []*string{&state.AdminPassword, &state.DatabasePassword, &state.JWTSecret} {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return state, err
		}
		*target = hex.EncodeToString(raw)
	}
	state.CorePrivateKey, state.CorePublicKey, err = newKomodoNoiseKey()
	if err != nil {
		return state, err
	}
	state.PeripheryPrivateKey, state.PeripheryPublicKey, err = newKomodoNoiseKey()
	if err != nil {
		return state, err
	}
	return state, saveKomodoBootstrapCustody(view, state)
}

func verifyExistingKomodoImages(view confinedfs.View, document architecturev2renderer.KomodoBootstrapDocument) error {
	file, err := view.Open("compose.yaml")
	if err != nil {
		return errors.New("existing Komodo custody has no readable installation recipe; inspect it before retrying")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return errors.New("existing Komodo installation recipe exceeds its bounded schema")
	}
	var installed struct {
		Services map[string]struct{ Image string } `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &installed); err != nil || len(installed.Services) != len(document.Components) {
		return errors.New("existing Komodo installation recipe differs from its control dependency")
	}
	for _, component := range document.Components {
		if installed.Services[component.ID].Image != component.ImageRef+"@"+component.ImageDigest {
			return &KomodoBootstrapUpgradeDeniedError{}
		}
	}
	return nil
}

func newKomodoNoiseKey() (string, string, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	public, err := x509.MarshalPKIXPublicKey(key.PublicKey())
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})), nil
}
func saveKomodoBootstrapCustody(view confinedfs.View, state komodoBootstrapCustody) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600("custody.json", raw)
	return err
}
func (o *osKomodoBootstrapOperations) prepareBootstrapFiles(view confinedfs.View, state komodoBootstrapCustody, document architecturev2renderer.KomodoBootstrapDocument) error {
	files := map[string]string{"admin-password": state.AdminPassword, "database-password": state.DatabasePassword, "mongo.env": "MONGO_INITDB_ROOT_PASSWORD=" + state.DatabasePassword + "\n", "jwt-secret": state.JWTSecret, "core.key": state.CorePrivateKey, "core.pub": state.CorePublicKey, "periphery.key": state.PeripheryPrivateKey, "periphery.pub": state.PeripheryPublicKey}
	for name, content := range files {
		if _, err := view.WriteAtomic0600(name, []byte(content)); err != nil {
			return err
		}
	}
	compose, err := renderKomodoBootstrapCompose(o.workspace, document)
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600("compose.yaml", compose)
	return err
}
func (o *osKomodoBootstrapOperations) compose(ctx context.Context, directory string, args ...string) ([]byte, error) {
	base := []string{"compose", "--project-name", komodoBootstrapProject, "-f", filepath.Join(directory, "compose.yaml")}
	return o.run(ctx, append(base, args...), directory)
}
func (o *osKomodoBootstrapOperations) run(ctx context.Context, args []string, directory string) ([]byte, error) {
	// Periphery and the bootstrap must address the same installed node. Never
	// inherit a caller's remote Docker context or endpoint.
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	cmd.Dir = directory
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "DOCKER_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output := basementCoreBoundedBuffer{remaining: basementCoreProcessOutputLimit}
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Komodo bootstrap node-local Docker operation failed; inspect the private runtime before retrying")
	}
	if output.exceeded {
		return nil, errors.New("Komodo bootstrap Docker output exceeded its bounded observation")
	}
	return output.Bytes(), nil
}
func (o *osKomodoBootstrapOperations) apiEndpoint(ctx context.Context, directory string) (string, error) {
	raw, err := o.compose(ctx, directory, "port", "core", "9120")
	if err != nil {
		return "", err
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(string(raw)))
	if err != nil || host != "127.0.0.1" || port == "" {
		return "", errors.New("Komodo API is not bound exclusively to node loopback")
	}
	return "http://" + net.JoinHostPort(host, port), nil
}
func komodoBootstrapRPC(ctx context.Context, client *http.Client, endpoint, path, kind string, params any, jwt, key, secret string, result any) error {
	payload, err := json.Marshal(map[string]any{"type": kind, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if jwt != "" {
		req.Header.Set("Authorization", jwt)
	}
	if key != "" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("x-api-secret", secret)
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Komodo %s is unavailable", kind)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || response.StatusCode != 200 {
		return fmt.Errorf("Komodo %s did not complete successfully (HTTP %d)", kind, response.StatusCode)
	}
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && len(wrapped.Data) > 0 {
		raw = wrapped.Data
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("Komodo %s returned an invalid response", kind)
	}
	return nil
}

type komodoBootstrapServer struct {
	ID      string `json:"id"`
	MongoID string `json:"_id"`
	Name    string `json:"name"`
	Info    struct {
		State string `json:"state"`
	} `json:"info"`
}

func waitKomodoLocalServer(ctx context.Context, client *http.Client, endpoint, key, secret string) (komodoBootstrapServer, error) {
	deadline := time.NewTimer(120 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		var servers []komodoBootstrapServer
		if err := komodoBootstrapRPC(ctx, client, endpoint, "/read", "ListServers", map[string]any{}, "", key, secret, &servers); err != nil {
			return komodoBootstrapServer{}, err
		}
		for _, server := range servers {
			if server.Name == komodoBootstrapServerName && strings.EqualFold(server.Info.State, "ok") {
				if server.ID == "" {
					server.ID = server.MongoID
				}
				if server.ID != "" {
					return server, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return komodoBootstrapServer{}, ctx.Err()
		case <-deadline.C:
			return komodoBootstrapServer{}, errors.New("Komodo outbound Periphery did not become ready")
		case <-tick.C:
		}
	}
}
func komodoBootstrapEvidence() platformdeploy.BootstrapEvidence {
	evidence := platformdeploy.BootstrapEvidence{Provider: "komodo", Mode: "native-node-local"}
	for capability, markers := range map[platformdeploy.BootstrapCapability][]string{
		platformdeploy.BootstrapCapabilityAPIAccess:      {"api key created"},
		platformdeploy.BootstrapCapabilityTeamManagement: {"registration disabled", "non-admin resource creation disabled"},
		platformdeploy.BootstrapCapabilityProxyRouting:   {"StackKit Traefik"},
		platformdeploy.BootstrapCapabilitySecrets:        {"platformConfig.mode=0600"},
		platformdeploy.BootstrapCapabilityHealthchecks:   {"Core healthcheck"},
		platformdeploy.BootstrapCapabilityServiceHandoff: {"serverId=stackkit-local"},
	} {
		evidence.Capabilities = append(evidence.Capabilities, platformdeploy.BootstrapCapabilityEvidence{Capability: capability, Status: "configured", Evidence: markers})
	}
	evidence.Capabilities = append(evidence.Capabilities, platformdeploy.BootstrapCapabilityEvidence{Capability: platformdeploy.BootstrapCapabilityBackups, Status: "unavailable", ReasonCode: "komodo_backup_restore_unavailable", Guidance: "Preserve node and application custody; use an explicit supported manager transition before backup or restore."})
	return evidence
}

func waitKomodoCoreAPI(ctx context.Context, client *http.Client, endpoint, version string) error {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/version", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 129))
			response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && len(body) <= 128 && strings.TrimSpace(string(body)) == version {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Komodo Core API did not become ready after node-local startup")
		case <-tick.C:
		}
	}
}

func renderKomodoBootstrapCompose(workspace string, document architecturev2renderer.KomodoBootstrapDocument) ([]byte, error) {
	services := map[string]any{}
	host := "komodo-control." + document.DomainBase
	rootCA := filepath.Join(workspace, ".stackkit", "custody", "basement-runtime", "step-ca", "certs", "root_ca.crt")
	for _, component := range document.Components {
		service := map[string]any{"image": component.ImageRef + "@" + component.ImageDigest, "restart": "unless-stopped", "init": true,
			"mem_limit": component.MemoryLimit, "labels": map[string]string{"komodo.skip": "", "stackkit.owner.module": architecturev2renderer.KomodoBootstrapModuleRef}}
		switch component.ID {
		case "mongo":
			service["command"] = []string{"mongod", "--quiet", "--wiredTigerCacheSizeGB", "0.25"}
			// The official entrypoint drops to mongodb before re-reading *_FILE.
			// Keep host credentials 0600 and deliver only Mongo's own password
			// through a separate private environment file.
			service["environment"] = map[string]string{"MONGO_INITDB_ROOT_USERNAME": "stackkits"}
			service["env_file"] = []string{"./mongo.env"}
			service["volumes"] = []string{"mongo-data:/data/db", "mongo-config:/data/configdb"}
			service["networks"] = []string{"komodo-control"}
			service["healthcheck"] = map[string]any{"test": []string{"CMD", "mongosh", "--quiet", "--eval", "db.getSiblingDB('admin').auth(process.env.MONGO_INITDB_ROOT_USERNAME, process.env.MONGO_INITDB_ROOT_PASSWORD); quit(db.adminCommand('ping').ok ? 0 : 1)"}, "interval": "5s", "timeout": "5s", "retries": 24}
		case "core":
			service["environment"] = map[string]string{
				"KOMODO_HOST": "https://" + host + ":8443", "KOMODO_DATABASE_ADDRESS": "mongo:27017", "KOMODO_DATABASE_USERNAME": "stackkits", "KOMODO_DATABASE_PASSWORD_FILE": "/run/secrets/database-password",
				"KOMODO_LOCAL_AUTH": "true", "KOMODO_INIT_ADMIN_USERNAME": "stackkits-admin", "KOMODO_INIT_ADMIN_PASSWORD_FILE": "/run/secrets/admin-password",
				"KOMODO_JWT_SECRET_FILE": "/run/secrets/jwt-secret", "KOMODO_DISABLE_USER_REGISTRATION": "true", "KOMODO_DISABLE_LOCAL_USER_REGISTRATION": "true", "KOMODO_DISABLE_OIDC_USER_REGISTRATION": "true",
				"KOMODO_ENABLE_NEW_USERS": "false", "KOMODO_DISABLE_NON_ADMIN_CREATE": "true", "KOMODO_DISABLE_INIT_RESOURCES": "true",
				"KOMODO_FIRST_SERVER_NAME": komodoBootstrapServerName, "KOMODO_PRIVATE_KEY_FILE": "/config/keys/core.key", "KOMODO_PERIPHERY_PUBLIC_KEY": "file:/config/keys/periphery.pub",
			}
			service["ports"] = []string{"127.0.0.1::9120"}
			service["volumes"] = []string{"./database-password:/run/secrets/database-password:ro", "./admin-password:/run/secrets/admin-password:ro", "./jwt-secret:/run/secrets/jwt-secret:ro",
				"./core.key:/config/keys/core.key:ro", "./periphery.pub:/config/keys/periphery.pub:ro", rootCA + ":/usr/local/share/ca-certificates/stackkit-root.crt:ro"}
			service["depends_on"] = map[string]any{"mongo": map[string]string{"condition": "service_healthy"}}
			service["networks"] = []string{"komodo-control", "basement-core"}
			service["labels"] = map[string]string{"komodo.skip": "", "stackkit.owner.module": architecturev2renderer.KomodoBootstrapModuleRef, "traefik.enable": "true", "traefik.docker.network": "stackkit-basement-core",
				"traefik.http.routers.stackkit-komodo-control.rule": "Host(`" + host + "`) && PathPrefix(`/ws/periphery`)", "traefik.http.routers.stackkit-komodo-control.entrypoints": "komodo-control",
				"traefik.http.routers.stackkit-komodo-control.tls": "true", "traefik.http.routers.stackkit-komodo-control.tls.options": "stackkit-komodo@file",
				"traefik.http.routers.stackkit-komodo-control.tls.certresolver": "stackkits", "traefik.http.routers.stackkit-komodo-control.service": "stackkit-komodo-control",
				"traefik.http.services.stackkit-komodo-control.loadbalancer.server.port": "9120"}
		case "periphery":
			service["environment"] = map[string]string{"PERIPHERY_CORE_ADDRESS": "wss://" + host + ":8443", "PERIPHERY_CONNECT_AS": komodoBootstrapServerName,
				"PERIPHERY_PRIVATE_KEY_FILE": "/config/keys/periphery.key", "PERIPHERY_CORE_PUBLIC_KEYS": "file:/config/keys/core.pub", "PERIPHERY_SERVER_ENABLED": "false", "PERIPHERY_ROOT_DIRECTORY": workspace}
			service["volumes"] = []string{"./periphery.key:/config/keys/periphery.key:ro", "./core.pub:/config/keys/core.pub:ro",
				rootCA + ":/usr/local/share/ca-certificates/stackkit-root.crt:ro", workspace + ":" + workspace, "/var/run/docker.sock:/var/run/docker.sock"}
			service["depends_on"] = []string{"core"}
			service["networks"] = []string{"basement-core"}
		default:
			return nil, errors.New("Komodo bootstrap has an unknown component")
		}
		services[component.ID] = service
	}
	return yaml.Marshal(map[string]any{"name": komodoBootstrapProject, "services": services, "networks": map[string]any{"komodo-control": map[string]any{"internal": true}, "basement-core": map[string]any{"external": true, "name": "stackkit-basement-core"}},
		"volumes": map[string]any{"mongo-data": map[string]any{}, "mongo-config": map[string]any{}}})
}
func (o *osKomodoBootstrapOperations) verifyControlTLS(ctx context.Context, domain string) error {
	raw, err := o.run(ctx, []string{"ps", "--filter", "label=com.docker.compose.project=stackkit-basement-core", "--filter", "label=com.docker.compose.service=router", "--no-trunc", "--quiet"}, o.workspace)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(string(raw))
	if len(id) != 64 {
		return errors.New("Komodo requires the single own-node Core router")
	}
	raw, err = o.run(ctx, []string{"inspect", "--format", `{{(index .NetworkSettings.Networks "stackkit-basement-core").IPAddress}}`, id}, o.workspace)
	if err != nil {
		return err
	}
	address := strings.TrimSpace(string(raw))
	if net.ParseIP(address) == nil {
		return errors.New("Komodo control listener has no private Core network address")
	}
	ca, _, err := localevidence.BasementStepCARootCAPEM(o.workspace)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("Komodo control listener has no valid Kit root")
	}
	tls13 := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{RootCAs: roots, ServerName: "komodo-control." + domain, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}}
	connection, err := tls13.DialContext(ctx, "tcp", net.JoinHostPort(address, "8443"))
	if err != nil {
		return errors.New("Komodo control listener did not prove Kit-rooted TLS 1.3")
	}
	connection.Close()
	tls12 := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{RootCAs: roots, ServerName: "komodo-control." + domain, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}}
	connection, err = tls12.DialContext(ctx, "tcp", net.JoinHostPort(address, "8443"))
	if connection != nil {
		connection.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "protocol version") {
		return errors.New("Komodo control listener did not reject TLS 1.2")
	}
	return nil
}
