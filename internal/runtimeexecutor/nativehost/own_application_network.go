package nativehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kombifyio/stackkits/internal/ownappfirewall"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const ownEgressLabel = "io.stackkit.own-egress"
const ownNetworkLabel = "io.stackkit.own-network"

var ownNetworkName = regexp.MustCompile(`^stackkit-own-[a-z][a-z0-9-]{0,39}-[a-z][a-z0-9-]*_private$`)
var ownContainerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

func ownApplicationNetworkName(workload, node string) string {
	return "stackkit-" + workload + "-" + node + "_private"
}

type ownNetwork struct {
	Name, Driver string
	Internal     bool
	EnableIPv6   bool
	Options      map[string]string
	Labels       map[string]string
	Containers   map[string]struct {
		IPv4Address string
		MacAddress  string
	}
}

func validOwnNetworkQuery(a []string) bool {
	if len(a) == 5 && a[0] == "network" && a[1] == "ls" && a[2] == "--format" && a[3] == "{{.Name}}" && a[4] == "--filter=label="+ownNetworkLabel+"=v1" {
		return true
	}
	if len(a) == 3 && a[0] == "network" && (a[1] == "inspect" || a[1] == "rm") && ownNetworkName.MatchString(a[2]) {
		return true
	}

	if len(a) == 9 && a[0] == "ps" && a[1] == "--all" && a[2] == "--filter" && a[3] == "label=com.docker.compose.project=stackkit-basement-core" && a[4] == "--filter" && (a[5] == "label=com.docker.compose.service=router" || a[5] == "label=com.docker.compose.service=router-remote") && a[6] == "--format" && a[7] == "{{.ID}}" && a[8] == "--no-trunc" {
		return true
	}
	if len(a) == 6 && a[0] == "ps" && a[1] == "--filter" && strings.HasPrefix(a[2], "label=com.docker.compose.project=") && ownNetworkName.MatchString(strings.TrimPrefix(a[2], "label=com.docker.compose.project=")+"_private") && a[3] == "--format" && a[4] == "{{.ID}}" && a[5] == "--no-trunc" {
		return true
	}
	return false
}
func (o *osStandaloneComposeWorkloadOperations) inspectOwnNetwork(ctx context.Context, name string) (ownNetwork, error) {
	raw, e := o.runner.Run(ctx, []string{"network", "inspect", name}, o.workspaceRoot)
	if e != nil {
		return ownNetwork{}, errors.New("own application private network unavailable")
	}
	var observed []ownNetwork
	if json.Unmarshal(raw, &observed) != nil || len(observed) != 1 {
		return ownNetwork{}, errors.New("invalid own application network observation")
	}
	n := observed[0]
	validMode := n.Internal && len(n.Options) == 0 && n.Labels[ownEgressLabel] == ""
	if n.Labels[ownEgressLabel] == "v1" {
		validMode = !n.Internal && !n.EnableIPv6 && len(n.Options) == 1 && n.Options["com.docker.network.bridge.name"] == ownappfirewall.Bridge(name)
	}
	if n.Name != name || n.Driver != "bridge" || !validMode || n.Labels[ownNetworkLabel] != "v1" || n.Labels["com.docker.compose.project"]+"_private" != name {
		return n, errors.New("own application network is not its admitted internal bridge")
	}
	return n, nil
}
func (o *osStandaloneComposeWorkloadOperations) ownRouter(ctx context.Context, service string) (string, error) {
	raw, e := o.runner.Run(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=stackkit-basement-core", "--filter", "label=com.docker.compose.service=" + service, "--format", "{{.ID}}", "--no-trunc"}, o.workspaceRoot)
	if e != nil {
		return "", errors.New("core router observation unavailable")
	}
	id := string(raw)
	for len(id) > 0 && (id[len(id)-1] == '\n' || id[len(id)-1] == '\r') {
		id = id[:len(id)-1]
	}
	if id != "" && !ownContainerID.MatchString(id) {
		return "", errors.New("exact running core router required")
	}
	return id, nil
}
func (o *osStandaloneComposeWorkloadOperations) connectOwnRouter(ctx context.Context, name string) error {
	n, e := o.inspectOwnNetwork(ctx, name)
	if e != nil {
		return e
	}
	for _, service := range []string{"router", "router-remote"} {
		id, e := o.ownRouter(ctx, service)
		if e != nil {
			return e
		}
		if id == "" {
			return errors.New("exact core ingress router required")
		}
		if _, ok := n.Containers[id]; ok {
			continue
		}
		if _, e = o.runner.Run(ctx, []string{"network", "connect", name, id}, o.workspaceRoot); e != nil {
			return errors.New("attach ingress router to own application private network")
		}
	}

	return nil
}
func (o *osStandaloneComposeWorkloadOperations) completeOwnApplication(ctx context.Context, p standaloneComposeProject) error {
	n, e := o.inspectOwnNetwork(ctx, ownApplicationNetworkName(p.bundle.WorkloadRef, p.bundle.NodeRef))
	if e != nil {
		return e
	}
	if n.Internal == p.bundle.OwnApplicationFirewalled {
		return errors.New("own application observed network does not match its reviewed firewall grant")
	}
	raw, e := o.runner.Run(ctx, standaloneComposeArgs(p, "ps"), p.directory)
	if e != nil {
		return e
	}
	statuses, e := parseStandaloneComposeStatuses(raw)
	if e != nil {
		return e
	}
	ids, e := standaloneComposeContainerIdentities(p.bundle.Components, statuses, true)
	if e != nil {
		return e
	}
	// Use the admitted container's private address, never a published host port.
	// A project keeps every service on this network, so the readiness probe
	// addresses the routed service only.
	entryID, admitted := ids[p.bundle.EntryComponent]
	if !admitted {
		return errors.New("own application entry container identity unavailable")
	}
	address := ""
	if endpoint, ok := n.Containers[entryID]; ok {
		address = endpoint.IPv4Address
		if p.bundle.OwnApplicationFirewalled && !strings.EqualFold(endpoint.MacAddress, ownappfirewall.MAC(n.Name)) {
			return errors.New("own application endpoint MAC differs from reviewed firewall identity")
		}
	}
	ip, _, e := net.ParseCIDR(address)
	if e != nil || ip == nil {
		return errors.New("own application private readiness address unavailable")
	}
	url := "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(p.entry.HealthPort)) + "/"
	for {
		status, probeErr := o.prober.Probe(ctx, url)
		if probeErr == nil && status >= http.StatusOK && status < 400 {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("own application HTTP readiness did not complete; route remains closed")
		case <-time.After(time.Second):
		}
	}
	if p.firewall != nil {
		if err := p.firewall.VerifyWatcher(ctx); err != nil {
			return err
		}
	}
	return o.connectOwnRouter(ctx, n.Name)
}

// Core recreation loses manual Docker network attachments. Reattach only the
// closed, labelled internal bridges, through the same fixed native runner.
func reconnectOwnApplicationNetworks(ctx context.Context, workspace string) error {
	if _, e := os.Lstat(filepath.Join(workspace, ".stackkit/own-applications/state.json")); os.IsNotExist(e) {
		return nil
	}
	runtime, e := NewOwnApplicationRuntime(workspace)
	if e != nil {
		return e
	}
	raw, e := runtime.ops.runner.Run(ctx, []string{"network", "ls", "--format", "{{.Name}}", "--filter=label=" + ownNetworkLabel + "=v1"}, workspace)
	if e != nil {
		return e
	}
	for _, name := range regexp.MustCompile(`\s+`).Split(string(raw), -1) {
		if name == "" {
			continue
		}
		if !ownNetworkName.MatchString(name) {
			return fmt.Errorf("foreign own-application network")
		}
		project := strings.TrimSuffix(name, "_private")
		running, err := runtime.ops.runner.Run(ctx, []string{"ps", "--filter", "label=com.docker.compose.project=" + project, "--format", "{{.ID}}", "--no-trunc"}, workspace)
		if err != nil {
			return err
		}
		ids := strings.Fields(string(running))
		if len(ids) == 0 {
			continue
		}
		admitted := false
		for _, id := range ids {
			if ownContainerID.MatchString(id) {
				admitted = true
				break
			}
		}
		if !admitted {
			return errors.New("exact own application container required")
		}
		if e = runtime.ops.connectOwnRouter(ctx, name); e != nil {
			return e
		}
	}
	return nil
}

// Network mutations are admitted separately from read queries: the native
// runner re-observes the exact core router IDs immediately before execution.
func ownRouterNetworkMutation(a []string) bool {
	return len(a) == 4 && a[0] == "network" && (a[1] == "connect" || a[1] == "disconnect") && ownNetworkName.MatchString(a[2]) && ownContainerID.MatchString(a[3])
}
func (r osStandaloneComposeProcessRunner) admitOwnRouterNetworkMutation(ctx context.Context, a []string, directory string) error {
	if !ownRouterNetworkMutation(a) {
		return errors.New("invalid own router network mutation")
	}
	for _, service := range []string{"router", "router-remote"} {
		raw, e := r.Run(ctx, []string{"ps", "--all", "--filter", "label=com.docker.compose.project=stackkit-basement-core", "--filter", "label=com.docker.compose.service=" + service, "--format", "{{.ID}}", "--no-trunc"}, directory)
		if e != nil {
			return e
		}
		if id := strings.TrimSpace(string(raw)); ownContainerID.MatchString(id) && id == a[3] {
			return nil
		}
	}
	return errors.New("network connect/disconnect permits only exact core router containers")
}
func (o *osStandaloneComposeWorkloadOperations) detachOwnRouters(ctx context.Context, name string, remove bool) error {
	// Listing proves absence without treating a failed inspect as absence.
	raw, e := o.runner.Run(ctx, []string{"network", "ls", "--format", "{{.Name}}", "--filter=label=" + ownNetworkLabel + "=v1"}, o.workspaceRoot)
	if e != nil {
		return e
	}
	found := false
	for _, candidate := range strings.Fields(string(raw)) {
		if candidate == name {
			found = true
		}
	}
	if !found {
		return nil
	}
	n, e := o.inspectOwnNetwork(ctx, name)
	if e != nil {
		return e
	}
	for _, service := range []string{"router", "router-remote"} {
		id, e := o.ownRouter(ctx, service)
		if e != nil {
			return e
		}
		if _, attached := n.Containers[id]; !attached || id == "" {
			continue
		}
		if _, e = o.runner.Run(ctx, []string{"network", "disconnect", name, id}, o.workspaceRoot); e != nil {
			return e
		}
	}
	n, e = o.inspectOwnNetwork(ctx, name)
	if e != nil {
		return e
	}
	for _, service := range []string{"router", "router-remote"} {
		id, e := o.ownRouter(ctx, service)
		if e != nil {
			return e
		}
		if _, attached := n.Containers[id]; attached && id != "" {
			return errors.New("own router detachment unverified")
		}
	}
	if !remove {
		return nil
	}
	if len(n.Containers) != 0 {
		return errors.New("own network still has container endpoints")
	}
	if _, e = o.runner.Run(ctx, []string{"network", "rm", name}, o.workspaceRoot); e != nil {
		return e
	}
	raw, e = o.runner.Run(ctx, []string{"network", "ls", "--format", "{{.Name}}", "--filter=label=" + ownNetworkLabel + "=v1"}, o.workspaceRoot)
	if e != nil {
		return e
	}
	for _, candidate := range strings.Fields(string(raw)) {
		if candidate == name {
			return errors.New("own network removal unverified")
		}
	}
	return nil
}
