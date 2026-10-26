package nativehost

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"gopkg.in/yaml.v3"
)

const runtipiRuntimeDirectory = ".stackkit/runtime/marketplace-runtipi"
const runtipiComposeProject = "stackkit-marketplace-runtipi"

// Derived from the sole regular executable in the archive verified by
// RuntipiCLIArchiveSHA256. Reuse never trusts a local sidecar digest.
const runtipiCLIExecutableSHA256 = "66e52f7d9961946e4116367cefb82d14bc1358da8708ad83b5b20bf7abaf9840"

type RuntipiMarketplaceObservation struct {
	Status             string `json:"status"`
	InstallationID     string `json:"installationId"`
	Release            string `json:"release"`
	OperatorRegistered bool   `json:"operatorRegistered"`
	RegistrationClosed bool   `json:"registrationClosed"`
	APIPrivate         bool   `json:"apiPrivate"`
	UIProtected        bool   `json:"uiProtected"`
}

type RuntipiMarketplaceOperations interface {
	ApplyRuntipiMarketplace(context.Context, architecturev2renderer.MarketplaceRuntipiDocument) (RuntipiMarketplaceObservation, error)
}

type osRuntipiMarketplaceOperations struct{ workspace string }

func NewOSRuntipiMarketplaceOperations(workspace string) (RuntipiMarketplaceOperations, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil || strings.TrimSpace(workspace) == "" {
		return nil, errors.New("Runtipi requires its Owner workspace")
	}
	root, err := confinedfs.Open(absolute)
	if err != nil {
		return nil, err
	}
	root.Close()
	return &osRuntipiMarketplaceOperations{workspace: absolute}, nil
}

func (o *osRuntipiMarketplaceOperations) ApplyRuntipiMarketplace(ctx context.Context, d architecturev2renderer.MarketplaceRuntipiDocument) (RuntipiMarketplaceObservation, error) {
	if ctx == nil || o == nil {
		return RuntipiMarketplaceObservation{}, errors.New("Runtipi requires its node lifecycle owner and context")
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	if _, err := architecturev2renderer.ParseMarketplaceRuntipiDocument(raw); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return RuntipiMarketplaceObservation{}, errors.New("prepared Runtipi bootstrap requires its own Linux amd64 node lifecycle owner")
	}
	owner, err := localevidence.LoadOwnerCustody(o.workspace)
	if err != nil || owner.Binding.SiteRef != d.SiteRef || owner.Binding.NodeRef != d.NodeRef || localevidence.IsPlaceholderOwnerEmail(owner.PocketID.Email) {
		return RuntipiMarketplaceObservation{}, errors.New("Runtipi requires the established exact Kit Owner and email")
	}
	if err := requireMarketplaceKitContext(o.workspace, d.MarketplaceContext()); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	root, err := confinedfs.Open(o.workspace)
	if err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(runtipiRuntimeDirectory)
	if err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	defer lock.Release()
	if err := tx.MkdirAll(runtipiRuntimeDirectory, 0700); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	for _, directory := range []string{runtipiRuntimeDirectory + "/state", ".stackkit/runtime/applications"} {
		if err := tx.MkdirAll(directory, 0700); err != nil {
			return RuntipiMarketplaceObservation{}, err
		}
	}
	view, err := root.View(runtipiRuntimeDirectory)
	if err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	digest := sha256.Sum256(raw)
	recipeDigest := "sha256:" + hex.EncodeToString(digest[:])
	state, err := o.custody(ctx, owner.OwnerRef, owner.PocketID.Email, d, recipeDigest)
	if err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	if err := o.prepareCLI(ctx, view, d); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	settings := map[string]any{}
	settingsFile, settingsErr := view.Open("state/settings.json")
	if settingsErr == nil {
		settingsRaw, readErr := io.ReadAll(io.LimitReader(settingsFile, (128<<10)+1))
		settingsFile.Close()
		if readErr != nil || len(settingsRaw) > 128<<10 || json.Unmarshal(settingsRaw, &settings) != nil || settings == nil {
			return RuntipiMarketplaceObservation{}, errors.New("Runtipi retained settings are invalid")
		}
	} else if !errors.Is(settingsErr, os.ErrNotExist) {
		return RuntipiMarketplaceObservation{}, settingsErr
	}
	settings["domain"], settings["localDomain"] = d.Host(), d.Host()
	settings["guestDashboard"], settings["allowErrorMonitoring"] = false, false
	if d.Route.ID != "" {
		proxyIP, err := o.routerIP(ctx, d)
		if err != nil {
			return RuntipiMarketplaceObservation{}, err
		}
		// Upstream v4.10.2 accepts only a comma-separated string here.
		settings["trustedProxyIps"] = proxyIP
	}
	settingsRaw, err := json.Marshal(settings)
	if err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	if _, err := view.WriteAtomic0600("state/settings.json", settingsRaw); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	directory := filepath.Join(o.workspace, runtipiRuntimeDirectory)
	command := exec.CommandContext(ctx, filepath.Join(directory, "runtipi-cli"), "prepare")
	command.Dir = directory
	// The verified upstream CLI checks Docker internally, so its child reads
	// need the same node daemon as the direct bootstrap/custody operations.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DOCKER_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "DOCKER_HOST=unix:///var/run/docker.sock")
	if _, err := command.CombinedOutput(); err != nil {
		return RuntipiMarketplaceObservation{}, errors.New("verified upstream Runtipi CLI could not prepare its node environment")
	}
	// The upstream CLI creates these files. Narrow their permissions before
	// the control process starts; the enclosing directory is also Owner-only.
	for _, name := range []string{".env", "state/settings.json"} {
		file, err := view.Open(name)
		if err != nil {
			return RuntipiMarketplaceObservation{}, errors.New("Runtipi preparation did not produce its private configuration")
		}
		err = file.Chmod(0600)
		file.Close()
		if err != nil {
			return RuntipiMarketplaceObservation{}, err
		}
	}
	if err := o.writeOverride(view, d); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	if _, err := o.compose(ctx, "up", "--detach", "--wait", "runtipi-db", "runtipi-queue", "runtipi"); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	endpointRaw, err := o.compose(ctx, "port", "runtipi", "3000")
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(endpointRaw)), "127.0.0.1:") {
		return RuntipiMarketplaceObservation{}, errors.New("Runtipi did not retain a loopback-only node API")
	}
	state.BaseURL = "http://" + strings.TrimSpace(string(endpointRaw))
	if err := waitMarketplaceHTTP(ctx, state.BaseURL); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	adapter := platformdeploy.NewRuntipiAdapter(platformdeploy.HTTPConfig{BaseURL: state.BaseURL})
	current, err := adapter.UserContext(ctx)
	if err != nil || current.Version.Current != d.Release {
		return RuntipiMarketplaceObservation{}, errors.New("Runtipi API version differs from its exact pinned installation")
	}
	if !current.IsConfigured && !state.Bootstrapped {
		err = adapter.Register(ctx, state.Username, state.Password)
	} else {
		err = adapter.Login(ctx, state.Username, state.Password)
	}
	if err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	current, err = adapter.UserContext(ctx)
	if err != nil || !current.IsConfigured || !current.IsLoggedIn {
		return RuntipiMarketplaceObservation{}, errors.New("Runtipi operator and closed-registration readback is incomplete")
	}
	if d.Route.ID != "" {
		if err := (&osMarketplaceOperations{workspace: o.workspace}).verifyPrivateUI(ctx, d.MarketplaceContext()); err != nil {
			return RuntipiMarketplaceObservation{}, err
		}
	}
	state.Bootstrapped = true
	if err := saveRuntipiInstallationCustody(o.workspace, state); err != nil {
		return RuntipiMarketplaceObservation{}, err
	}
	return RuntipiMarketplaceObservation{Status: "ready", InstallationID: state.InstallationID, Release: d.Release, OperatorRegistered: true, RegistrationClosed: true, APIPrivate: true, UIProtected: d.Route.ID != ""}, nil
}

func (o *osRuntipiMarketplaceOperations) routerIP(ctx context.Context, d architecturev2renderer.MarketplaceRuntipiDocument) (string, error) {
	project, network, err := marketplaceCoreRuntime(d.MarketplaceContext())
	if err != nil {
		return "", err
	}
	owner := &osMarketplaceOperations{workspace: o.workspace}
	raw, err := owner.run(ctx, []string{"ps", "--filter", "label=com.docker.compose.project=" + project, "--filter", "label=com.docker.compose.service=router", "--no-trunc", "--quiet"}, nil, o.workspace)
	id := strings.TrimSpace(string(raw))
	if err != nil || len(id) != 64 {
		return "", errors.New("Runtipi requires its exact single Kit router")
	}
	raw, err = owner.run(ctx, []string{"inspect", "--format", fmt.Sprintf(`{{(index .NetworkSettings.Networks %q).IPAddress}}`, network), id}, nil, o.workspace)
	ip := strings.TrimSpace(string(raw))
	if err != nil || net.ParseIP(ip) == nil {
		return "", errors.New("Runtipi Kit router private address is missing")
	}
	return ip, nil
}

func (o *osRuntipiMarketplaceOperations) custody(ctx context.Context, owner, email string, d architecturev2renderer.MarketplaceRuntipiDocument, digest string) (runtipiInstallationCustody, error) {
	raw, err := localevidence.ResolveEncryptedLocalIssuedSecret(o.workspace, runtipiInstallationSecretRef)
	if err == nil {
		defer clear(raw)
		var state runtipiInstallationCustody
		if json.Unmarshal(raw, &state) != nil || state.OwnerRef != owner || state.SiteRef != d.SiteRef || state.NodeRef != d.NodeRef || state.Domain != d.Host() || state.RecipeDigest != digest || state.Password == "" || state.InstallationID == "" {
			return state, errors.New("Runtipi custody or pin changed; use an explicit checkpointed transition")
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return runtipiInstallationCustody{}, err
	}
	for _, query := range [][]string{
		{"ps", "--all", "--filter", "label=com.docker.compose.project=" + runtipiComposeProject, "--quiet"},
		{"network", "ls", "--filter", "name=^runtipi_tipi_main_network$", "--quiet"},
	} {
		output, err := (&osMarketplaceOperations{workspace: o.workspace}).run(ctx, query, nil, o.workspace)
		if err != nil || strings.TrimSpace(string(output)) != "" {
			return runtipiInstallationCustody{}, errors.New("Runtipi refuses to adopt an existing platform or control network without its encrypted node custody")
		}
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return runtipiInstallationCustody{}, err
	}
	password := hex.EncodeToString(random)
	clear(random)
	identity := sha256.Sum256([]byte(owner + "/" + d.SiteRef + "/" + d.NodeRef + "/" + password))
	state := runtipiInstallationCustody{OwnerRef: owner, SiteRef: d.SiteRef, NodeRef: d.NodeRef, InstallationID: hex.EncodeToString(identity[:]), Username: email, Password: password, Domain: d.Host(), RecipeDigest: digest}
	return state, saveRuntipiInstallationCustody(o.workspace, state)
}

func saveRuntipiInstallationCustody(workspace string, state runtipiInstallationCustody) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	defer clear(raw)
	prior, readErr := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef)
	if readErr == nil {
		defer clear(prior)
		var fields, changed map[string]json.RawMessage
		if json.Unmarshal(prior, &fields) != nil || json.Unmarshal(raw, &changed) != nil {
			return errors.New("retained installation payload invalid")
		}
		for key, value := range changed {
			fields[key] = value
		}
		merged, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		defer clear(merged)
		return localevidence.StoreEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef, merged)
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	return localevidence.StoreEncryptedLocalIssuedSecret(workspace, runtipiInstallationSecretRef, raw)
}

func (o *osRuntipiMarketplaceOperations) prepareCLI(ctx context.Context, view confinedfs.View, d architecturev2renderer.MarketplaceRuntipiDocument) error {
	if file, err := view.Open("runtipi-cli"); err == nil {
		stat, statErr := file.Stat()
		hash := sha256.New()
		_, readErr := io.Copy(hash, io.LimitReader(file, (64<<20)+1))
		file.Close()
		if statErr == nil && stat.Mode().IsRegular() && stat.Size() <= 64<<20 && readErr == nil && hex.EncodeToString(hash.Sum(nil)) == runtipiCLIExecutableSHA256 {
			file, err := view.Open("runtipi-cli")
			if err != nil {
				return err
			}
			defer file.Close()
			return file.Chmod(0700)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/runtipi/runtipi/releases/download/"+d.Release+"/runtipi-cli-linux-x86_64.tar.gz", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 90 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Runtipi release archive returned HTTP %d", response.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(response.Body, (64<<20)+1))
	if err != nil || len(archive) > 64<<20 {
		return errors.New("Runtipi release archive exceeds its bounded download")
	}
	hash := sha256.Sum256(archive)
	if hex.EncodeToString(hash[:]) != d.CLIArchiveHash {
		return errors.New("Runtipi release archive checksum differs from CUE admission")
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer compressed.Close()
	tarReader := tar.NewReader(compressed)
	header, err := tarReader.Next()
	if err != nil || header.Name != "runtipi-cli-linux-x86_64" || header.Typeflag != tar.TypeReg || header.Size > 64<<20 {
		return errors.New("Runtipi archive has an unexpected executable member")
	}
	executable, err := io.ReadAll(io.LimitReader(tarReader, (64<<20)+1))
	if err != nil || len(executable) > 64<<20 {
		return errors.New("Runtipi CLI exceeds its bounded executable size")
	}
	executableHash := sha256.Sum256(executable)
	if hex.EncodeToString(executableHash[:]) != runtipiCLIExecutableSHA256 {
		return errors.New("Runtipi executable checksum differs from its verified release archive")
	}
	if _, err := view.WriteAtomic0600("runtipi-cli", executable); err != nil {
		return err
	}
	file, err := view.Open("runtipi-cli")
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Chmod(0700)
}

func (o *osRuntipiMarketplaceOperations) writeOverride(view confinedfs.View, d architecturev2renderer.MarketplaceRuntipiDocument) error {
	_, coreNetwork, err := marketplaceCoreRuntime(d.MarketplaceContext())
	if err != nil {
		return err
	}
	services := map[string]any{}
	for _, component := range d.Components {
		service := map[string]any{"runtipi": "runtipi", "postgres": "runtipi-db", "queue": "runtipi-queue"}[component.ID].(string)
		labels := map[string]string{"traefik.enable": "false", "stackkit.owner.node": d.NodeRef, "stackkit.owner.workload": "marketplace"}
		entry := map[string]any{"container_name": runtipiComposeProject + "-" + component.ID, "image": component.ImageRef + "@" + component.ImageDigest, "mem_limit": component.MemoryLimit, "labels": labels}
		if component.ID == "runtipi" {
			var ports yaml.Node
			if err := ports.Encode([]string{"127.0.0.1::3000"}); err != nil {
				return err
			}
			ports.Tag = "!override"
			entry["ports"] = &ports
			entry["networks"] = []string{"tipi_main_network", "kit-core"}
			entry["volumes"] = []string{filepath.Join(o.workspace, ".stackkit/runtime/applications") + ":" + filepath.Join(o.workspace, ".stackkit/runtime/applications") + ":ro"}
			if d.Route.ID != "" {
				entry["labels"] = standaloneComposeRouteLabels(d.Route, false)
			}
		}
		// Do not merge any upstream proxy labels into the Kit-owned route.
		var labelNode yaml.Node
		if err := labelNode.Encode(entry["labels"]); err != nil {
			return err
		}
		labelNode.Tag = "!override"
		entry["labels"] = &labelNode
		if component.ID == "queue" || component.ID == "postgres" {
			// Dependencies communicate on the upstream internal network only.
			// Reset upstream publications, including PostgreSQL's 5432:5432.
			entry["ports"] = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!reset"}
		}
		services[service] = entry
	}
	// Only these three services are explicitly started. The upstream proxy
	// remains in its original recipe and never competes with the Kit router.
	raw, err := yaml.Marshal(map[string]any{"services": services, "networks": map[string]any{"kit-core": map[string]any{"external": true, "name": coreNetwork}}})
	if err != nil {
		return err
	}
	_, err = view.WriteAtomic0600("stackkit-compose.yml", raw)
	return err
}

func (o *osRuntipiMarketplaceOperations) compose(ctx context.Context, args ...string) ([]byte, error) {
	directory := filepath.Join(o.workspace, runtipiRuntimeDirectory)
	commandArgs := []string{"compose", "--project-name", runtipiComposeProject, "--env-file", filepath.Join(directory, ".env"), "-f", filepath.Join(directory, "docker-compose.yml"), "-f", filepath.Join(directory, "stackkit-compose.yml")}
	// Bootstrap and custody discovery must inspect the same node daemon used
	// by the Kit router, regardless of an inherited Docker context or host.
	output, err := (&osMarketplaceOperations{workspace: o.workspace}).run(ctx, append(commandArgs, args...), nil, directory)
	if err != nil {
		return nil, fmt.Errorf("Runtipi node Compose operation failed: %w", err)
	}
	return output, nil
}
