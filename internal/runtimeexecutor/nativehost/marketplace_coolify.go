package nativehost

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"gopkg.in/yaml.v3"
)

const marketplaceDirectory = localbackuppolicy.MarketplaceRuntimeDirectory
const marketplaceProject = localbackuppolicy.MarketplaceComposeProject

type MarketplaceObservation struct {
	Status             string                     `json:"status"`
	RootAdmin          bool                       `json:"rootAdmin"`
	RegistrationClosed bool                       `json:"registrationClosed"`
	AutoUpdateDisabled bool                       `json:"autoUpdateDisabled"`
	PocketIDClientID   string                     `json:"pocketIdClientId"`
	ServerID           string                     `json:"serverId"`
	Proxy              string                     `json:"proxy"`
	APIPrivate         bool                       `json:"apiPrivate"`
	Image              string                     `json:"image"`
	UIProtected        bool                       `json:"uiProtected"`
	OwnHostSSH         bool                       `json:"ownHostSSH"`
	SSH                *marketplaceSSHObservation `json:"ssh,omitempty"`
}

type MarketplaceOperations interface {
	ApplyMarketplace(context.Context, architecturev2renderer.MarketplaceCoolifyDocument) (MarketplaceObservation, error)
}

// MarketplaceUpgradeAuthority is supplied by the installed lifecycle owner.
// It must verify the signed target-Apply join and prior executor checkpoint.
type MarketplaceUpgradeAuthority func(context.Context, architecturev2renderer.MarketplaceCoolifyDocument) error

type osMarketplaceOperations struct {
	workspace string
	upgrade   MarketplaceUpgradeAuthority
}
type marketplaceCustody struct {
	Version          string `json:"version"`
	OwnerRef         string `json:"ownerRef"`
	SiteRef          string `json:"siteRef"`
	NodeRef          string `json:"nodeRef"`
	Domain           string `json:"domain"`
	AppID            string `json:"appId"`
	AppKey           string `json:"appKey"`
	AdminPassword    string `json:"adminPassword"`
	DatabasePassword string `json:"databasePassword"`
	RedisPassword    string `json:"redisPassword"`
	PusherKey        string `json:"pusherKey"`
	PusherSecret     string `json:"pusherSecret"`
	APIToken         string `json:"apiToken,omitempty"`
	ServerID         string `json:"serverId,omitempty"`
}

func NewOSMarketplaceOperations(workspace string, upgrade ...MarketplaceUpgradeAuthority) (MarketplaceOperations, error) {
	if len(upgrade) > 1 {
		return nil, errors.New("Marketplace requires one installed upgrade authority")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil || strings.TrimSpace(workspace) == "" {
		return nil, errors.New("Marketplace requires its owner workspace")
	}
	root, err := confinedfs.Open(absolute)
	if err != nil {
		return nil, err
	}
	root.Close()
	operations := &osMarketplaceOperations{workspace: absolute}
	if len(upgrade) == 1 {
		operations.upgrade = upgrade[0]
	}
	return operations, nil
}

func (o *osMarketplaceOperations) ApplyMarketplace(ctx context.Context, d architecturev2renderer.MarketplaceCoolifyDocument) (MarketplaceObservation, error) {
	if ctx == nil || o == nil {
		return MarketplaceObservation{}, errors.New("Marketplace requires its node operations owner")
	}
	if err := architecturev2renderer.ValidateMarketplaceCoolifyDocument(d); err != nil {
		return MarketplaceObservation{}, err
	}
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil {
		return MarketplaceObservation{}, err
	}
	if owner.Binding.SiteRef != d.SiteRef || owner.Binding.NodeRef != d.NodeRef {
		return MarketplaceObservation{}, errors.New("Marketplace names another owner node")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return MarketplaceObservation{}, errors.New("Marketplace installation runs on its own Linux node as the installed lifecycle owner")
	}
	if err := requireMarketplaceKitContext(o.workspace, d); err != nil {
		return MarketplaceObservation{}, err
	}
	if localevidence.IsPlaceholderOwnerEmail(owner.PocketID.Email) {
		return MarketplaceObservation{}, errors.New("Marketplace requires established Kit custody and the root owner's email")
	}
	root, err := confinedfs.Open(o.workspace)
	if err != nil {
		return MarketplaceObservation{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return MarketplaceObservation{}, err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(marketplaceDirectory)
	if err != nil {
		return MarketplaceObservation{}, err
	}
	defer lock.Release()
	if err := tx.MkdirAll(marketplaceDirectory, 0700); err != nil {
		return MarketplaceObservation{}, err
	}
	view, err := root.View(marketplaceDirectory)
	if err != nil {
		return MarketplaceObservation{}, err
	}
	state, err := o.loadMarketplaceCustody(ctx, view, owner.OwnerRef, d)
	if err != nil {
		return MarketplaceObservation{}, err
	}
	service, err := localowner.NewService(o.workspace)
	if err != nil {
		return MarketplaceObservation{}, err
	}
	client, err := service.EnsureApplicationOIDCClient(ctx, localowner.ApplicationOIDCClientRequest{
		RequiredPrivilege: "user", ClientID: localowner.ApplicationOIDCClientID("marketplace"), Name: "Marketplace",
		CallbackURL: "https://" + d.Host() + "/auth/oidc/callback", SecretRef: localowner.ApplicationOIDCClientSecretRef("marketplace"),
	})
	if err != nil {
		return MarketplaceObservation{}, err
	}
	secret, err := localevidence.ResolveLocalSecretMaterial(o.workspace, localowner.ApplicationOIDCClientSecretRef("marketplace"))
	if err != nil {
		return MarketplaceObservation{}, err
	}
	defer clear(secret)
	if err := o.prepareMarketplace(view, state, owner.PocketID.Email, d); err != nil {
		return MarketplaceObservation{}, err
	}
	directory := filepath.Join(o.workspace, filepath.FromSlash(marketplaceDirectory))
	if _, err := o.compose(ctx, directory, nil, "run", "--rm", "--no-deps", "--user", "0", "--entrypoint", "/bin/sh", "coolify", "-ec",
		"for path in ssh applications databases services backups images; do mkdir -p /var/www/html/storage/app/$path; chown -R 9999:9999 /var/www/html/storage/app/$path; done"); err != nil {
		return MarketplaceObservation{}, fmt.Errorf("initialize Marketplace storage: %w", err)
	}
	if _, err := o.compose(ctx, directory, nil, "up", "-d", "--wait", "--wait-timeout", "240", "postgres", "redis"); err != nil {
		return MarketplaceObservation{}, fmt.Errorf("start Marketplace dependencies: %w", err)
	}
	args := []string{"up", "-d", "--wait", "--wait-timeout", "240"}
	browserCAReload := false
	if d.KitRef == "basement-kit" {
		_, _, browserCAReload, err = localevidence.BasementBrowserCAMigrationChain(o.workspace)
		if err != nil {
			return MarketplaceObservation{}, err
		}
	}
	if state.ServerID == "" || browserCAReload {
		args = append(args, "--force-recreate")
	}
	args = append(args, "coolify")
	if _, err := o.compose(ctx, directory, nil, args...); err != nil {
		return MarketplaceObservation{}, fmt.Errorf("start Marketplace application: %w", err)
	}
	endpoint, err := o.endpoint(ctx, directory)
	if err != nil {
		return MarketplaceObservation{}, err
	}
	if err := waitMarketplaceHTTP(ctx, endpoint); err != nil {
		return MarketplaceObservation{}, err
	}
	input, _ := json.Marshal(map[string]string{"ownerEmail": owner.PocketID.Email, "clientId": client.ClientID, "clientSecret": string(secret), "issuer": client.Issuer, "origin": "https://" + d.Host(), "existingToken": state.APIToken})
	defer clear(input)
	raw, err := o.compose(ctx, directory, input, "exec", "-T", "coolify", "php", "-r", strings.TrimPrefix(marketplaceBootstrapPHP, "<?php\n"))
	if err != nil {
		return MarketplaceObservation{}, fmt.Errorf("configure Marketplace: %w", err)
	}
	defer clear(raw)
	var result struct {
		Observation MarketplaceObservation `json:"observation"`
		Token       string                 `json:"token"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.Observation.RootAdmin || !result.Observation.RegistrationClosed || !result.Observation.AutoUpdateDisabled ||
		result.Observation.Proxy != "NONE" || result.Observation.ServerID == "" || result.Token == "" {
		return MarketplaceObservation{}, errors.New("Marketplace admin bootstrap failed closed; its private installation is retained for repair")
	}
	if state.ServerID != "" && state.ServerID != result.Observation.ServerID {
		return MarketplaceObservation{}, errors.New("Marketplace server identity changed; preserve its node custody")
	}
	state.APIToken, state.ServerID = result.Token, result.Observation.ServerID
	if err := saveMarketplaceCustody(view, state); err != nil {
		return MarketplaceObservation{}, err
	}
	if err := verifyMarketplaceAPI(ctx, endpoint, state.APIToken); err != nil {
		return MarketplaceObservation{}, err
	}
	for _, image := range d.Components {
		raw, err := o.compose(ctx, directory, nil, "ps", "--quiet", image.ID)
		if err != nil {
			return MarketplaceObservation{}, err
		}
		id := strings.TrimSpace(string(raw))
		if len(id) != 64 {
			return MarketplaceObservation{}, errors.New("Marketplace component has no single running identity")
		}
		actual, err := o.run(ctx, []string{"inspect", "--format", "{{.Config.Image}}", id}, nil, directory)
		if err != nil || strings.TrimSpace(string(actual)) != image.ImageRef+"@"+image.ImageDigest {
			return MarketplaceObservation{}, errors.New("Marketplace running image differs from its selected pin")
		}
	}
	if d.Route.ID != "" {
		if err := o.verifyPrivateUI(ctx, d); err != nil {
			return MarketplaceObservation{}, err
		}
		result.Observation.UIProtected = true
	}
	sshObservation, err := o.ensureMarketplaceSSH(ctx, view, state)
	if err != nil {
		return MarketplaceObservation{}, err
	}
	result.Observation.OwnHostSSH = sshObservation.OwnHostAuthenticated && sshObservation.OutsidePolicyPreserved
	result.Observation.SSH = &sshObservation
	result.Observation.Status = "ready"
	result.Observation.APIPrivate = true
	result.Observation.PocketIDClientID = client.ClientID
	for _, image := range d.Components {
		if image.ID == "coolify" {
			result.Observation.Image = image.ImageRef + "@" + image.ImageDigest
		}
	}
	return result.Observation, nil
}

func (o *osMarketplaceOperations) loadMarketplaceCustody(ctx context.Context, view confinedfs.View, owner string, d architecturev2renderer.MarketplaceCoolifyDocument) (marketplaceCustody, error) {
	var state marketplaceCustody
	file, err := view.Open("custody.json")
	if err == nil {
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.Mode().Perm()&0077 != 0 {
			return state, errors.New("Marketplace custody must remain private")
		}
		raw, err := io.ReadAll(io.LimitReader(file, 65537))
		defer clear(raw)
		if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &state) != nil || state.Version != "stackkit.marketplace-custody/v1" || state.OwnerRef != owner || state.SiteRef != d.SiteRef || state.NodeRef != d.NodeRef || state.Domain != d.DomainBase {
			return state, errors.New("Marketplace private custody differs from the selected node")
		}
		for _, value := range []string{state.AppID, state.AppKey, state.AdminPassword, state.DatabasePassword, state.RedisPassword, state.PusherKey, state.PusherSecret} {
			if value == "" || strings.ContainsAny(value, "\r\n\x00") {
				return state, errors.New("Marketplace private bootstrap material is incomplete; preserve custody and repair it before applying")
			}
		}
		if (state.APIToken == "") != (state.ServerID == "") {
			return state, errors.New("Marketplace API token and server custody are inconsistent")
		}
		composeFile, err := view.Open("compose.yaml")
		if err != nil {
			return state, errors.New("Marketplace custody has no existing installation recipe")
		}
		defer composeFile.Close()
		var recipe struct {
			Services map[string]struct {
				Image string `yaml:"image"`
			} `yaml:"services"`
		}
		rawRecipe, err := io.ReadAll(io.LimitReader(composeFile, 131073))
		if err != nil || len(rawRecipe) > 131072 || yaml.Unmarshal(rawRecipe, &recipe) != nil {
			return state, errors.New("Marketplace installation recipe is invalid")
		}
		for _, c := range d.Components {
			if recipe.Services[c.ID].Image != c.ImageRef+"@"+c.ImageDigest {
				if o.upgrade == nil {
					return state, errors.New("Marketplace image changes require its explicit checkpointed upgrade")
				}
				if err := o.upgrade(ctx, d); err != nil {
					return state, fmt.Errorf("Marketplace checkpointed upgrade: %w", err)
				}
				break
			}
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return state, err
	}
	raw, err := o.run(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=" + marketplaceProject, "--quiet"}, nil, o.workspace)
	if err != nil {
		return state, err
	}
	if strings.TrimSpace(string(raw)) != "" {
		return state, errors.New("Marketplace refuses to adopt an installation without its node custody")
	}
	state = marketplaceCustody{Version: "stackkit.marketplace-custody/v1", OwnerRef: owner, SiteRef: d.SiteRef, NodeRef: d.NodeRef, Domain: d.DomainBase}
	for _, p := range []*string{&state.AppID, &state.AdminPassword, &state.DatabasePassword, &state.RedisPassword, &state.PusherKey, &state.PusherSecret} {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return state, err
		}
		*p = hex.EncodeToString(b)
	}
	state.AdminPassword += "!aA7"
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return state, err
	}
	state.AppKey = "base64:" + base64.StdEncoding.EncodeToString(b)
	return state, saveMarketplaceCustody(view, state)
}

func saveMarketplaceCustody(view confinedfs.View, state marketplaceCustody) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	defer clear(raw)
	_, err = view.WriteAtomic0600("custody.json", raw)
	return err
}

func (o *osMarketplaceOperations) prepareMarketplace(view confinedfs.View, s marketplaceCustody, email string, d architecturev2renderer.MarketplaceCoolifyDocument) error {
	_, coreNetwork, err := marketplaceCoreRuntime(d)
	if err != nil {
		return err
	}
	env := map[string]string{"APP_ID": s.AppID, "APP_NAME": "Marketplace", "APP_ENV": "production", "APP_KEY": s.AppKey, "APP_URL": "https://" + d.Host(), "DB_HOST": "postgres", "DB_PORT": "5432", "DB_DATABASE": "coolify", "DB_USERNAME": "coolify", "DB_PASSWORD": s.DatabasePassword, "REDIS_HOST": "redis", "REDIS_PASSWORD": s.RedisPassword, "PUSHER_APP_ID": s.AppID, "PUSHER_APP_KEY": s.PusherKey, "PUSHER_APP_SECRET": s.PusherSecret, "ROOT_USERNAME": "StackKits Owner", "ROOT_USER_EMAIL": email, "ROOT_USER_PASSWORD": s.AdminPassword, "AUTOUPDATE": "false", "SSL_CERT_FILE": "/etc/ssl/certs/stackkit-ca-bundle.crt", "CURL_CA_BUNDLE": "/etc/ssl/certs/stackkit-ca-bundle.crt"}
	var lines strings.Builder
	for k, v := range env {
		if strings.ContainsAny(v, "\r\n\x00' ") {
			v = "'" + strings.ReplaceAll(v, "'", "'\\''") + "'"
		}
		fmt.Fprintf(&lines, "%s=%s\n", k, v)
	}
	if _, err := view.WriteAtomic0600("coolify.env", []byte(lines.String())); err != nil {
		return err
	}
	caBundle, err := homeIdentityCABundle(o.workspace)
	if err != nil {
		return err
	}
	if _, err := view.WriteAtomic0600("ca-bundle.crt", caBundle); err != nil {
		return err
	}
	// PHP's cURL extension uses curl.cainfo rather than the curl CLI's
	// CURL_CA_BUNDLE environment variable. Both PHP transports consume the
	// same custody-authenticated bundle without relaxing peer verification.
	if _, err := view.WriteAtomic0600("php-trust.ini", []byte("curl.cainfo = \"/etc/ssl/certs/stackkit-ca-bundle.crt\"\nopenssl.cafile = \"/etc/ssl/certs/stackkit-ca-bundle.crt\"\n")); err != nil {
		return err
	}
	// Upstream PHP runs as 9999. Its read-only private mounts must be readable
	// by that principal without making credentials readable by other users.
	for _, name := range []string{"coolify.env", "ca-bundle.crt", "php-trust.ini"} {
		file, err := view.Open(name)
		if err != nil {
			return err
		}
		err = file.Chown(9999, 9999)
		file.Close()
		if err != nil {
			return err
		}
	}
	services := map[string]any{}
	for _, c := range d.Components {
		entry := map[string]any{"image": c.ImageRef + "@" + c.ImageDigest, "restart": "unless-stopped", "mem_limit": c.MemoryLimit, "networks": []string{"marketplace"}, "labels": map[string]string{"stackkit.owner.ref": s.OwnerRef, "stackkit.owner.node": s.NodeRef, "stackkit.owner.workload": "marketplace", "traefik.enable": "false"}}
		switch c.ID {
		case "coolify":
			entry["env_file"] = []string{"./coolify.env"}
			entry["ports"] = []string{"127.0.0.1::8080"}
			entry["extra_hosts"] = []string{"host.docker.internal:host-gateway"}
			entry["volumes"] = []string{"./coolify.env:/var/www/html/.env:ro", "./ca-bundle.crt:/etc/ssl/certs/stackkit-ca-bundle.crt:ro", "./php-trust.ini:/usr/local/etc/php/conf.d/zzzz-stackkit-trust.ini:ro", "marketplace-ssh:/var/www/html/storage/app/ssh", "marketplace-applications:/var/www/html/storage/app/applications", "marketplace-databases:/var/www/html/storage/app/databases", "marketplace-services:/var/www/html/storage/app/services", "marketplace-backups:/var/www/html/storage/app/backups", "marketplace-images:/var/www/html/storage/app/images"}
			entry["networks"] = []string{"marketplace", "kit-core"}
			if d.Route.ID != "" {
				labels := standaloneComposeRouteLabels(d.Route, false)
				labels["stackkit.owner.ref"], labels["stackkit.owner.node"], labels["stackkit.owner.workload"] = s.OwnerRef, s.NodeRef, "marketplace"
				entry["labels"] = labels
			}
			entry["depends_on"] = map[string]any{"postgres": map[string]string{"condition": "service_healthy"}, "redis": map[string]string{"condition": "service_healthy"}}
			entry["healthcheck"] = map[string]any{"test": []string{"CMD-SHELL", "curl --fail http://127.0.0.1:8080/api/health && curl --fail http://127.0.0.1:6001/up"}, "interval": "5s", "start_period": "60s", "retries": 36, "timeout": "3s"}
		case "postgres":
			entry["environment"] = map[string]string{"POSTGRES_USER": "coolify", "POSTGRES_PASSWORD": s.DatabasePassword, "POSTGRES_DB": "coolify"}
			entry["volumes"] = []string{"marketplace-postgres:/var/lib/postgresql/data"}
			entry["healthcheck"] = map[string]any{"test": []string{"CMD", "pg_isready", "-U", "coolify", "-d", "coolify"}, "interval": "5s", "retries": 20, "timeout": "3s"}
		case "redis":
			entry["environment"] = map[string]string{"REDIS_PASSWORD": s.RedisPassword}
			entry["command"] = []string{"sh", "-c", "exec redis-server --save 20 1 --loglevel warning --requirepass \"$$REDIS_PASSWORD\""}
			entry["volumes"] = []string{"marketplace-redis:/data"}
			entry["healthcheck"] = map[string]any{"test": []string{"CMD-SHELL", "REDISCLI_AUTH=\"$$REDIS_PASSWORD\" redis-cli ping | grep -q PONG"}, "interval": "5s", "retries": 20, "timeout": "3s"}
		}
		services[c.ID] = entry
	}
	volumes := map[string]any{}
	for _, name := range []string{"marketplace-postgres", "marketplace-redis", "marketplace-ssh", "marketplace-applications", "marketplace-databases", "marketplace-services", "marketplace-backups", "marketplace-images"} {
		volumes[name] = map[string]any{}
	}
	raw, err := yaml.Marshal(map[string]any{"name": marketplaceProject, "services": services, "volumes": volumes, "networks": map[string]any{"marketplace": map[string]any{}, "kit-core": map[string]any{"external": true, "name": coreNetwork}}})
	if err != nil {
		return err
	}
	defer clear(raw)
	_, err = view.WriteAtomic0600("compose.yaml", raw)
	return err
}

func (o *osMarketplaceOperations) run(ctx context.Context, args []string, input []byte, directory string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	cmd.Dir = directory
	cmd.Stdin = bytes.NewReader(input)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DOCKER_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := ""
		if start := strings.Index(stderr.String(), "Marketplace bootstrap postcondition failed:"); start >= 0 {
			detail = "; " + strings.Split(strings.TrimSpace(stderr.String()[start:]), "\n")[0]
		}
		return nil, fmt.Errorf("Marketplace Docker operation failed (%s)%s: %w", args[0], detail, err)
	}
	if stdout.Len() > 2<<20 {
		return nil, errors.New("Marketplace Docker output exceeds its bound")
	}
	return stdout.Bytes(), nil
}
func (o *osMarketplaceOperations) compose(ctx context.Context, directory string, input []byte, args ...string) ([]byte, error) {
	return o.run(ctx, append([]string{"compose", "--project-name", marketplaceProject, "--file", filepath.Join(directory, "compose.yaml")}, args...), input, directory)
}
func (o *osMarketplaceOperations) endpoint(ctx context.Context, directory string) (string, error) {
	raw, err := o.compose(ctx, directory, nil, "port", "coolify", "8080")
	if err != nil {
		return "", err
	}
	address := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(address, "127.0.0.1:") {
		return "", errors.New("Marketplace API must remain loopback-only")
	}
	return "http://" + address, nil
}
func waitMarketplaceHTTP(ctx context.Context, endpoint string) error {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/health", nil)
		if err != nil {
			return err
		}
		reply, err := client.Do(req)
		if err == nil {
			reply.Body.Close()
			if reply.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Marketplace private API did not become healthy")
		case <-time.After(time.Second):
		}
	}
}
func verifyMarketplaceAPI(ctx context.Context, endpoint, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/v1/projects", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	reply, err := client.Do(req)
	if err != nil {
		return errors.New("Marketplace authenticated API readback failed")
	}
	defer reply.Body.Close()
	if reply.StatusCode != 200 {
		return errors.New("Marketplace API token did not authorize project readback")
	}
	return nil
}

func (o *osMarketplaceOperations) verifyPrivateUI(ctx context.Context, d architecturev2renderer.MarketplaceCoolifyDocument) error {
	coreProject, coreNetwork, err := marketplaceCoreRuntime(d)
	if err != nil {
		return err
	}
	raw, err := o.run(ctx, []string{"ps", "--filter", "label=com.docker.compose.project=" + coreProject, "--filter", "label=com.docker.compose.service=router", "--no-trunc", "--quiet"}, nil, o.workspace)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(string(raw))
	if len(id) != 64 {
		return errors.New("Marketplace requires its own single Core router")
	}
	raw, err = o.run(ctx, []string{"inspect", "--format", fmt.Sprintf(`{{(index .NetworkSettings.Networks %q).IPAddress}}`, coreNetwork), id}, nil, o.workspace)
	if err != nil {
		return err
	}
	ip := strings.TrimSpace(string(raw))
	if net.ParseIP(ip) == nil {
		return errors.New("Marketplace Core router has no own private interface")
	}
	ca, err := homeIdentityCABundle(o.workspace)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("Marketplace Kit CA is invalid")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: d.Route.Host, MinVersion: tls.VersionTLS13}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip, "443"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+d.Route.Host+"/", nil)
	if err != nil {
		return err
	}
	reply, err := client.Do(request)
	if err != nil {
		return errors.New("Marketplace UI Kit CA/TLS verification failed")
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusUnauthorized || reply.TLS == nil || reply.TLS.Version != tls.VersionTLS13 {
		return errors.New("Marketplace UI did not deny anonymous access through the Kit TLS router")
	}
	return nil
}

const marketplaceBootstrapPHP = `<?php
require '/var/www/html/vendor/autoload.php';
$app=require '/var/www/html/bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
try {
 $phase='input';
 $input=json_decode(stream_get_contents(STDIN),true,32,JSON_THROW_ON_ERROR);
 $phase='issuer-trust';
 if (ini_get('curl.cainfo')!=='/etc/ssl/certs/stackkit-ca-bundle.crt' || ini_get('openssl.cafile')!=='/etc/ssl/certs/stackkit-ca-bundle.crt') {throw new RuntimeException('PHP issuer trust is not loaded');}
 $curl=curl_init(rtrim($input['issuer'],'/').'/.well-known/openid-configuration');
 curl_setopt_array($curl,[CURLOPT_RETURNTRANSFER=>true,CURLOPT_TIMEOUT=>20,CURLOPT_SSL_VERIFYPEER=>true,CURLOPT_SSL_VERIFYHOST=>2]);
 $discovery=curl_exec($curl);$discoveryStatus=curl_getinfo($curl,CURLINFO_HTTP_CODE);curl_close($curl);
 if ($discovery===false || $discoveryStatus!==200 || (json_decode($discovery,true,32,JSON_THROW_ON_ERROR)['issuer']??null)!==$input['issuer']) {throw new RuntimeException('Verified node-local issuer discovery failed');}
 $phase='root-admin';
 $root=App\Models\User::find(0);
 if (!$root) { Illuminate\Support\Facades\Artisan::call('db:seed',['--force'=>true]); $root=App\Models\User::find(0); }
 if (!$root || $root->email!==$input['ownerEmail'] || !$root->teams()->where('team_id',0)->wherePivot('role','owner')->exists()) { throw new RuntimeException('Root admin bootstrap not established'); }
 $settings=App\Models\InstanceSettings::findOrFail(0);
 if ($settings->is_registration_enabled) {throw new RuntimeException('Public registration remained enabled');}
 $settings->is_auto_update_enabled=false; $settings->is_api_enabled=true; $settings->save();
 $phase='pocketid';
 $oidc=App\Models\OauthSetting::updateOrCreate(['provider'=>'oidc'],[
  'client_id'=>$input['clientId'],'client_secret'=>$input['clientSecret'],'base_url'=>$input['issuer'],
  'redirect_uri'=>$input['origin'].'/auth/oidc/callback','enabled'=>true,'custom_label'=>'Pocket ID',
  'scopes'=>'openid email profile','allow_registration'=>true,'auto_join_root_team'=>true,'require_email_verified'=>true,'use_pkce'=>true]);
 $server=App\Models\Server::findOrFail(0);
 $phase='proxy';
 $server->proxy=['type'=>'NONE','status'=>'exited'];$server->save();
 $proxy=json_decode($server->getRawOriginal('proxy'),true,32,JSON_THROW_ON_ERROR);
 if(($proxy['type']??null)!=='NONE'){throw new RuntimeException('Stored proxy owner differs');}
 $token=$input['existingToken'];
 $phase='api-token';
 session(['currentTeam'=>App\Models\Team::findOrFail(0)]);
 if (!$token) { $existing=$root->tokens()->where('name','stackkit-node-marketplace')->exists();if($existing){throw new RuntimeException('Uncertain API token issuance; inspect custody');} $token=$root->createToken('stackkit-node-marketplace',['read','write','deploy'])->plainTextToken; }
 $phase='observation';
 echo json_encode(['token'=>$token,'observation'=>['rootAdmin'=>true,'registrationClosed'=>!$settings->is_registration_enabled,'autoUpdateDisabled'=>!$settings->is_auto_update_enabled,'serverId'=>$server->uuid,'proxy'=>$proxy['type']]],JSON_THROW_ON_ERROR);
} catch (Throwable $e) {fwrite(STDERR,"Marketplace bootstrap postcondition failed: ".$phase." (".get_class($e).")\n");exit(1);}
`
