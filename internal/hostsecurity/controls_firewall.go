package hostsecurity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// recordedPolicy is what a repair wrote down about the firewall it installed.
// Verification re-renders it and compares with the live table, so a rule added
// or removed since the apply is drift, not an unnoticed change.
type recordedPolicy struct {
	SchemaVersion string         `json:"schema_version"`
	Policy        FirewallPolicy `json:"policy"`
}

const recordedPolicySchema = "stackkit.host-firewall-policy/v1"

type nftChain struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Hook   string `json:"hook"`
	Policy string `json:"policy"`
}

type nftRule struct {
	Family string                       `json:"family"`
	Table  string                       `json:"table"`
	Chain  string                       `json:"chain"`
	Expr   []map[string]json.RawMessage `json:"expr"`
}

type nftListing struct {
	Chains []nftChain
	Rules  []nftRule
}

func parseNFTListing(raw string) (nftListing, error) {
	var document struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nftListing{}, fmt.Errorf("nft reported an unreadable ruleset: %w", err)
	}
	var listing nftListing
	for _, item := range document.Nftables {
		if chain, ok := item["chain"]; ok {
			var parsed nftChain
			if err := json.Unmarshal(chain, &parsed); err == nil {
				listing.Chains = append(listing.Chains, parsed)
			}
		}
		if rule, ok := item["rule"]; ok {
			var parsed nftRule
			if err := json.Unmarshal(rule, &parsed); err == nil {
				listing.Rules = append(listing.Rules, parsed)
			}
		}
	}
	return listing, nil
}

// unconditionalAccept reports whether a chain has a rule that accepts
// everything it sees; such a chain filters nothing whatever its policy says.
func (l nftListing) unconditionalAccept(chain nftChain) bool {
	for _, rule := range l.Rules {
		if rule.Family != chain.Family || rule.Table != chain.Table || rule.Chain != chain.Name {
			continue
		}
		if len(rule.Expr) == 1 {
			if _, accepts := rule.Expr[0]["accept"]; accepts {
				return true
			}
		}
	}
	return false
}

func (o *observation) firewallRemediation() Remediation {
	switch o.options.SiteKind {
	case SiteHome:
		return Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlFirewall}
	case SiteCloud:
		return Remediation{Capability: RemediationManual, Action: "stackkit apply (the Cloud host-security owner restores the firewall and keeps the execution account)"}
	}
	return Remediation{Capability: RemediationManual, Action: "rerun with --site-kind home or cloud so the matching firewall owner can be named"}
}

func (o *observation) firewallExpected() string {
	switch o.options.SiteKind {
	case SiteHome:
		return "inbound default-drop in nftables table " + HomeFirewallTable + " matching the last applied policy"
	case SiteCloud:
		return "inbound default-drop in nftables table " + CloudFirewallTable
	}
	return "inbound default-drop (a StackKits nftables table, ufw deny incoming, or an equivalent policy-drop input chain)"
}

func (o *observation) recordedFirewall() *recordedPolicy {
	if o.firewallRec != nil {
		return o.firewallRec
	}
	raw, err := o.engine.Host.ReadFile(HomeFirewallPolicyPath)
	if err != nil {
		return nil
	}
	var record recordedPolicy
	if json.Unmarshal(raw, &record) != nil || record.SchemaVersion != recordedPolicySchema || record.Policy.Validate() != nil {
		return nil
	}
	o.firewallRec = &record
	return o.firewallRec
}

//nolint:gocyclo // One linear decision over the ways an inbound policy can be present, absent or hollow.
func (o *observation) firewall(ctx context.Context) Control {
	c := o.control(ControlFirewall, o.firewallExpected(), o.firewallRemediation())
	if !o.engine.isRoot() {
		return o.unknown(c, rootHint("list the firewall ruleset"))
	}
	output, err := o.engine.run(ctx, "nft", "-j", "list", "ruleset")
	if errors.Is(err, errToolMissing) {
		return o.firewallWithoutNFT(ctx, c)
	}
	if err != nil {
		return o.unknown(c, "nft could not run: "+err.Error())
	}
	if output.ExitCode != 0 {
		return o.unknown(c, "nft could not list the ruleset: "+boundedText(output.Stderr))
	}
	listing, err := parseNFTListing(output.Stdout)
	if err != nil {
		return o.unknown(c, err.Error())
	}
	var input []nftChain
	for _, chain := range listing.Chains {
		if chain.Hook == "input" {
			input = append(input, chain)
		}
	}
	owned := func(name string) *nftChain {
		for i := range input {
			if input[i].Table == name {
				return &input[i]
			}
		}
		return nil
	}
	want := map[SiteKind][]string{
		SiteHome: {HomeFirewallTable, CloudFirewallTable}, SiteCloud: {CloudFirewallTable, HomeFirewallTable},
		SiteUnknown: {HomeFirewallTable, CloudFirewallTable},
	}[o.options.SiteKind]
	for _, name := range want {
		chain := owned(name)
		if chain == nil {
			continue
		}
		if chain.Policy != "drop" {
			return o.finish(c, StateDrifted, fmt.Sprintf("table %s input policy is %q", name, chain.Policy), "the StackKits table no longer drops unmatched inbound traffic")
		}
		if listing.unconditionalAccept(*chain) {
			return o.finish(c, StateDrifted, fmt.Sprintf("table %s accepts all inbound traffic", name), "a catch-all accept rule makes the default-drop policy inert")
		}
		if name == HomeFirewallTable {
			if state, observed, reason := o.compareHomeRuleset(ctx); state != StateCompliant {
				return o.finish(c, state, observed, reason)
			}
		}
		return o.finish(c, StateCompliant, fmt.Sprintf("table %s input policy drop", name), "")
	}
	if o.options.SiteKind == SiteHome || o.options.SiteKind == SiteCloud {
		// The expected owner is absent. An equivalent default-deny posture from
		// another firewall still protects the host, and is reported as such.
		if observed, found := o.equivalentFirewall(ctx, input, listing); found {
			return o.finish(c, StateCompliant, observed, "equivalent default-deny firewall; the StackKits table is not installed")
		}
		return o.finish(c, StateDrifted, "no inbound default-drop firewall observed", "the StackKits firewall table is absent and no equivalent policy is active")
	}
	if observed, found := o.equivalentFirewall(ctx, input, listing); found {
		return o.finish(c, StateCompliant, observed, "equivalent default-deny firewall")
	}
	return o.finish(c, StateDrifted, "no inbound default-drop firewall observed", "no input chain drops unmatched traffic")
}

// equivalentFirewall accepts ufw with deny-incoming, or any policy-drop input
// chain that is not hollowed out by a catch-all accept.
func (o *observation) equivalentFirewall(ctx context.Context, input []nftChain, listing nftListing) (string, bool) {
	for _, chain := range input {
		if chain.Policy == "drop" && !listing.unconditionalAccept(chain) {
			return fmt.Sprintf("table %s input policy drop", chain.Table), true
		}
	}
	output, err := o.engine.run(ctx, "ufw", "status", "verbose")
	if err == nil && output.ExitCode == 0 && strings.Contains(output.Stdout, "Status: active") &&
		strings.Contains(strings.ToLower(output.Stdout), "deny (incoming)") {
		return "ufw active with default deny (incoming)", true
	}
	return "", false
}

func (o *observation) firewallWithoutNFT(ctx context.Context, c Control) Control {
	output, err := o.engine.run(ctx, "ufw", "status", "verbose")
	if err == nil && output.ExitCode == 0 && strings.Contains(output.Stdout, "Status: active") &&
		strings.Contains(strings.ToLower(output.Stdout), "deny (incoming)") {
		return o.finish(c, StateCompliant, "ufw active with default deny (incoming)", "equivalent default-deny firewall")
	}
	return o.finish(c, StateDrifted, "no firewall tooling or policy observed", "nft is not installed and ufw does not deny incoming traffic")
}

// compareHomeRuleset compares the live home table with the recorded policy.
func (o *observation) compareHomeRuleset(ctx context.Context) (State, string, string) {
	record := o.recordedFirewall()
	if record == nil {
		return StateCompliant, "table " + HomeFirewallTable + " input policy drop (no applied-policy record; structure checked only)", ""
	}
	output, err := o.engine.run(ctx, "nft", "list", "table", "inet", HomeFirewallTable)
	if err != nil || output.ExitCode != 0 {
		return StateUnknown, "not observed", "the live table could not be listed to compare with the applied policy"
	}
	if normalizeRuleset(output.Stdout) != normalizeRuleset(record.Policy.Render()) {
		return StateDrifted, "table " + HomeFirewallTable + " differs from the applied policy", "a rule was added, removed or changed since the last repair"
	}
	return StateCompliant, "", ""
}

func normalizeRuleset(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimRight(line, " \t"); strings.TrimSpace(trimmed) != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

func boundedText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}
