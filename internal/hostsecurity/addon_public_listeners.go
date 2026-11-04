package hostsecurity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// SetAddOnPublicListeners opens (or, with empty lists, closes) the public
// ports of an add-on the Owner switched on in the StackKits Home firewall,
// such as the VPN add-on's WireGuard endpoint and peer service. Without a
// StackKits Home firewall record there is nothing to open and it reports
// false; a live table that differs from its record is refused, never
// replaced. The policy is recorded, so baseline repairs keep the ports.
func (e Engine) SetAddOnPublicListeners(ctx context.Context, tcp, udp []int) (bool, error) {
	if e.Host.Geteuid() != 0 {
		return false, errors.New("opening an add-on's public ports requires root")
	}
	unlock, err := e.lockFirewall()
	if err != nil {
		return false, err
	}
	defer unlock()
	raw, err := e.Host.ReadFile(HomeFirewallPolicyPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var record recordedPolicy
	if json.Unmarshal(raw, &record) != nil || record.SchemaVersion != recordedPolicySchema {
		return false, errors.New("existing Home firewall record is invalid")
	}
	policy := record.Policy
	if err := policy.Validate(); err != nil {
		return false, fmt.Errorf("existing Home firewall record: %w", err)
	}
	if policy.ContainerOnly {
		return false, nil // host input is not StackKits-managed here
	}
	query, err := e.run(ctx, "nft", "list", "table", "inet", HomeFirewallTable)
	if err != nil {
		return false, err
	}
	if query.ExitCode != 0 && !strings.Contains(query.Stderr, "No such file or directory") {
		return false, fmt.Errorf("cannot establish existing Home firewall ownership: %s", boundedText(query.Stderr))
	}
	if query.ExitCode == 0 && normalizeRuleset(query.Stdout) != normalizeRuleset(policy.Render()) {
		return false, errors.New("Home firewall differs from its recorded ownership; refusing to replace it (run stackkit host security repair)")
	}
	tcp, udp = sortedUniquePorts(tcp), sortedUniquePorts(udp)
	if slices.Equal(tcp, policy.normalized().AddOnPublicTCP) && slices.Equal(udp, policy.normalized().AddOnPublicUDP) && query.ExitCode == 0 {
		return true, nil
	}
	policy.AddOnPublicTCP, policy.AddOnPublicUDP = tcp, udp
	if err := policy.Validate(); err != nil {
		return false, err
	}
	r := &repairer{engine: e, apply: true}
	if err := r.installHomeFirewallPolicy(ctx, policy); err != nil {
		return false, err
	}
	return true, nil
}
