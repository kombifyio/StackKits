package hostsecurity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/dataprofilev1"
)

// ContainerBridge is an exact Docker network and its stable Linux bridge.
// The identity depends on stack identity, never on a changing plan hash.
type ContainerBridge struct {
	Network   string `json:"network"`
	Interface string `json:"interface"`
}

type ContainerDestinationPolicy struct {
	StackID string            `json:"stackId"`
	Bridges []ContainerBridge `json:"bridges"`
}

var containerNetworkName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

const FullLocalContainerOwnerLabel = "io.stackkit.full-local"

func StableContainerBridge(stackID, network string) string {
	hash := sha256.Sum256([]byte(stackID + "/" + network))
	return "sk" + hex.EncodeToString(hash[:6])
}

func (p ContainerDestinationPolicy) Validate() error {
	if !containerNetworkName.MatchString(p.StackID) || len(p.Bridges) == 0 {
		return errors.New("Full Local requires an exact stack identity and owned bridges")
	}
	for _, bridge := range p.Bridges {
		if !containerNetworkName.MatchString(bridge.Network) || bridge.Interface != StableContainerBridge(p.StackID, bridge.Network) {
			return errors.New("Full Local bridge differs from its stable stack/network identity")
		}
	}
	return nil
}

func CoreContainerNetworks() []string {
	return []string{"stackkit-basement-core", "stackkit-basement-control", "stackkit-basement-backup"}
}

func CoreContainerDestinations(stackID string) ContainerDestinationPolicy {
	policy := ContainerDestinationPolicy{StackID: stackID}
	for _, network := range CoreContainerNetworks() {
		policy.Bridges = append(policy.Bridges, ContainerBridge{Network: network, Interface: StableContainerBridge(stackID, network)})
	}
	return policy
}

func (p ContainerDestinationPolicy) renderChain() string {
	hash := sha256.Sum256([]byte(p.StackID))
	chain := "stackkits_local_" + hex.EncodeToString(hash[:6])
	var names []string
	for _, bridge := range p.Bridges {
		names = append(names, fmt.Sprintf("%q", bridge.Interface))
	}
	sort.Strings(names)
	names = slices.Compact(names)
	interfaces := strings.Join(names, ", ")
	if len(names) > 1 {
		interfaces = "{ " + interfaces + " }"
	}
	var chains []string
	prefixes, excluded := dataprofile.LocalDestinationPolicy()
	for _, hook := range []string{"input", "forward"} {
		lines := []string{"\tchain " + chain + "_" + hook + " {", "\t\ttype filter hook " + hook + " priority filter - 10; policy accept;"}
		for _, direction := range []struct{ iface, address string }{{"iifname", "daddr"}, {"oifname", "saddr"}} {
			if hook == "input" && direction.iface == "oifname" {
				continue
			}
			selector := "\t\t" + direction.iface + " " + interfaces
			// Neighbor discovery is link control, not an application egress
			// exception. Keep fresh IPv6 LAN neighbors usable after recreation.
			if direction.iface == "iifname" {
				lines = append(lines, selector+" ip6 daddr ff02::1:ff00:0/104 ip6 hoplimit 255 icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert } counter accept")
			}
			for _, address := range excluded {
				family := "ip"
				if address.Is6() {
					family = "ip6"
				}
				lines = append(lines, selector+" "+family+" "+direction.address+" "+address.String()+" counter drop")
			}
			for _, prefix := range prefixes {
				family := "ip"
				if prefix.Addr().Is6() {
					family = "ip6"
				}
				value := prefix.String()
				if prefix.IsSingleIP() {
					value = prefix.Addr().String()
				}
				lines = append(lines, selector+" "+family+" "+direction.address+" "+value+" counter accept")
			}
			lines = append(lines, selector+" counter drop")
		}
		chains = append(chains, strings.Join(append(lines, "\t}"), "\n"))
	}
	return strings.Join(chains, "\n")
}

// EnforceContainerDestinations extends the existing Home firewall transaction.
// It touches only the owned table and exact cohort interfaces, never OUTPUT,
// daemon DNS, routes, sysctls, foreign bridges or host update timers.
func (e Engine) EnforceContainerDestinations(ctx context.Context, scope ContainerDestinationPolicy) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if e.Host == nil {
		e.Host = LocalHost{}
	}
	if e.Host.Geteuid() != 0 {
		return errors.New("Full Local container admission requires root and existing nftables")
	}
	if _, err := e.Host.LookPath("nft"); err != nil {
		return fmt.Errorf("offline prerequisite nftables is missing: %w", err)
	}
	owner, ok := e.Host.(interface {
		ValidateContainerBridge(context.Context, string, ContainerBridge) error
	})
	if !ok {
		return errors.New("Full Local requires local container bridge custody admission")
	}
	for _, bridge := range scope.Bridges {
		if err := owner.ValidateContainerBridge(ctx, scope.StackID, bridge); err != nil {
			return err
		}
	}
	unlock, err := e.lockFirewall()
	if err != nil {
		return err
	}
	defer unlock()
	policy := FirewallPolicy{ContainerOnly: true}
	raw, err := e.Host.ReadFile(HomeFirewallPolicyPath)
	if err == nil {
		var record recordedPolicy
		if json.Unmarshal(raw, &record) != nil || record.SchemaVersion != recordedPolicySchema {
			return errors.New("existing Home firewall record is invalid")
		}
		policy = record.Policy
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("existing Home firewall record: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	r := &repairer{engine: e, apply: true}
	query, err := e.run(ctx, "nft", "list", "table", "inet", HomeFirewallTable)
	if err != nil {
		return err
	}
	if query.ExitCode != 0 && !strings.Contains(query.Stderr, "No such file or directory") {
		return fmt.Errorf("cannot establish existing Home firewall ownership: %s", boundedText(query.Stderr))
	}
	prior, exists := query.Stdout, query.ExitCode == 0
	if exists && (len(raw) == 0 || normalizeRuleset(prior) != normalizeRuleset(policy.Render())) {
		return errors.New("Home firewall differs from its recorded ownership; refusing to replace it")
	}
	found := false
	for index := range policy.Containers {
		if policy.Containers[index].StackID != scope.StackID {
			continue
		}
		scope.Bridges = append(scope.Bridges, policy.Containers[index].Bridges...)
		sort.Slice(scope.Bridges, func(i, j int) bool { return scope.Bridges[i].Network < scope.Bridges[j].Network })
		scope.Bridges = slices.Compact(scope.Bridges)
		policy.Containers[index] = scope
		found = true
	}
	if !found {
		policy.Containers = append(policy.Containers, scope)
	}
	sort.Slice(policy.Containers, func(i, j int) bool { return policy.Containers[i].StackID < policy.Containers[j].StackID })
	if err := e.Host.MkdirAll(StateDir, 0700); err != nil {
		return err
	}
	if err := r.installHomeFirewallPolicy(ctx, policy); err != nil {
		return err
	}
	return nil
}

// A future bridge can be filtered before it exists. An existing interface must
// be the exact locally owned Docker bridge, never an adopted same-name link.
func (LocalHost) ValidateContainerBridge(ctx context.Context, stackID string, bridge ContainerBridge) error {
	if _, err := os.Stat(filepath.Join("/sys/class/net", bridge.Interface)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join("/sys/class/net", bridge.Interface, "bridge")); err != nil {
		return errors.New("Full Local stable interface exists but is not a Linux bridge")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "network", "inspect", bridge.Network)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output := &boundedBuffer{remaining: maxCommandOutput}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		return fmt.Errorf("Full Local bridge has no exact Docker custody: %w", err)
	}
	if output.exceeded {
		return errors.New("Full Local bridge custody observation exceeds bound")
	}
	var networks []struct {
		Name, Driver    string
		Labels, Options map[string]string
	}
	if err := json.Unmarshal(output.Bytes(), &networks); err != nil {
		return err
	}
	if len(networks) != 1 || networks[0].Name != bridge.Network || networks[0].Driver != "bridge" || networks[0].Labels[FullLocalContainerOwnerLabel] != stackID || networks[0].Options["com.docker.network.bridge.name"] != bridge.Interface {
		return errors.New("Full Local stable interface is not owned by the exact Docker cohort")
	}
	return nil
}

// The local Home owner serializes repair and cohort admission with the same
// existing held-root lock primitive used by artifact transactions.
func (LocalHost) LockFirewall() (func(), error) {
	if err := os.MkdirAll(StateDir, 0700); err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(StateDir)
	if err != nil {
		return nil, err
	}
	transaction, err := root.BeginTransaction()
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	lock, err := transaction.TryAcquireOutputLock("home-firewall")
	if err != nil {
		_ = transaction.Close()
		_ = root.Close()
		return nil, err
	}
	return func() { _ = lock.Release(); _ = transaction.Close(); _ = root.Close() }, nil
}

func (e Engine) lockFirewall() (func(), error) {
	if host, ok := e.Host.(interface{ LockFirewall() (func(), error) }); ok {
		return host.LockFirewall()
	}
	// Alternate Host implementations retain their existing serialization.
	return func() {}, nil
}
