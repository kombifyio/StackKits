package hostsecurity

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultFreshness is how long one observation may be relied on.
	DefaultFreshness = 15 * time.Minute
	MinFreshness     = time.Minute
	MaxFreshness     = 24 * time.Hour

	// StateDir holds what this package records on the host so a later
	// verification can tell drift from the last applied policy.
	StateDir               = "/etc/stackkit/host-security"
	HomeFirewallPolicyPath = StateDir + "/home-firewall-policy.json"
	HomeFirewallRuleset    = StateDir + "/home-firewall.nft"

	evidenceFileName = "host-security-evidence.json"
)

// Listener is one declared service listener.
type Listener struct {
	Transport string `json:"transport"`
	Port      int    `json:"port"`
}

// Options select what a verification judges the host against.
type Options struct {
	Kit      string
	Mode     Mode
	SiteKind SiteKind
	NodeRef  string
	PlanHash string
	// Freshness is the budget after which the evidence expires. Zero selects
	// DefaultFreshness.
	Freshness time.Duration
	// DeclaredListeners is the declared-service authority. nil means no
	// authority is available, which makes exposed listeners unknown rather
	// than compliant.
	DeclaredListeners []Listener
	DeclaredAuthority string
	// ManagementSources are owner-configured networks that always reach sshd.
	ManagementSources []netip.Prefix
	// ExceptionsPath is the owner-controlled exceptions file; empty means none.
	ExceptionsPath string
	// WorkspaceRoot locates custody material (certificates). Empty skips it.
	WorkspaceRoot string
}

// Engine observes and repairs one node through Host.
type Engine struct {
	Host Host
}

// EvidencePath is where local evidence for a workspace is written.
func EvidencePath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, ".stackkit", evidenceFileName)
}

func clampFreshness(value time.Duration) time.Duration {
	switch {
	case value <= 0:
		return DefaultFreshness
	case value < MinFreshness:
		return MinFreshness
	case value > MaxFreshness:
		return MaxFreshness
	}
	return value
}

// Verify observes every control and returns the judged evidence. It changes
// nothing on the host. A control it cannot observe is unknown with a reason,
// never a failure and never compliant.
func (e Engine) Verify(ctx context.Context, options Options) Evidence {
	now := e.Host.Now().UTC()
	freshness := clampFreshness(options.Freshness)
	if options.Mode == "" {
		options.Mode = ModeStandard
	}
	if options.SiteKind == "" {
		options.SiteKind = SiteUnknown
	}
	evidence := Evidence{
		SchemaVersion: EvidenceSchemaVersion, Kit: options.Kit, Mode: options.Mode, SiteKind: options.SiteKind,
		BaselineVersion: BaselineVersion, NodeRef: options.NodeRef, PlanHash: options.PlanHash,
		ObservedAt: now, ExpiresAt: now.Add(freshness), FreshnessSecs: int(freshness / time.Second),
	}
	run := observation{engine: e, options: options}
	controls := []Control{run.firewall(ctx)}
	controls = append(controls, run.ssh(ctx)...)
	controls = append(controls,
		run.bruteForce(ctx), run.unattendedUpgrades(ctx), run.pendingSecurity(ctx),
		run.rebootRequired(), run.sysctl(),
	)
	listeners, published := run.exposure(ctx)
	controls = append(controls, listeners, published)
	if certificate, ok := run.certificates(ctx); ok {
		controls = append(controls, certificate)
	}
	evidence.Notices = append(evidence.Notices, run.notices...)

	exceptions, exceptionNotices := LoadExceptions(e.Host, options.ExceptionsPath)
	evidence.Notices = append(evidence.Notices, exceptionNotices...)
	controls, evidence.Exceptions = applyExceptions(controls, exceptions, now)
	evidence.Controls = controls
	evidence.Overall = overall(controls)
	return evidence
}

// observation carries the per-run state the control observers share.
type observation struct {
	engine  Engine
	options Options
	notices []string
	// cached observations several controls read
	sshd        *sshdObservation
	sshdFailure string
	firewallRec *recordedPolicy
}

func (o *observation) now() time.Time { return o.engine.Host.Now().UTC() }

func (o *observation) note(format string, args ...any) {
	o.notices = append(o.notices, fmt.Sprintf(format, args...))
}

// control starts a control record for the given ID.
func (o *observation) control(id, expected string, remediation Remediation) Control {
	return Control{ID: id, State: StateUnknown, Expected: expected, Remediation: remediation}
}

func (o *observation) finish(c Control, state State, observed, reason string) Control {
	c.State, c.Observed, c.Reason, c.ObservedAt = state, observed, reason, o.now()
	return c
}

func (o *observation) unknown(c Control, reason string) Control {
	return o.finish(c, StateUnknown, "not observed", reason)
}

var errToolMissing = errors.New("tool is not installed")

// toolPath finds an executable even when an unprivileged PATH omits sbin.
func (e Engine) toolPath(name string) (string, bool) {
	if path, err := e.Host.LookPath(name); err == nil {
		return path, true
	}
	for _, directory := range []string{"/usr/sbin", "/sbin", "/usr/local/sbin", "/usr/bin", "/bin"} {
		candidate := directory + "/" + name
		if info, err := e.Host.Stat(candidate); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return candidate, true
		}
	}
	return "", false
}

// run executes a tool resolved by name; a missing tool is errToolMissing.
func (e Engine) run(ctx context.Context, name string, args ...string) (Output, error) {
	path, ok := e.toolPath(name)
	if !ok {
		return Output{}, fmt.Errorf("%s: %w", name, errToolMissing)
	}
	return e.Host.Run(ctx, path, args...)
}

func (e Engine) isRoot() bool { return e.Host.Geteuid() == 0 }

func rootHint(action string) string {
	return "requires root to " + action + "; run with sudo"
}

func joinInts(ports []int) string {
	parts := make([]string, len(ports))
	for i, port := range ports {
		parts[i] = fmt.Sprint(port)
	}
	return strings.Join(parts, ",")
}
