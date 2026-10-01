package hostsecurity

import (
	"context"
	"net/netip"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// ManagementPath is everything a repair must not cut: the ports sshd listens
// on, the SSH sessions in use right now, the owner's configured management
// networks, and the accounts that hold a login key.
type ManagementPath struct {
	SSHPorts          []int    `json:"ssh_ports"`
	SessionPeers      []string `json:"session_peers,omitempty"`
	ManagementSources []string `json:"management_sources,omitempty"`
	SessionUser       string   `json:"session_user,omitempty"`
	KeyAccounts       []string `json:"key_accounts,omitempty"`
	InSSHSession      bool     `json:"in_ssh_session"`
	// SSHInstalled is false when the host has no sshd, in which case there is
	// no ssh path to preserve.
	SSHInstalled bool `json:"ssh_installed"`

	peers    []netip.Addr // peers that need an explicit firewall rule
	allPeers []netip.Addr // every peer of a session in use
	sources  []netip.Prefix
}

// detectManagementPath observes the current management path. settings may be
// nil when sshd cannot be read; the caller decides whether that blocks.
func (o *observation) detectManagementPath(ctx context.Context, settings *sshdObservation) ManagementPath {
	path := ManagementPath{}
	if _, installed := o.engine.toolPath(sshdBinary); installed {
		path.SSHInstalled = true
	}
	if settings != nil {
		path.SSHPorts = settings.ports()
	}

	peers := map[netip.Addr]bool{}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT"} {
		if fields := strings.Fields(o.engine.Host.Getenv(key)); len(fields) > 0 {
			if addr, err := netip.ParseAddr(fields[0]); err == nil {
				peers[addr.Unmap()] = true
				path.InSSHSession = true
			}
		}
	}
	// sudo resets the environment, so the established-socket listing is the
	// source that still sees a session the repair was started from.
	if output, err := o.engine.run(ctx, "ss", "-H", "-t", "-n", "state", "established"); err == nil && output.ExitCode == 0 {
		for _, peer := range sshPeers(output.Stdout, path.SSHPorts) {
			peers[peer] = true
		}
	}
	for peer := range peers {
		path.allPeers = append(path.allPeers, peer)
		if explicitPeer(peer) {
			path.peers = append(path.peers, peer)
		}
	}
	sort.Slice(path.allPeers, func(i, j int) bool { return path.allPeers[i].Less(path.allPeers[j]) })
	sort.Slice(path.peers, func(i, j int) bool { return path.peers[i].Less(path.peers[j]) })
	for _, peer := range path.peers {
		path.SessionPeers = append(path.SessionPeers, peer.String())
	}

	sources := map[netip.Prefix]bool{}
	for _, source := range o.options.ManagementSources {
		sources[source.Masked()] = true
	}
	// Sources configured by an earlier repair stay configured: dropping one
	// because a flag was not repeated would cut the path it was added for.
	if record := o.recordedFirewall(); record != nil {
		for _, source := range record.Policy.ManagementSources {
			sources[source.Masked()] = true
		}
	}
	for source := range sources {
		path.sources = append(path.sources, source)
	}
	sort.Slice(path.sources, func(i, j int) bool { return comparePrefix(path.sources[i], path.sources[j]) < 0 })
	for _, source := range path.sources {
		path.ManagementSources = append(path.ManagementSources, source.String())
	}

	path.SessionUser = o.sessionUser(path.InSSHSession)
	path.KeyAccounts = o.accountsWithLoginKeys(settings)
	return path
}

// inSSHSession reports whether this process was started from an ssh session.
func (o *observation) inSSHSession() bool {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT"} {
		if fields := strings.Fields(o.engine.Host.Getenv(key)); len(fields) > 0 {
			if _, err := netip.ParseAddr(fields[0]); err == nil {
				return true
			}
		}
	}
	return false
}

// sessionUser is the account the management session logs in as: the sudo
// caller, or the ssh login itself.
func (o *observation) sessionUser(inSSHSession bool) string {
	user := strings.TrimSpace(o.engine.Host.Getenv("SUDO_USER"))
	if user == "" && inSSHSession {
		user = strings.TrimSpace(o.engine.Host.Getenv("USER"))
	}
	return user
}

// loginKeyProblem reports whether disabling password logins would lock the
// owner out: no account holds an authorized key, or the account in use holds
// none of its own.
func loginKeyProblem(keyAccounts []string, sessionUser string) (blocked bool, detail string) {
	if len(keyAccounts) == 0 {
		return true, "no account has an authorized SSH key, so disabling password logins would lock every ssh login out"
	}
	if sessionUser != "" && !slices.Contains(keyAccounts, sessionUser) {
		return true, sessionUser + " is logged in without an authorized SSH key of their own, so disabling password logins would cut this session's next login"
	}
	return false, ""
}

// keyGuidance names the account that needs a key and the command that finishes
// the repair afterwards.
func keyGuidance(sessionUser string) string {
	account := "the execution or owner account that logs in over ssh"
	if sessionUser != "" {
		account = sessionUser
	}
	return "add an SSH key for " + account + " (~/.ssh/authorized_keys), then run `stackkit host security repair --control " + ControlSSHPassword + " --apply`"
}

// sshPeers reads `ss -H -t -n state established` and returns the remote
// addresses of connections to one of the ssh ports.
func sshPeers(stdout string, ports []int) []netip.Addr {
	var peers []netip.Addr
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		_, localPort, ok := splitHostPort(fields[len(fields)-2])
		if !ok || !containsPort(ports, localPort) {
			continue
		}
		address, _, ok := splitHostPort(fields[len(fields)-1])
		if !ok {
			continue
		}
		if parsed, err := netip.ParseAddr(address); err == nil {
			peers = append(peers, parsed.Unmap())
		}
	}
	return peers
}

// explicitPeer reports whether a peer needs its own firewall rule. Loopback,
// LAN and link-local peers are already admitted by the baseline, and naming
// them would only make every repair differ from the last.
func explicitPeer(peer netip.Addr) bool {
	if peer.IsLoopback() || peer.IsUnspecified() {
		return false
	}
	return !matchAny(privateSourcesV4, peer) && !matchAny(privateSourcesV6, peer)
}

// accountsWithLoginKeys lists accounts that hold at least one authorized SSH
// key, so disabling password logins cannot leave the host with no way in.
func (o *observation) accountsWithLoginKeys(settings *sshdObservation) []string {
	patterns := []string{".ssh/authorized_keys"}
	if settings != nil {
		if configured := settings.values["authorizedkeysfile"]; len(configured) > 0 {
			patterns = strings.Fields(strings.Join(configured, " "))
		}
	}
	homes := o.accountHomes()
	var accounts []string
	for name, home := range homes {
		for _, pattern := range patterns {
			if strings.EqualFold(pattern, "none") {
				continue
			}
			path := strings.NewReplacer("%h", home, "%u", name, "%%", "%").Replace(pattern)
			if !filepath.IsAbs(path) {
				path = filepath.Join(home, path)
			}
			if raw, err := o.engine.Host.ReadFile(path); err == nil && hasAuthorizedKey(string(raw)) {
				accounts = append(accounts, name)
				break
			}
		}
	}
	sort.Strings(accounts)
	return accounts
}

func hasAuthorizedKey(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return true
		}
	}
	return false
}

// accountHomes maps login accounts to their home directories from
// /etc/passwd. Accounts with a non-login shell cannot use the keys.
func (o *observation) accountHomes() map[string]string {
	homes := map[string]string{}
	raw, err := o.engine.Host.ReadFile("/etc/passwd")
	if err != nil {
		return homes
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[0] == "" || fields[5] == "" {
			continue
		}
		shell := fields[6]
		if strings.HasSuffix(shell, "nologin") || strings.HasSuffix(shell, "/false") {
			continue
		}
		homes[fields[0]] = fields[5]
	}
	return homes
}
