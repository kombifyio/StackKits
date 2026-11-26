package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const paperworkInstallPath = ".stackkit/paperwork"

func linkedPaperworkInstallEnabled() bool {
	return os.Getenv("STACKKIT_LINKED_PAPERWORK_INSTALL") == "true"
}

func newInstallPaperworkBundleCommand() *cobra.Command {
	var pin hostdelivery.PaperworkBundlePin
	var approved bool
	var tenant, server string
	var generation uint64
	cmd := &cobra.Command{
		Use:    "install-paperwork-bundle",
		Short:  "Start the pinned self-hosted Paperwork bundle on this node",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime.GOOS != "linux" || !approved {
				return errors.New("Paperwork installation requires a Linux node and --owner-approve")
			}
			if !linkedPaperworkInstallEnabled() {
				return errors.New("Paperwork installation is disabled on this node")
			}
			authMode := os.Getenv("PAPERWORK_AUTH_MODE")
			if authMode == "" {
				authMode = "oidc"
			}
			workspace := getWorkDir()
			return withLifecycleMutation(workspace, "host delivery install-paperwork-bundle", func() error {
				environment, err := paperworkInstallEnvironment(workspace, authMode, tenant, server, generation)
				if err != nil {
					return err
				}
				version, err := installPaperworkBundle(cmd.Context(), workspace, pin, authMode, environment)
				if err != nil {
					return err
				}
				return writeCommandResult(cmd, cmd.CommandPath(), map[string]string{
					"source_sha": pin.SourceSHA, "image": pin.Image, "bundle_sha256": pin.BundleSHA256, "version": version, "state": "compose_ready",
				})
			})
		},
	}
	addPaperworkBundlePinFlags(cmd, &pin)
	cmd.Flags().BoolVar(&approved, "owner-approve", false, "Authorize starting this pinned Paperwork installation on the local Docker engine")
	cmd.Flags().StringVar(&tenant, "tenant-ref", "", "Enrolled tenant from the Guard for linked sign-in")
	cmd.Flags().StringVar(&server, "server-id", "", "Enrolled server from the Guard for linked sign-in")
	cmd.Flags().Uint64Var(&generation, "guard-generation", 0, "Current enrolled Guard generation for linked sign-in")
	return cmd
}

func paperworkInstallEnvironment(workspace, authMode, tenant, server string, generation uint64) ([]string, error) {
	keys := []string{"PAPERWORK_PUBLIC_URL"}
	switch authMode {
	case "oidc":
		keys = append(keys, "PAPERWORK_OIDC_CLIENT_ID", "PAPERWORK_OIDC_CLIENT_SECRET", "PAPERWORK_OIDC_ISSUER_URL")
	case "linked":
		if os.Getenv("PAPERWORK_OIDC_ISSUER_URL") != "" || os.Getenv("PAPERWORK_OIDC_CLIENT_ID") != "" || os.Getenv("PAPERWORK_OIDC_CLIENT_SECRET") != "" {
			return nil, errors.New("linked Paperwork sign-in requires Vault host delivery, not OIDC overrides")
		}
	default:
		return nil, errors.New("Paperwork installation requires PAPERWORK_AUTH_MODE=oidc or PAPERWORK_AUTH_MODE=linked")
	}
	environment := []string{"LANG=C", "LC_ALL=C", "PAPERWORK_HTTP_BIND=127.0.0.1", "PAPERWORK_HTTP_PORT=8080", "PAPERWORK_AUTH_MODE=" + authMode}
	for _, key := range keys {
		value, err := paperworkInstallEnvValue(key)
		if err != nil {
			return nil, err
		}
		environment = append(environment, key+"="+value)
	}
	if authMode == "linked" {
		record, err := readPaperworkSignInRecord(workspace)
		if err != nil || record.Revoked || record.Tenant != tenant || record.Server != server || record.Credential.GuardGeneration != generation ||
			record.Credential.Origin != os.Getenv("PAPERWORK_PUBLIC_URL") {
			return nil, errors.New("node custody has no linked Paperwork credential for the enrolled generation and configured origin")
		}
		environment = append(environment, "PAPERWORK_OIDC_CLIENT_ID="+record.Credential.ClientID, "PAPERWORK_OIDC_CLIENT_SECRET="+record.Credential.ClientSecret)
	}
	return environment, nil
}

func paperworkInstallEnvValue(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" || len(value) > 4096 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("Paperwork installation requires a valid %s", key)
	}
	if key == "PAPERWORK_PUBLIC_URL" || key == "PAPERWORK_OIDC_ISSUER_URL" {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (key == "PAPERWORK_PUBLIC_URL" && parsed.EscapedPath() != "") {
			return "", fmt.Errorf("Paperwork installation requires an HTTPS %s without credentials, query or fragment", key)
		}
	}
	return value, nil
}

func installPaperworkBundle(ctx context.Context, workspace string, pin hostdelivery.PaperworkBundlePin, authMode string, environment []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	grant, err := localevidence.ResolveEncryptedLocalIssuedSecret(workspace, hostdelivery.PaperworkRegistrySecretRef)
	if err != nil {
		return "", errors.New("node custody has no Paperwork registry grant")
	}
	defer backupcustody.Clear(grant)
	bundle, err := hostdelivery.FetchPaperworkBundle(ctx, pin, grant)
	if err != nil {
		return "", err
	}
	if err := admitPaperworkInstallCompose(bundle.Compose, authMode); err != nil {
		return "", err
	}
	docker, err := exec.LookPath("/usr/bin/docker")
	if err != nil {
		docker, err = exec.LookPath("/usr/local/bin/docker")
		if err != nil {
			return "", errors.New("Paperwork installation requires a system-wide Docker CLI")
		}
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer tx.Close()
	if err := tx.MkdirAll(paperworkInstallPath, 0o700); err != nil {
		return "", err
	}
	info, err := tx.Lstat(paperworkInstallPath)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errors.New("Paperwork installation needs a private node state directory")
	}
	view, err := root.View(paperworkInstallPath)
	if err != nil {
		return "", err
	}
	if err := pinPaperworkInstall(view, tx, bundle.Compose, authMode); err != nil {
		return "", err
	}
	installDir := filepath.Join(root.Name(), filepath.FromSlash(paperworkInstallPath))
	configDir, err := os.MkdirTemp("/dev/shm", "stackkit-paperwork-")
	if err != nil {
		return "", errors.New("Paperwork installation requires private temporary Docker config storage")
	}
	defer os.RemoveAll(configDir)
	const dockerEndpoint = "unix:///var/run/docker.sock"
	registryHost, err := pin.RegistryHost()
	if err != nil {
		return "", err
	}
	if err := pullPaperworkImage(ctx, docker, dockerEndpoint, configDir, registryHost, pin.Image, grant); err != nil {
		return "", err
	}
	if err := tx.VerifyPathIdentity(); err != nil {
		return "", err
	}
	projectHash := sha256.Sum256([]byte(installDir))
	project := "stackkit-paperwork-" + hex.EncodeToString(projectHash[:8])
	command := exec.CommandContext(ctx, docker, "--host", dockerEndpoint, "--config", configDir, "compose",
		"--project-name", project, "--project-directory", installDir, "--env-file", "/dev/null", "-f", "-",
		"up", "-d", "--wait", "--wait-timeout", "180", "--no-build", "--pull", "missing")
	command.Env = environment
	command.Stdin = bytes.NewReader(bundle.Compose)
	if err := command.Run(); err != nil {
		return "", errors.New("Paperwork Compose did not become ready on the local Docker engine")
	}
	return bundle.Version, nil
}

func admitPaperworkInstallCompose(compose []byte, authMode string) error {
	var topology struct {
		Name      string    `yaml:"name"`
		Extension yaml.Node `yaml:"x-paperwork"`
		Services  map[string]struct {
			Image       string            `yaml:"image"`
			Entrypoint  []string          `yaml:"entrypoint"`
			Command     []string          `yaml:"command"`
			User        string            `yaml:"user"`
			Restart     string            `yaml:"restart"`
			NetworkMode string            `yaml:"network_mode"`
			Volumes     []string          `yaml:"volumes"`
			Ports       []string          `yaml:"ports"`
			Environment map[string]string `yaml:"environment"`
			DependsOn   yaml.Node         `yaml:"depends_on"`
			Healthcheck yaml.Node         `yaml:"healthcheck"`
		} `yaml:"services"`
		Volumes map[string]*struct{} `yaml:"volumes"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(compose))
	decoder.KnownFields(true)
	if err := decoder.Decode(&topology); err != nil || topology.Name != "paperwork" || len(topology.Services) != 6 {
		return errors.New("Paperwork Compose topology differs from the admitted local install shape")
	}
	if authMode == "linked" && !paperworkSecretEnvironmentOnly(compose) {
		return errors.New("linked Paperwork Compose must pass the client secret only in the server environment")
	}
	for _, name := range []string{"generate", "postgres", "objects", "init", "context-issuer", "paperwork"} {
		service, exists := topology.Services[name]
		if !exists || !paperworkInstallDigestRef(service.Image) || (service.NetworkMode != "" && (name != "generate" || service.NetworkMode != "none")) {
			return errors.New("Paperwork Compose needs six digest-pinned services without host networking")
		}
		if authMode == "linked" {
			switch name {
			case "init", "context-issuer", "paperwork":
				if service.Environment["PAPERWORK_AUTH_MODE"] != "${PAPERWORK_AUTH_MODE:-oidc}" {
					return errors.New("Paperwork bundle does not propagate linked sign-in to migration, context issuer and server")
				}
			}
			if (name == "context-issuer" || name == "paperwork") && service.Environment["PAPERWORK_OIDC_ISSUER_URL"] != "${PAPERWORK_OIDC_ISSUER_URL:-}" {
				return errors.New("Paperwork bundle does not support linked sign-in without an issuer override")
			}
		}
		if name == "paperwork" {
			if len(service.Ports) != 1 || service.Ports[0] != "${PAPERWORK_HTTP_BIND:-127.0.0.1}:${PAPERWORK_HTTP_PORT:-8080}:8080" {
				return errors.New("Paperwork Compose must publish only the Paperwork loopback port")
			}
		} else if len(service.Ports) != 0 {
			return errors.New("Paperwork Compose dependencies must not publish ports")
		}
		for _, volume := range service.Volumes {
			source, target, valid := strings.Cut(volume, ":")
			options, declared := topology.Volumes[source]
			if !valid || !declared || options != nil || strings.ContainsAny(source, "/\\$.") || !strings.HasPrefix(target, "/") || strings.ContainsAny(target, "\\$\x00\r\n") {
				return errors.New("Paperwork Compose requires named local volumes without host mounts")
			}
		}
	}
	for _, options := range topology.Volumes {
		if options != nil {
			return errors.New("Paperwork Compose volumes must not select an external volume or driver")
		}
	}
	return nil
}

func paperworkSecretEnvironmentOnly(compose []byte) bool {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(compose))
	if decoder.Decode(&document) != nil || decoder.Decode(new(yaml.Node)) != io.EOF || len(document.Content) != 1 {
		return false
	}
	const key = "PAPERWORK_OIDC_CLIENT_SECRET"
	const location = "\x00services\x00paperwork\x00environment\x00" + key
	seen := false
	var walk func(*yaml.Node, string, bool) bool
	walk = func(node *yaml.Node, path string, anchored bool) bool {
		anchored = anchored || node.Anchor != ""
		if node.Kind == yaml.MappingNode {
			for index := 0; index < len(node.Content); index += 2 {
				name, value := node.Content[index], node.Content[index+1]
				if name.Kind != yaml.ScalarNode {
					return false
				}
				field := path + "\x00" + name.Value
				if strings.Contains(name.Value, key) && (field != location || name.Value != key || name.Anchor != "") {
					return false
				}
				if !walk(value, field, anchored) {
					return false
				}
			}
			return true
		}
		if node.Kind == yaml.ScalarNode && strings.Contains(node.Value, key) {
			if anchored || seen || path != location || node.Value != "${"+key+":?set "+key+" in .env}" {
				return false
			}
			seen = true
		}
		for _, child := range node.Content {
			if !walk(child, path+"\x00[]", anchored) {
				return false
			}
		}
		return true
	}
	return walk(document.Content[0], "", false) && seen
}

func paperworkInstallDigestRef(image string) bool {
	name, digest, found := strings.Cut(image, "@sha256:")
	if !found || name == "" || strings.ContainsAny(name, "$ \t\r\n") || digest != strings.ToLower(digest) || len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func pinPaperworkInstall(view confinedfs.View, tx *confinedfs.Transaction, compose []byte, authMode string) error {
	installed, _, err := tx.ReadStableBounded(paperworkInstallPath+"/compose.yaml", 2<<20)
	hasCompose := err == nil
	if !hasCompose && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hasCompose && !bytes.Equal(installed, compose) {
		return errors.New("Paperwork installation already pins different Compose bytes; use a separate upgrade operation")
	}
	mode, _, err := tx.ReadStableBounded(paperworkInstallPath+"/auth-mode", 16)
	switch {
	case err == nil:
		if string(mode) != authMode+"\n" {
			return errors.New("Paperwork installation already pins a different sign-in mode; use a separate upgrade operation")
		}
	case errors.Is(err, os.ErrNotExist):
		if hasCompose && authMode != "oidc" {
			return errors.New("Paperwork installation has a legacy OIDC pin; use a separate upgrade operation")
		}
		if _, err := view.WriteAtomic0600NoReplace("auth-mode", []byte(authMode+"\n")); err != nil {
			return err
		}
	default:
		return err
	}
	if hasCompose {
		return nil
	}
	_, err = view.WriteAtomic0600NoReplace("compose.yaml", compose)
	return err
}

func pullPaperworkImage(ctx context.Context, docker, endpoint, configDir, registryHost, image string, material []byte) error {
	grant, err := hostdelivery.ParsePaperworkRegistryGrant(material, time.Now(), 30*time.Second)
	if err != nil {
		return err
	}
	if !grant.AuthorizesRegistry(registryHost) {
		return errors.New("Paperwork registry grant does not authorize the admitted image registry")
	}
	encoded, err := json.Marshal(map[string]any{"auths": map[string]any{registryHost: map[string]string{"registrytoken": grant.RegistryToken}}})
	if err != nil {
		return errors.New("could not prepare Paperwork pull authority")
	}
	defer backupcustody.Clear(encoded)
	config := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(config, encoded, 0o600); err != nil {
		return errors.New("could not store temporary Paperwork pull authority")
	}
	command := exec.CommandContext(ctx, docker, "--host", endpoint, "--config", configDir, "image", "pull", image)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	pullErr := command.Run()
	if err := os.Remove(config); err != nil {
		return errors.New("could not remove temporary Paperwork pull authority")
	}
	if pullErr != nil {
		return errors.New("Docker could not pull the admitted Paperwork image from the private registry")
	}
	return nil
}
