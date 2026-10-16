package platformdeploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

var ErrNativeKomodoCustodyBinding = errors.New("native Komodo custody differs from the owner node")

// LoadOwnerKomodoConfigForServer never reinterprets a retained workload as a
// claim on another Core. Native and retained-default credentials keep their
// existing custody owners; the recorded server selects the exact one.
func LoadOwnerKomodoConfigForServer(ctx context.Context, workspace, serverID string) (HTTPConfig, error) {
	if strings.TrimSpace(serverID) == "" {
		return HTTPConfig{}, errors.New("Komodo control requires its recorded server identity")
	}
	cfg, err := LoadOwnerKomodoConfig(workspace)
	if err == nil && cfg.ServerID == serverID {
		return cfg, nil
	}
	native, err := LoadOwnerNativeKomodoConfig(ctx, workspace)
	if err != nil {
		return HTTPConfig{}, err
	}
	if native.ServerID != serverID {
		return HTTPConfig{}, errors.New("no owner-custodied Komodo Core matches the recorded server")
	}
	return native, nil
}

// LoadOwnerNativeKomodoConfig consumes only the existing native bootstrap
// custody and its exact private Core container. It creates no credentials and
// does not replace the default controller used by retained workloads.
func LoadOwnerNativeKomodoConfig(ctx context.Context, workspace string) (HTTPConfig, error) {
	if ctx == nil {
		return HTTPConfig{}, errors.New("native Komodo observation requires a bounded context")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return HTTPConfig{}, err
	}
	basement, err := localevidence.LoadBasementRuntimeCustody(workspace)
	if err != nil {
		return HTTPConfig{}, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return HTTPConfig{}, err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return HTTPConfig{}, err
	}
	file, err := view.Open(".stackkit/runtime/komodo/custody.json")
	if err != nil {
		return HTTPConfig{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return HTTPConfig{}, errors.New("native Komodo custody is not private")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return HTTPConfig{}, errors.New("native Komodo custody exceeds its bounded schema")
	}
	var state struct {
		Version     string `json:"version"`
		OwnerRef    string `json:"ownerRef"`
		SiteRef     string `json:"siteRef"`
		NodeRef     string `json:"nodeRef"`
		Domain      string `json:"domain"`
		APIKey      string `json:"apiKey"`
		APISecret   string `json:"apiSecret"`
		APIKeyState string `json:"apiKeyState"`
		ServerID    string `json:"serverId"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return HTTPConfig{}, errors.New("native Komodo custody cannot be decoded")
	}
	if state.Version != "stackkit.komodo-custody/v1" || state.OwnerRef != owner.OwnerRef || state.SiteRef != owner.Binding.SiteRef || state.NodeRef != owner.Binding.NodeRef || state.Domain != basement.Domain {
		return HTTPConfig{}, ErrNativeKomodoCustodyBinding
	}
	if state.APIKeyState != "ready" || state.APIKey == "" || state.APISecret == "" || state.ServerID == "" {
		return HTTPConfig{}, errors.New("native Komodo API identity is not established; inspect its existing bootstrap custody")
	}
	ids, err := nativeNodeDocker(ctx, "ps", "--filter", "label=com.docker.compose.project=stackkit-komodo", "--filter", "label=com.docker.compose.service=core", "--no-trunc", "--quiet").Output()
	if err != nil {
		return HTTPConfig{}, errors.New("native Komodo Core container cannot be observed")
	}
	id := strings.TrimSpace(string(ids))
	if len(id) != 64 || strings.ContainsAny(id, "\r\n ") {
		return HTTPConfig{}, errors.New("native Komodo requires exactly one running Core")
	}
	observed, err := nativeNodeDocker(ctx, "inspect", id).Output()
	if err != nil {
		return HTTPConfig{}, errors.New("native Komodo Core binding cannot be observed")
	}
	var containers []struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	if json.Unmarshal(observed, &containers) != nil || len(containers) != 1 {
		return HTTPConfig{}, errors.New("native Komodo Core binding is ambiguous")
	}
	c := containers[0]
	directory := filepath.Join(workspace, ".stackkit", "runtime", "komodo")
	if c.Config.Labels["com.docker.compose.project"] != "stackkit-komodo" || c.Config.Labels["com.docker.compose.service"] != "core" || c.Config.Labels["com.docker.compose.project.working_dir"] != directory {
		return HTTPConfig{}, errors.New("native Komodo Core belongs to another host project")
	}
	ports := c.NetworkSettings.Ports["9120/tcp"]
	if len(ports) != 1 || ports[0].HostIP != "127.0.0.1" || ports[0].HostPort == "" {
		return HTTPConfig{}, errors.New("native Komodo API is not bound exclusively to node loopback")
	}
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return HTTPConfig{BaseURL: "http://" + net.JoinHostPort(ports[0].HostIP, ports[0].HostPort), APIKey: state.APIKey, Secret: state.APISecret, ServerID: state.ServerID, Client: client}, nil
}

func nativeNodeDocker(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "DOCKER_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	return cmd
}
