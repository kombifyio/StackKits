package nativehost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	cryptossh "golang.org/x/crypto/ssh"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

const cloudHostSecurityProcessOutputLimit = 256 << 10

// cloudHostSecurityProcessRunner is the closed process seam. Callers cannot
// widen it: the only accepted argv shapes are produced by the helpers in this
// file, and the runner re-checks every one of them before exec.
type cloudHostSecurityProcessRunner interface {
	Run(ctx context.Context, args []string) ([]byte, error)
}

func cloudHostSecurityNFTApplyArgs(rulesetPath string) []string {
	return []string{"nft", "-f", rulesetPath}
}

func cloudHostSecurityNFTListArgs() []string {
	return []string{"nft", "list", "table", "inet", cloudHostSecurityTable}
}

func cloudHostSecuritySSHDValidateArgs() []string {
	return []string{"sshd", "-t"}
}

func cloudHostSecuritySSHDEffectiveArgs() []string {
	return []string{"sshd", "-T"}
}

func cloudHostSecurityReloadArgs(unit string) []string {
	return []string{"systemctl", "reload", unit}
}

func cloudHostSecurityEnableArgs(unit string) []string {
	return []string{"systemctl", "enable", "--now", unit}
}

func cloudHostSecurityIsEnabledArgs(unit string) []string {
	return []string{"systemctl", "is-enabled", unit}
}

func cloudHostSecurityIsActiveArgs(unit string) []string {
	return []string{"systemctl", "is-active", unit}
}

func cloudHostSecurityPackageRefreshArgs() []string {
	return []string{"apt-get", "update", "-y"}
}

func cloudHostSecurityPackageInstallArgs(name string) []string {
	return []string{"apt-get", "install", "-y", "--no-install-recommends", name}
}

// cloudHostSecurityManagedUnits is the closed set of units this owner may
// touch. A policy can select among them; it can never name a new one.
var cloudHostSecurityManagedUnits = []string{"ssh", "fail2ban", "unattended-upgrades"}

// cloudHostSecurityToolingPackages binds each command this owner depends on to
// the exact package that provides it. Both sides are closed: a policy can name
// neither another command nor another package.
var cloudHostSecurityToolingPackages = map[string]string{
	"nft":                "nftables",
	"fail2ban-server":    "fail2ban",
	"unattended-upgrade": "unattended-upgrades",
}

func validCloudHostSecurityPackage(name string) bool {
	for _, managed := range cloudHostSecurityToolingPackages {
		if managed == name {
			return true
		}
	}
	return false
}

// renderCloudHostFirewallRuleset produces the file this owner hands to nft.
// The create/delete preamble makes the load idempotent: nft applies the whole
// file as one transaction, so re-running it replaces the owned table instead
// of appending a second copy of every rule.
func renderCloudHostFirewallRuleset(policy CloudFirewallPolicy) string {
	return "table inet " + cloudHostSecurityTable + "\n" +
		"delete table inet " + cloudHostSecurityTable + "\n" +
		renderCloudHostFirewallTable(policy)
}

// renderCloudHostFirewallTable is the canonical text of the owned table. It is
// what a listing of the live host has to normalize to.
func renderCloudHostFirewallTable(policy CloudFirewallPolicy) string {
	lines := []string{
		"table inet " + cloudHostSecurityTable + " {",
		"\tchain " + cloudHostSecurityEdgeChain + " {",
		"\t}",
		"\tchain " + cloudHostSecurityBaseChain + " {",
		"\t\ttype filter hook input priority filter; policy " + cloudHostFirewallDefaultPolicy(policy) + ";",
		"\t\tct state established,related accept",
		"\t\tiif \"lo\" accept",
		"\t\tct state invalid drop",
	}
	if subnet := strings.TrimSpace(policy.TransportSubnet); subnet != "" {
		lines = append(lines, "\t\tip saddr "+subnet+" accept")
	}
	if !policy.IPv6 {
		lines = append(lines, "\t\tmeta nfproto ipv6 drop")
	}
	// The authenticated execution channel must survive its own policy: an
	// owner that locks the channel out cannot reconcile or verify anything.
	lines = append(lines,
		"\t\ttcp dport 22 accept",
		"\t\tjump "+cloudHostSecurityEdgeChain,
		"\t}",
		"}",
	)
	return strings.Join(lines, "\n") + "\n"
}

func cloudHostFirewallDefaultPolicy(policy CloudFirewallPolicy) string {
	if strings.EqualFold(strings.TrimSpace(policy.DefaultIngress), "deny") {
		return "drop"
	}
	return "accept"
}

func validCloudFirewallPolicy(policy CloudFirewallPolicy) error {
	if !validCoreHostBootstrapDigest(policy.PolicyDigest) || !validCoreHostBootstrapDigest(policy.RequestDigest) ||
		!validCoreHostBootstrapDigest(policy.StateDigest) {
		return errors.New("Cloud firewall policy is not a bound executor request")
	}
	if strings.TrimSpace(policy.StackID) == "" || strings.TrimSpace(policy.SiteRef) == "" ||
		strings.TrimSpace(policy.NodeRef) == "" || strings.TrimSpace(policy.ExecutionChannelRef) == "" ||
		strings.TrimSpace(policy.EvaluatedAt) == "" {
		return errors.New("Cloud firewall policy is missing its exact node binding")
	}
	if !slices.Contains([]string{"deny", "allow"}, strings.TrimSpace(policy.DefaultIngress)) {
		return errors.New("Cloud firewall policy default ingress is outside the closed contract")
	}
	if !policy.DeclaredServicesOnly {
		return errors.New("Cloud firewall policy must keep ingress limited to declared services")
	}
	if subnet := strings.TrimSpace(policy.TransportSubnet); subnet == "" || strings.ContainsAny(subnet, " \t\r\n\"{};") {
		return errors.New("Cloud firewall policy transport subnet is not a bounded literal")
	}
	if policy.BaseRuleset == "" || policy.PublicEdgeChain == "" {
		return errors.New("Cloud firewall policy is missing its owned ruleset references")
	}
	return nil
}

func validCloudHardeningPolicy(policy CloudHardeningPolicy) error {
	if !validCoreHostBootstrapDigest(policy.PolicyDigest) || !validCoreHostBootstrapDigest(policy.RequestDigest) ||
		!validCoreHostBootstrapDigest(policy.StateDigest) {
		return errors.New("Cloud hardening policy is not a bound executor request")
	}
	if policy.Profile != cloudHostSecurityHardeningProfile {
		return errors.New("Cloud hardening profile is outside the closed contract")
	}
	if !policy.SSHKeyOnly || policy.SSHRootLogin != "disabled" {
		return errors.New("Cloud hardening profile requires key-only SSH without root login")
	}
	if policy.BruteForceProtection != "enabled" || policy.AutomaticSecurityUpdates != "enabled" {
		return errors.New("Cloud hardening profile requires brute-force protection and automatic security updates")
	}
	return nil
}

func cloudFirewallPolicyFromExpectation(expectation CloudHostSecurityVerifyExpectation) CloudFirewallPolicy {
	return CloudFirewallPolicy{
		NetworkMode: expectation.NetworkMode, TransportSubnet: expectation.TransportSubnet, IPv6: expectation.IPv6,
		BaseRuleset: expectation.BaseRuleset, PublicEdgeChain: expectation.PublicEdgeChain,
		DefaultIngress: expectation.DefaultIngress, DeclaredServicesOnly: expectation.DeclaredServicesOnly,
	}
}

func cloudHardeningPolicyFromExpectation(expectation CloudHostSecurityVerifyExpectation) CloudHardeningPolicy {
	return CloudHardeningPolicy{
		Profile: expectation.HardeningProfile, TLSMinVersion: expectation.TLSMinVersion,
		SSHKeyOnly: expectation.SSHKeyOnly, SSHRootLogin: expectation.SSHRootLogin,
		BruteForceProtection: expectation.BruteForceProtection, AutomaticSecurityUpdates: expectation.AutomaticSecurityUpdates,
	}
}

// observeFirewall lists the owned table and renders it back into the same
// canonical form the owner installs, so the comparison is over meaning rather
// than over nft's formatting.
func (o *osCloudHostSecurityOperations) observeFirewall(ctx context.Context) (string, error) {
	raw, err := o.runner.Run(ctx, cloudHostSecurityNFTListArgs())
	if err != nil {
		return "", fmt.Errorf("list the owned Cloud host ruleset: %w", err)
	}
	return normalizeCloudHostRuleset(string(raw)), nil
}

// normalizeCloudHostRuleset drops comments, handles and blank lines, and
// re-indents by nesting depth. nft echoes rules with extra counters and
// formatting that carry no policy meaning.
func normalizeCloudHostRuleset(raw string) string {
	var out []string
	depth := 0
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if index := strings.Index(trimmed, " # handle "); index >= 0 {
			trimmed = strings.TrimSpace(trimmed[:index])
		}
		if trimmed == "}" {
			depth--
		}
		if depth < 0 {
			return raw
		}
		out = append(out, strings.Repeat("\t", depth)+trimmed)
		if strings.HasSuffix(trimmed, "{") {
			depth++
		}
	}
	return strings.Join(out, "\n") + "\n"
}

func (o *osCloudHostSecurityOperations) writeSSHHardening(ctx context.Context, policy CloudHardeningPolicy) error {
	content := strings.Join([]string{
		"# Managed by StackKits Cloud host security. Do not edit.",
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
		"ChallengeResponseAuthentication no",
		"PermitRootLogin no",
		"PubkeyAuthentication yes",
		"", // trailing newline
	}, "\n")
	if err := os.Remove(cloudHostSecurityLegacySSHDropIn); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the superseded Cloud host-security sshd drop-in: %w", err)
	}
	if err := writeCloudHostSecuritySystemFile(cloudHostSecuritySSHDropIn, content); err != nil {
		return err
	}
	if err := ensureCloudHostSecuritySSHDRuntimeDirectory(); err != nil {
		return err
	}
	if _, err := o.runner.Run(ctx, cloudHostSecuritySSHDValidateArgs()); err != nil {
		return fmt.Errorf("validate the hardened sshd configuration: %w", err)
	}
	if err := o.reloadSSHDIfActive(ctx); err != nil {
		return err
	}
	_ = policy
	return nil
}

// reloadSSHDIfActive hands the drop-in to an already running sshd. Ubuntu
// socket-activates ssh, so ssh.service is normally inactive and systemd
// refuses to reload it; every new connection then starts an sshd that reads
// the drop-in anyway. The effective configuration is observed separately, so
// declining an impossible reload does not weaken the postcondition.
func (o *osCloudHostSecurityOperations) reloadSSHDIfActive(ctx context.Context) error {
	raw, err := o.runner.Run(ctx, cloudHostSecurityIsActiveArgs("ssh"))
	if err != nil {
		return fmt.Errorf("observe the sshd service state: %w", err)
	}
	if strings.TrimSpace(string(raw)) != "active" {
		return nil
	}
	if _, err := o.runner.Run(ctx, cloudHostSecurityReloadArgs("ssh")); err != nil {
		return fmt.Errorf("reload sshd with the hardened configuration: %w", err)
	}
	return nil
}

// ensureManagedTooling installs the owner's own packages when the commands
// they provide are absent. A provider hands over a stock Ubuntu image with
// none of them, so without this the owner would enable units and load rulesets
// that cannot exist and would report a posture it can never reach.
func (o *osCloudHostSecurityOperations) ensureManagedTooling(ctx context.Context, commands ...string) error {
	missing := make([]string, 0, len(commands))
	for _, command := range commands {
		name, managed := cloudHostSecurityToolingPackages[command]
		if !managed {
			return errors.New("Cloud host-security tooling is outside the closed contract")
		}
		if _, err := exec.LookPath(command); err != nil && !slices.Contains(missing, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if _, err := o.runner.Run(ctx, cloudHostSecurityPackageRefreshArgs()); err != nil {
		return fmt.Errorf("refresh the package index for %s: %w", strings.Join(missing, ", "), err)
	}
	for _, name := range missing {
		if _, err := o.runner.Run(ctx, cloudHostSecurityPackageInstallArgs(name)); err != nil {
			return fmt.Errorf("install the managed %s package: %w", name, err)
		}
	}
	return nil
}

func (o *osCloudHostSecurityOperations) writeAutomaticSecurityUpdates(ctx context.Context, policy CloudHardeningPolicy) error {
	content := strings.Join([]string{
		"// Managed by StackKits Cloud host security. Do not edit.",
		"APT::Periodic::Update-Package-Lists \"1\";",
		"APT::Periodic::Unattended-Upgrade \"1\";",
		"",
	}, "\n")
	if err := writeCloudHostSecuritySystemFile(cloudHostSecurityAPTDropIn, content); err != nil {
		return err
	}
	if policy.AutomaticSecurityUpdates != "enabled" {
		return errors.New("automatic security updates are required by the Cloud hardening profile")
	}
	if _, err := o.runner.Run(ctx, cloudHostSecurityEnableArgs("unattended-upgrades")); err != nil {
		return fmt.Errorf("enable unattended security upgrades: %w", err)
	}
	return nil
}

func (o *osCloudHostSecurityOperations) enableBruteForceProtection(ctx context.Context, policy CloudHardeningPolicy) error {
	if policy.BruteForceProtection != "enabled" {
		return errors.New("brute-force protection is required by the Cloud hardening profile")
	}
	if _, err := o.runner.Run(ctx, cloudHostSecurityEnableArgs("fail2ban")); err != nil {
		return fmt.Errorf("enable brute-force protection: %w", err)
	}
	return nil
}

func (o *osCloudHostSecurityOperations) verifyManagedUnits(ctx context.Context, expectation CloudHostSecurityVerifyExpectation) error {
	units := map[string]string{
		"fail2ban":            expectation.BruteForceProtection,
		"unattended-upgrades": expectation.AutomaticSecurityUpdates,
	}
	for _, unit := range []string{"fail2ban", "unattended-upgrades"} {
		if units[unit] != "enabled" {
			return fmt.Errorf("Cloud hardening expectation does not enable %s", unit)
		}
		raw, err := o.runner.Run(ctx, cloudHostSecurityIsEnabledArgs(unit))
		if err != nil {
			return fmt.Errorf("observe %s: %w", unit, err)
		}
		if state := strings.TrimSpace(string(raw)); state != "enabled" && state != "static" && state != "enabled-runtime" {
			return fmt.Errorf("%s is %q rather than enabled", unit, state)
		}
	}
	return nil
}

type cloudHostSecuritySSHDSettings map[string]string

func (s cloudHostSecuritySSHDSettings) satisfies(policy CloudHardeningPolicy) error {
	if policy.SSHKeyOnly {
		for _, key := range []string{"passwordauthentication", "kbdinteractiveauthentication"} {
			if s[key] != "no" {
				return fmt.Errorf("sshd still accepts %s", key)
			}
		}
		if s["pubkeyauthentication"] != "yes" {
			return errors.New("sshd does not accept public keys, which would lock out the execution channel")
		}
	}
	if policy.SSHRootLogin == "disabled" && s["permitrootlogin"] != "no" {
		return fmt.Errorf("sshd permits root login as %q", s["permitrootlogin"])
	}
	return nil
}

const (
	cloudHostSecurityExecutionUser      = "kombify"
	cloudHostSecurityExecutionShell     = "/bin/bash"
	cloudHostSecurityExecutionSudoers   = "/etc/sudoers.d/60-stackkits-execution-channel"
	cloudHostSecurityRootAuthorizedKeys = "/root/.ssh/authorized_keys"
	cloudHostSecurityMintedKeyName      = "id_ed25519"
	cloudHostSecurityMintedPubName      = "id_ed25519.pub"
	cloudHostSecurityMintedKeyScope     = "execution-channel"
)

func cloudExecutionChannelCustodyKeyPath(workspaceRoot, name string) string {
	return filepath.Join(workspaceRoot, ".stackkit", "cloud-host-security", cloudHostSecurityMintedKeyScope, name)
}

type cloudHostSecurityExecutionLayout struct {
	user         string
	rootKeysPath string
	sudoersPath  string
	lookup       func(string) (*user.User, error)
}

func defaultCloudHostSecurityExecutionLayout() cloudHostSecurityExecutionLayout {
	return cloudHostSecurityExecutionLayout{
		user:         cloudHostSecurityExecutionUser,
		rootKeysPath: cloudHostSecurityRootAuthorizedKeys,
		sudoersPath:  cloudHostSecurityExecutionSudoers,
		lookup:       user.Lookup,
	}
}

func cloudHostSecurityUserAddArgs(username string) []string {
	return []string{"useradd", "--create-home", "--shell", cloudHostSecurityExecutionShell, username}
}

func validLinuxExecutionChannelUser(name string) bool {
	if name == "" || name == "root" || len(name) > 32 {
		return false
	}
	for i, r := range name {
		if i == 0 && (r < 'a' || r > 'z') {
			return false
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func executionChannelUserFromOwnerCustody(workspaceRoot string) string {
	custody, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return ""
	}
	if validLinuxExecutionChannelUser(custody.PocketID.Username) {
		return custody.PocketID.Username
	}
	return ""
}

// PrepareCloudExecutionChannel provisions the non-root execution-channel account
// and workspace-custodied SSH key before Cloud host-security disables root
// login. Installers and `stackkit host prepare` call this idempotently. A
// dispatched Apply (see NewOSCloudHostSecurityOperationsForDispatchedChannel)
// keeps the default execution account.
func PrepareCloudExecutionChannel(ctx context.Context, workspaceRoot string, dispatched bool) error {
	constructor := NewOSCloudHostSecurityOperations
	if dispatched {
		constructor = NewOSCloudHostSecurityOperationsForDispatchedChannel
	}
	operations, err := constructor(workspaceRoot)
	if err != nil {
		return err
	}
	return operations.preserveExecutionChannelAccount(ctx)
}

// preserveExecutionChannelAccount copies any existing SSH public keys onto a
// non-root login and grants it passwordless sudo before PermitRootLogin is
// disabled. When the host has no provider-injected keys (a password-root VPS),
// it mints a workspace-custodied execution-channel key rather than aborting
// Cloud Kit Apply.
func (o *osCloudHostSecurityOperations) preserveExecutionChannelAccount(ctx context.Context) error {
	layout := o.execution
	if strings.TrimSpace(layout.user) == "" {
		layout = defaultCloudHostSecurityExecutionLayout()
	}
	if layout.user == cloudHostSecurityExecutionUser && !o.dispatchedChannel {
		if owner := executionChannelUserFromOwnerCustody(o.workspaceRoot); owner != "" {
			layout.user = owner
		}
	}
	lookup := layout.lookup
	if lookup == nil {
		lookup = user.Lookup
	}
	account, err := lookup(layout.user)
	if err != nil {
		if _, addErr := o.runner.Run(ctx, cloudHostSecurityUserAddArgs(layout.user)); addErr != nil {
			return fmt.Errorf("create the Cloud execution-channel account: %w", addErr)
		}
		account, err = lookup(layout.user)
		if err != nil {
			return errors.New("Cloud execution-channel account is missing after create")
		}
	}
	home := strings.TrimSpace(account.HomeDir)
	if home == "" {
		return errors.New("Cloud execution-channel account has no home directory")
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	gid, gidErr := strconv.Atoi(account.Gid)
	if uidErr != nil || gidErr != nil {
		uid, gid = -1, -1
	}
	keys, err := o.ensureExecutionChannelAuthorizedKeys(layout, home)
	if err != nil {
		return err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return fmt.Errorf("create the execution-channel SSH directory: %w", err)
	}
	if uid >= 0 && gid >= 0 {
		_ = os.Chown(sshDir, uid, gid)
	}
	if err := writeExecutionChannelFile(filepath.Join(sshDir, "authorized_keys"), keys, 0o600, uid, gid); err != nil {
		return err
	}
	if private := readOptionalFile(cloudExecutionChannelCustodyKeyPath(o.workspaceRoot, cloudHostSecurityMintedKeyName)); len(private) > 0 {
		if err := writeExecutionChannelFile(filepath.Join(sshDir, cloudHostSecurityMintedKeyName), string(private), 0o600, uid, gid); err != nil {
			return err
		}
	}
	sudoers := "# Managed by StackKits Cloud host security. Do not edit.\n" + layout.user + " ALL=(ALL) NOPASSWD:ALL\n"
	return writeExecutionChannelFile(layout.sudoersPath, sudoers, 0o440, 0, 0)
}

func (o *osCloudHostSecurityOperations) ensureExecutionChannelAuthorizedKeys(layout cloudHostSecurityExecutionLayout, home string) (string, error) {
	keys := mergeAuthorizedSSHKeys(executionChannelAuthorizedKeySources(layout, home)...)
	if strings.TrimSpace(keys) != "" {
		return keys, nil
	}
	if pub := readOptionalFile(cloudExecutionChannelCustodyKeyPath(o.workspaceRoot, cloudHostSecurityMintedPubName)); strings.TrimSpace(string(pub)) != "" {
		return mergeAuthorizedSSHKeys(pub), nil
	}
	authorized, private, err := mintCloudExecutionChannelKey()
	if err != nil {
		return "", err
	}
	if _, err := o.persist(cloudHostSecurityMintedKeyScope, cloudHostSecurityMintedKeyName, private, 0o600); err != nil {
		return "", err
	}
	if _, err := o.persist(cloudHostSecurityMintedKeyScope, cloudHostSecurityMintedPubName, []byte(authorized), 0o644); err != nil {
		return "", err
	}
	return authorized, nil
}

func executionChannelAuthorizedKeySources(layout cloudHostSecurityExecutionLayout, home string) [][]byte {
	sources := [][]byte{
		readOptionalFile(layout.rootKeysPath),
		readOptionalFile(filepath.Join(home, ".ssh", "authorized_keys")),
		readOptionalFile("/home/ubuntu/.ssh/authorized_keys"),
		readOptionalFile("/home/debian/.ssh/authorized_keys"),
		readOptionalFile("/home/admin/.ssh/authorized_keys"),
	}
	if sudoUser := strings.TrimSpace(os.Getenv("SUDO_USER")); sudoUser != "" && sudoUser != "root" && sudoUser != layout.user {
		if account, err := user.Lookup(sudoUser); err == nil {
			sources = append(sources, readOptionalFile(filepath.Join(account.HomeDir, ".ssh", "authorized_keys")))
		}
	}
	return sources
}

func mintCloudExecutionChannelKey() (authorized string, private []byte, err error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, fmt.Errorf("mint Cloud execution-channel key: %w", err)
	}
	sshPublic, err := cryptossh.NewPublicKey(publicKey)
	if err != nil {
		return "", nil, fmt.Errorf("encode Cloud execution-channel public key: %w", err)
	}
	block, err := cryptossh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return "", nil, fmt.Errorf("encode Cloud execution-channel private key: %w", err)
	}
	authorized = strings.TrimSpace(string(cryptossh.MarshalAuthorizedKey(sshPublic))) + " stackkits-cloud-execution-channel\n"
	return authorized, pem.EncodeToMemory(block), nil
}

func mergeAuthorizedSSHKeys(parts ...[]byte) string {
	seen := map[string]struct{}{}
	var out []string
	for _, part := range parts {
		for _, line := range strings.Split(strings.ReplaceAll(string(part), "\r\n", "\n"), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if _, exists := seen[trimmed]; exists {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

func readOptionalFile(path string) []byte {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}

func writeExecutionChannelFile(path, content string, mode os.FileMode, uid, gid int) error {
	clean := filepath.Clean(path)
	if clean != path || path == "" || strings.Contains(path, "\x00") {
		return errors.New("execution-channel path is not canonical")
	}
	directory := filepath.Dir(path)
	if info, err := os.Lstat(directory); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create the execution-channel directory: %w", err)
		}
	}
	if existing, err := os.Lstat(path); err == nil && existing.Mode()&os.ModeSymlink != 0 {
		return errors.New("execution-channel path is a symlink")
	} else if err == nil {
		_ = os.Chmod(path, 0o600)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write the execution-channel file: %w", err)
	}
	_ = os.Chmod(path, mode)
	if uid >= 0 && gid >= 0 {
		_ = os.Chown(path, uid, gid)
	}
	return nil
}

func (o *osCloudHostSecurityOperations) observeSSHD(ctx context.Context) (cloudHostSecuritySSHDSettings, error) {
	raw, err := o.runner.Run(ctx, cloudHostSecuritySSHDEffectiveArgs())
	if err != nil {
		return nil, fmt.Errorf("observe the effective sshd configuration: %w", err)
	}
	settings := cloudHostSecuritySSHDSettings{}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		settings[strings.ToLower(key)] = strings.TrimSpace(value)
	}
	if len(settings) == 0 {
		return nil, errors.New("sshd reported no effective configuration")
	}
	return settings, nil
}

// ensureCloudHostSecuritySSHDRuntimeDirectory creates the privilege
// separation directory sshd requires before it will parse a configuration at
// all. Only ssh.service declares it as a systemd RuntimeDirectory, and Ubuntu
// socket-activates ssh, so on a stock host the directory is absent and every
// sshd validation and observation this owner performs would fail.
func ensureCloudHostSecuritySSHDRuntimeDirectory() error {
	if info, err := os.Lstat(cloudHostSecuritySSHDRuntimeDirectory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("the sshd privilege separation path is not a plain directory")
		}
		return nil
	}
	if err := os.MkdirAll(cloudHostSecuritySSHDRuntimeDirectory, 0o755); err != nil {
		return fmt.Errorf("create the sshd privilege separation directory: %w", err)
	}
	return os.Chmod(cloudHostSecuritySSHDRuntimeDirectory, 0o755)
}

// writeCloudHostSecuritySystemFile installs one managed drop-in. The parent
// directory must already exist: creating /etc trees for a package that is not
// installed would report a posture the host cannot actually enforce.
func writeCloudHostSecuritySystemFile(path, content string) error {
	if filepath.Clean(path) != path || !strings.HasPrefix(path, "/etc/") {
		return errors.New("Cloud host-security drop-in path is outside /etc")
	}
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("managed configuration directory %s is not present", directory)
	}
	if existing, statErr := os.Lstat(path); statErr == nil && existing.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed configuration path is a symlink")
	}
	// Only root reads these drop-ins (sshd and apt run as root), so keep them
	// owner-only rather than world-readable.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write managed configuration %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}

type osCloudHostSecurityProcessRunner struct{}

// Run re-validates the argv against the closed contract before exec, so a
// future caller cannot smuggle a different command through this seam.
func (osCloudHostSecurityProcessRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	if err := validCloudHostSecurityArgs(args); err != nil {
		return nil, err
	}
	executable, err := exec.LookPath(args[0])
	if err != nil {
		return nil, fmt.Errorf("required %s tooling is not installed", args[0])
	}
	command := exec.CommandContext(ctx, executable, args[1:]...)
	// The package steps must never wait on an interactive prompt: the owner
	// runs unattended and a blocked apt-get would hang the whole Apply.
	command.Env = []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C",
		"DEBIAN_FRONTEND=noninteractive",
	}
	output := &cloudHostSecurityBoundedBuffer{remaining: cloudHostSecurityProcessOutputLimit}
	command.Stdout, command.Stderr = output, output
	runErr := command.Run()
	if output.exceeded {
		return nil, errors.New("Cloud host-security process output exceeded the bound")
	}
	if runErr != nil {
		// systemctl is-enabled and is-active report state through the exit
		// code; their stdout is the answer the caller has to see.
		if args[0] == "systemctl" && (args[1] == "is-enabled" || args[1] == "is-active") {
			return append([]byte(nil), output.Bytes()...), nil
		}
		if detail := strings.TrimSpace(output.String()); detail != "" {
			return nil, fmt.Errorf("bounded %s process failed (%s): %s", args[0], strings.Join(args[1:], " "), detail)
		}
		return nil, fmt.Errorf("bounded %s process failed", args[0])
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func validCloudHostSecurityArgs(args []string) error {
	switch {
	case len(args) == 3 && args[0] == "nft" && args[1] == "-f" &&
		filepath.Clean(args[2]) == args[2] && filepath.Base(args[2]) == "ruleset.nft":
		return nil
	case slices.Equal(args, cloudHostSecurityNFTListArgs()),
		slices.Equal(args, cloudHostSecuritySSHDValidateArgs()),
		slices.Equal(args, cloudHostSecuritySSHDEffectiveArgs()):
		return nil
	case len(args) == 3 && args[0] == "systemctl" && args[1] == "reload" && slices.Contains(cloudHostSecurityManagedUnits, args[2]):
		return nil
	case len(args) == 4 && args[0] == "systemctl" && args[1] == "enable" && args[2] == "--now" && slices.Contains(cloudHostSecurityManagedUnits, args[3]):
		return nil
	case len(args) == 3 && args[0] == "systemctl" && args[1] == "is-enabled" && slices.Contains(cloudHostSecurityManagedUnits, args[2]):
		return nil
	case len(args) == 3 && args[0] == "systemctl" && args[1] == "is-active" && slices.Contains(cloudHostSecurityManagedUnits, args[2]):
		return nil
	case slices.Equal(args, cloudHostSecurityPackageRefreshArgs()):
		return nil
	case len(args) == 5 && args[0] == "apt-get" && args[1] == "install" && args[2] == "-y" &&
		args[3] == "--no-install-recommends" && validCloudHostSecurityPackage(args[4]):
		return nil
	case len(args) == 5 && args[0] == "useradd" && args[1] == "--create-home" &&
		args[2] == "--shell" && args[3] == cloudHostSecurityExecutionShell && validLinuxExecutionChannelUser(args[4]):
		return nil
	}
	return errors.New("process is outside the closed Cloud host-security contract")
}

type cloudHostSecurityBoundedBuffer struct {
	bytes.Buffer
	remaining int
	exceeded  bool
}

func (b *cloudHostSecurityBoundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if len(value) > b.remaining {
		value = value[:b.remaining]
		b.exceeded = true
	}
	b.remaining -= len(value)
	_, _ = b.Buffer.Write(value)
	return original, nil
}

var _ CloudHostSecurityOperations = (*osCloudHostSecurityOperations)(nil)
