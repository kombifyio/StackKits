package hostsecurity

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// StackKits-managed SSH certificate access. The user CA trust and the host
// certificate each have their own sshd drop-in, sorted before distribution
// drop-ins so their first-match settings win, and their own files under
// /etc/ssh. sshd reads the CA keys and principals files at every
// authentication, so emptying them revokes at once; only the drop-ins need a
// reload.
const (
	SSHAccessDir             = "/etc/ssh/stackkit"
	SSHTrustedUserCAKeysPath = SSHAccessDir + "/trusted_user_ca_keys"
	SSHPrincipalsDir         = SSHAccessDir + "/principals"
	SSHPrincipalsPattern     = SSHPrincipalsDir + "/%u"
	SSHUserCADropIn          = "/etc/ssh/sshd_config.d/01-stackkit-ssh-user-ca.conf"
	SSHHostCertificateDropIn = "/etc/ssh/sshd_config.d/01-stackkit-ssh-host-certificate.conf"

	maxSSHCAKeys         = 8
	maxSSHUsers          = 32
	maxSSHUserPrincipals = 64
	minSSHRSABits        = 3072
	// hostCertificateRenewShare is the remaining share of a host certificate's
	// validity below which it is reported as expiring.
	hostCertificateRenewShare = 5

	managedSSHHeader = "# Managed by StackKits host delivery. Do not edit."
)

// Reason codes of the SSH access controls.
const (
	ReasonSSHCleanupPending        = "ssh_access_cleanup_pending"
	ReasonHostCertificateExpired   = "ssh_host_certificate_expired"
	ReasonHostCertificateExpiring  = "ssh_host_certificate_expiring"
	ReasonHostCertificateNotForKey = "ssh_host_certificate_key_mismatch"
)

var (
	sshUserName  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	sshPrincipal = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+-]{0,127}$`)
)

// SSHAccess is the SSH certificate access StackKits holds on this server: the
// owner's user CA keys, the principals each local user accepts, and one host
// certificate. No CA keys means no CA trust (revoked or never delivered).
type SSHAccess struct {
	CAKeys          []string
	Principals      map[string][]string
	HostCertificate string
}

// SSHAccessState is what node custody records for SSH access. Verify judges
// the host against it and repair reinstalls it.
type SSHAccessState struct {
	Desired SSHAccess
	// Recorded is false when custody never received SSH access; then the
	// controls still report managed files or effective foreign user-CA trust.
	Recorded bool
	// CleanupPending names revocation effects not yet completed on the target.
	CleanupPending []string
	// Failure explains why custody could not be read; the controls are unknown.
	Failure string
	// HostNames are the names a host certificate must carry. Empty uses the
	// system hostname.
	HostNames []string
}

// SSHAccessResult reports one SSH access change.
type SSHAccessResult struct {
	Changes []string `json:"changes,omitempty"`
	// CleanupPending names a revocation effect that could not be completed;
	// the revocation itself already holds (the CA keys file is empty).
	CleanupPending []string `json:"cleanupPending,omitempty"`
	// HostCertificatePath is where the host certificate was installed.
	HostCertificatePath string `json:"hostCertificatePath,omitempty"`
}

// NormalizeSSHUserCATrust validates CA keys and principals and returns their
// canonical form: comment-free authorized-key lines and sorted, de-duplicated
// principals. An empty key list is a revocation and carries no principals.
func NormalizeSSHUserCATrust(caKeys []string, principals map[string][]string) ([]string, map[string][]string, error) {
	keys := []string{}
	for _, line := range caKeys {
		key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
		if err != nil || len(options) > 0 || len(bytes.TrimSpace(rest)) > 0 || strings.ContainsAny(comment, "\r\n") {
			return nil, nil, errors.New("an SSH CA key is not a single OpenSSH public key line")
		}
		if err := acceptableCAKey(key); err != nil {
			return nil, nil, err
		}
		keys = append(keys, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	if len(keys) > maxSSHCAKeys {
		return nil, nil, fmt.Errorf("at most %d SSH CA keys may be trusted", maxSSHCAKeys)
	}
	normalized := map[string][]string{}
	if len(keys) == 0 {
		return keys, normalized, nil
	}
	if len(principals) == 0 || len(principals) > maxSSHUsers {
		return nil, nil, fmt.Errorf("SSH CA trust needs between 1 and %d local users with principals", maxSSHUsers)
	}
	for user, names := range principals {
		if !sshUserName.MatchString(user) || user == "root" {
			return nil, nil, fmt.Errorf("local user %q cannot accept CA-signed logins (a valid non-root user name is required)", user)
		}
		var list []string
		for _, name := range names {
			if !sshPrincipal.MatchString(name) {
				return nil, nil, fmt.Errorf("principal %q of user %s is not a plain principal name", name, user)
			}
			list = append(list, name)
		}
		slices.Sort(list)
		list = slices.Compact(list)
		if len(list) == 0 || len(list) > maxSSHUserPrincipals {
			return nil, nil, fmt.Errorf("user %s needs between 1 and %d principals", user, maxSSHUserPrincipals)
		}
		normalized[user] = list
	}
	return keys, normalized, nil
}

func acceptableCAKey(key ssh.PublicKey) error {
	switch key.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoSKED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoSKECDSA256:
		return nil
	case ssh.KeyAlgoRSA:
		if crypto, ok := key.(ssh.CryptoPublicKey); ok {
			if public, ok := crypto.CryptoPublicKey().(*rsa.PublicKey); ok && public.N.BitLen() >= minSSHRSABits {
				return nil
			}
		}
		return fmt.Errorf("an RSA SSH CA key needs at least %d bits", minSSHRSABits)
	}
	return fmt.Errorf("SSH CA key type %s is not accepted (certificates are never CA keys)", key.Type())
}

// --- reading what is installed ----------------------------------------------

type installedUserCA struct {
	keys       []string
	principals map[string][]string
	dropIn     []byte
	hasDropIn  bool
	hasKeys    bool
}

func (i installedUserCA) any() bool { return i.hasDropIn || i.hasKeys || len(i.principals) > 0 }

func managedLines(raw []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}

func (e Engine) readUserCA() (installedUserCA, error) {
	var installed installedUserCA
	if raw, err := e.Host.ReadFile(SSHUserCADropIn); err == nil {
		installed.dropIn, installed.hasDropIn = raw, true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return installedUserCA{}, err
	}
	if raw, err := e.Host.ReadFile(SSHTrustedUserCAKeysPath); err == nil {
		installed.hasKeys = true
		for _, line := range managedLines(raw) {
			key, _, _, _, parseErr := ssh.ParseAuthorizedKey([]byte(line))
			if parseErr != nil {
				installed.keys = append(installed.keys, line)
				continue
			}
			installed.keys = append(installed.keys, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
		}
		slices.Sort(installed.keys)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return installedUserCA{}, err
	}
	installed.principals = map[string][]string{}
	users, err := e.Host.ReadDirNames(SSHPrincipalsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return installedUserCA{}, err
	}
	for _, user := range users {
		raw, readErr := e.Host.ReadFile(SSHPrincipalsDir + "/" + user)
		if readErr != nil {
			return installedUserCA{}, readErr
		}
		names := managedLines(raw)
		slices.Sort(names)
		installed.principals[user] = names
	}
	return installed, nil
}

func renderCAKeys(keys []string) []byte {
	return []byte(managedSSHHeader + "\n" + strings.Join(keys, "\n") + "\n")
}

func renderPrincipals(names []string) []byte {
	return []byte(managedSSHHeader + "\n" + strings.Join(names, "\n") + "\n")
}

func renderUserCADropIn() []byte {
	return []byte(managedSSHHeader + "\nTrustedUserCAKeys " + SSHTrustedUserCAKeysPath + "\nAuthorizedPrincipalsFile " + SSHPrincipalsPattern + "\n")
}

func renderHostCertificateDropIn(path string) []byte {
	return []byte(managedSSHHeader + "\nHostCertificate " + path + "\n")
}

func intersectKeys(installed, desired []string) []string {
	var kept []string
	for _, key := range installed {
		if slices.Contains(desired, key) {
			kept = append(kept, key)
		}
	}
	return kept
}

func intersectPrincipals(installed, desired map[string][]string) map[string][]string {
	kept := map[string][]string{}
	for user, names := range installed {
		var list []string
		for _, name := range names {
			if slices.Contains(desired[user], name) {
				list = append(list, name)
			}
		}
		if len(list) > 0 {
			kept[user] = list
		}
	}
	return kept
}

func samePrincipals(left, right map[string][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for user, names := range left {
		if !slices.Equal(names, right[user]) {
			return false
		}
	}
	return true
}

// --- changing what is installed ---------------------------------------------

// sshFiles records and, when applying, performs file changes.
type sshFiles struct {
	engine  Engine
	apply   bool
	changes []string
}

func (f *sshFiles) write(path string, data []byte, mode os.FileMode) error {
	f.changes = append(f.changes, fmt.Sprintf("write %s (%04o)", path, mode))
	if !f.apply {
		return nil
	}
	return f.engine.Host.WriteFile(path, data, mode)
}

func (f *sshFiles) remove(path string) error {
	f.changes = append(f.changes, "remove "+path)
	if !f.apply {
		return nil
	}
	return f.engine.Host.Remove(path)
}

// writePrincipals makes the principals directory hold exactly the given users.
func (f *sshFiles) writePrincipals(installed, desired map[string][]string) error {
	for user := range installed {
		if _, keep := desired[user]; !keep {
			if err := f.remove(SSHPrincipalsDir + "/" + user); err != nil {
				return err
			}
		}
	}
	users := make([]string, 0, len(desired))
	for user := range desired {
		users = append(users, user)
	}
	sort.Strings(users)
	for _, user := range users {
		if slices.Equal(installed[user], desired[user]) {
			continue
		}
		if err := f.write(SSHPrincipalsDir+"/"+user, renderPrincipals(desired[user]), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// reloadSSHD makes a running sshd service read its configuration again. A
// socket-activated sshd (no running service) reads it per connection.
func (e Engine) reloadSSHD(ctx context.Context) error {
	if _, ok := e.toolPath("systemctl"); !ok {
		return errors.New("systemctl is not available to reload sshd")
	}
	for _, unit := range []string{"ssh", "sshd"} {
		active, _ := e.run(ctx, "systemctl", "is-active", unit)
		if strings.TrimSpace(active.Stdout) != "active" {
			continue
		}
		output, err := e.run(ctx, "systemctl", "reload", unit)
		if err != nil {
			return err
		}
		if output.ExitCode != 0 {
			return fmt.Errorf("systemctl reload %s failed: %s", unit, boundedText(output.Stderr))
		}
		return nil
	}
	return nil
}

func (e Engine) testSSHD(ctx context.Context) error {
	if err := e.Host.MkdirAll("/run/sshd", 0o755); err != nil {
		return fmt.Errorf("create the sshd privilege separation directory: %w", err)
	}
	output, err := e.run(ctx, sshdBinary, "-t")
	if err != nil {
		return fmt.Errorf("sshd -t could not run: %w", err)
	}
	if output.ExitCode != 0 {
		return fmt.Errorf("sshd rejected the configuration: %s", boundedText(output.Stderr))
	}
	return nil
}

func (e Engine) effectiveSSHD(ctx context.Context) (*sshdObservation, string) {
	return (&observation{engine: e}).sshdEffective(ctx)
}

// ApplySSHUserCATrust makes this server trust exactly the given user CA keys
// for exactly the given principals. Revocations take effect first, by
// emptying the files sshd reads at every authentication, and are never undone:
// a failed change is rolled back only to what both the previous and the new
// trust allow. A full revocation (no keys) that cannot reload sshd still holds
// and reports the pending cleanup.
func (e Engine) ApplySSHUserCATrust(ctx context.Context, caKeys []string, principals map[string][]string) (SSHAccessResult, error) {
	return e.convergeUserCA(ctx, caKeys, principals, true)
}

//nolint:gocyclo // One guarded sequence: revoke first, grant, validate, reload, read back, roll back.
func (e Engine) convergeUserCA(ctx context.Context, caKeys []string, principals map[string][]string, apply bool) (SSHAccessResult, error) {
	keys, wanted, err := NormalizeSSHUserCATrust(caKeys, principals)
	if err != nil {
		return SSHAccessResult{}, blocked("%s", err.Error())
	}
	if apply && !e.isRoot() {
		return SSHAccessResult{}, blocked("%s", rootHint("change the sshd CA trust"))
	}
	installed, err := e.readUserCA()
	if err != nil {
		return SSHAccessResult{}, fmt.Errorf("read the installed SSH CA trust: %w", err)
	}
	files := &sshFiles{engine: e, apply: apply}
	keptKeys, keptPrincipals := intersectKeys(installed.keys, keys), intersectPrincipals(installed.principals, wanted)
	if apply && (len(keptKeys) < len(installed.keys) || !samePrincipals(keptPrincipals, installed.principals)) {
		if installed.hasKeys {
			if err := files.write(SSHTrustedUserCAKeysPath, renderCAKeys(keptKeys), 0o644); err != nil {
				return SSHAccessResult{Changes: files.changes}, err
			}
		}
		if err := files.writePrincipals(installed.principals, keptPrincipals); err != nil {
			return SSHAccessResult{Changes: files.changes}, err
		}
		installed.principals = keptPrincipals
	}

	if len(keys) == 0 {
		return e.revokeUserCA(ctx, files, installed)
	}

	rollback := func() {
		if installed.hasDropIn {
			_ = e.Host.WriteFile(SSHUserCADropIn, installed.dropIn, 0o600)
		} else {
			_ = e.Host.Remove(SSHUserCADropIn)
		}
		if installed.hasKeys || len(keptKeys) > 0 {
			_ = e.Host.WriteFile(SSHTrustedUserCAKeysPath, renderCAKeys(keptKeys), 0o644)
		} else {
			_ = e.Host.Remove(SSHTrustedUserCAKeysPath)
		}
		current, readErr := e.readUserCA()
		if readErr == nil {
			_ = (&sshFiles{engine: e, apply: true}).writePrincipals(current.principals, keptPrincipals)
		}
	}
	if !installed.hasKeys || !slices.Equal(installed.keys, keys) {
		if err := files.write(SSHTrustedUserCAKeysPath, renderCAKeys(keys), 0o644); err != nil {
			rollback()
			return SSHAccessResult{Changes: files.changes}, err
		}
	}
	if err := files.writePrincipals(installed.principals, wanted); err != nil {
		rollback()
		return SSHAccessResult{Changes: files.changes}, err
	}
	if !installed.hasDropIn || !bytes.Equal(installed.dropIn, renderUserCADropIn()) {
		if err := files.write(SSHUserCADropIn, renderUserCADropIn(), 0o600); err != nil {
			rollback()
			return SSHAccessResult{Changes: files.changes}, err
		}
	}
	result := SSHAccessResult{Changes: files.changes}
	if !apply {
		return result, nil
	}
	if err := e.testSSHD(ctx); err != nil {
		rollback()
		return result, fmt.Errorf("%w; the previous SSH CA trust was restored without any revoked key", err)
	}
	if err := e.reloadSSHD(ctx); err != nil {
		rollback()
		_ = e.reloadSSHD(ctx)
		return result, fmt.Errorf("reload sshd: %w; the previous SSH CA trust was restored without any revoked key", err)
	}
	effective, failure := e.effectiveSSHD(ctx)
	if effective == nil {
		rollback()
		_ = e.reloadSSHD(ctx)
		return result, fmt.Errorf("the new sshd configuration could not be read back (%s); the previous SSH CA trust was restored without any revoked key", failure)
	}
	if effective.values["trustedusercakeys"] == nil || effective.values["trustedusercakeys"][0] != SSHTrustedUserCAKeysPath ||
		effective.values["authorizedprincipalsfile"] == nil || effective.values["authorizedprincipalsfile"][0] != SSHPrincipalsPattern {
		rollback()
		_ = e.reloadSSHD(ctx)
		return result, errors.New("another sshd setting overrides TrustedUserCAKeys or AuthorizedPrincipalsFile; remove it so the StackKits drop-in takes effect")
	}
	return result, nil
}

// revokeUserCA removes the CA trust. The keys file is emptied first, which
// already denies every CA-signed login; the drop-in is then removed and sshd
// reloaded. When that cannot finish, the revocation still holds and the
// remaining cleanup is reported instead of an error.
func (e Engine) revokeUserCA(ctx context.Context, files *sshFiles, installed installedUserCA) (SSHAccessResult, error) {
	if !installed.any() {
		return SSHAccessResult{}, nil
	}
	if installed.hasKeys {
		if err := files.write(SSHTrustedUserCAKeysPath, renderCAKeys(nil), 0o644); err != nil {
			return SSHAccessResult{Changes: files.changes}, err
		}
	}
	if err := files.writePrincipals(installed.principals, nil); err != nil {
		return SSHAccessResult{Changes: files.changes}, err
	}
	if installed.hasDropIn {
		if err := files.remove(SSHUserCADropIn); err != nil {
			return SSHAccessResult{Changes: files.changes}, err
		}
	}
	result := SSHAccessResult{Changes: files.changes}
	if !files.apply {
		if installed.hasKeys {
			result.Changes = append(result.Changes, "remove "+SSHTrustedUserCAKeysPath)
		}
		return result, nil
	}
	if installed.hasDropIn {
		if err := e.testSSHD(ctx); err != nil {
			// The restored drop-in names the emptied keys file, so no CA is trusted.
			_ = e.Host.WriteFile(SSHUserCADropIn, installed.dropIn, 0o600)
			result.CleanupPending = append(result.CleanupPending, "sshd rejected its configuration without the CA trust drop-in, so the drop-in was kept and names the emptied CA keys file: "+err.Error())
			return result, nil
		}
		if err := e.reloadSSHD(ctx); err != nil {
			result.CleanupPending = append(result.CleanupPending, "sshd could not be reloaded; the running daemon still names the emptied CA keys file: "+err.Error())
			return result, nil
		}
	}
	if installed.hasKeys {
		if err := files.remove(SSHTrustedUserCAKeysPath); err != nil {
			result.CleanupPending = append(result.CleanupPending, "the emptied CA keys file could not be removed: "+err.Error())
		}
	}
	result.Changes = files.changes
	return result, nil
}

// --- host certificates --------------------------------------------------------

// HostNames returns the names a host certificate for this server must carry:
// the explicit names, or the system hostname.
func (e Engine) HostNames(explicit []string) []string {
	if len(explicit) > 0 {
		return explicit
	}
	raw, err := e.Host.ReadFile("/etc/hostname")
	if err != nil {
		return nil
	}
	if name := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0]); name != "" {
		return []string{name}
	}
	return nil
}

func parseHostCertificate(line string) (*ssh.Certificate, string, error) {
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
	if err != nil || len(options) > 0 || len(bytes.TrimSpace(rest)) > 0 {
		return nil, "", errors.New("the host certificate is not a single OpenSSH certificate line")
	}
	cert, ok := key.(*ssh.Certificate)
	if !ok || cert.CertType != ssh.HostCert {
		return nil, "", errors.New("the host certificate is not an OpenSSH host certificate")
	}
	return cert, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(cert))), nil
}

// hostKeyFor finds the host key the certificate certifies among sshd's
// configured host keys.
func (e Engine) hostKeyFor(ctx context.Context, cert *ssh.Certificate) (string, error) {
	effective, failure := e.effectiveSSHD(ctx)
	if effective == nil {
		return "", fmt.Errorf("the configured host keys cannot be read: %s", failure)
	}
	want := cert.Key.Marshal()
	for _, path := range effective.values["hostkey"] {
		raw, err := e.Host.ReadFile(path + ".pub")
		if err != nil {
			continue
		}
		public, _, _, _, err := ssh.ParseAuthorizedKey(raw)
		if err == nil && bytes.Equal(public.Marshal(), want) {
			return path, nil
		}
	}
	return "", errors.New("the host certificate certifies a key this server does not hold as a host key")
}

// ValidateHostCertificate checks one host certificate for this server: an
// OpenSSH host certificate, signed, currently valid, with a finite lifetime,
// naming every host name as a principal and certifying one of sshd's
// configured host keys. It returns the canonical line and the host key path.
func (e Engine) ValidateHostCertificate(ctx context.Context, line string, hostNames []string) (string, string, error) {
	cert, canonical, err := parseHostCertificate(line)
	if err != nil {
		return "", "", err
	}
	if cert.ValidBefore == ssh.CertTimeInfinity {
		return "", "", errors.New("the host certificate never expires; a host certificate must have a finite lifetime")
	}
	names := e.HostNames(hostNames)
	if len(names) == 0 {
		return "", "", errors.New("this server's host names are unknown; name them explicitly")
	}
	checker := ssh.CertChecker{Clock: e.Host.Now}
	for _, name := range names {
		if !slices.Contains(cert.ValidPrincipals, name) {
			return "", "", fmt.Errorf("the host certificate does not name this server's host name %q", name)
		}
		if err := checker.CheckCert(name, cert); err != nil {
			return "", "", fmt.Errorf("the host certificate is not valid: %w", err)
		}
	}
	keyPath, err := e.hostKeyFor(ctx, cert)
	if err != nil {
		return "", "", err
	}
	return canonical, keyPath, nil
}

func hostCertificateDropInPath(raw []byte) string {
	for _, line := range managedLines(raw) {
		if path, ok := strings.CutPrefix(line, "HostCertificate "); ok {
			return strings.TrimSpace(path)
		}
	}
	return ""
}

// ApplySSHHostCertificate installs the host certificate beside the host key it
// certifies and points sshd at it. An empty line removes a StackKits-installed
// host certificate. A change sshd rejects or does not take is rolled back.
func (e Engine) ApplySSHHostCertificate(ctx context.Context, line string, hostNames []string) (SSHAccessResult, error) {
	return e.convergeHostCertificate(ctx, line, hostNames, true)
}

//nolint:gocyclo // One guarded sequence: validate, write, test, reload, read back, roll back.
func (e Engine) convergeHostCertificate(ctx context.Context, line string, hostNames []string, apply bool) (SSHAccessResult, error) {
	if apply && !e.isRoot() {
		return SSHAccessResult{}, blocked("%s", rootHint("change the sshd host certificate"))
	}
	previousDropIn, dropInErr := e.Host.ReadFile(SSHHostCertificateDropIn)
	hadDropIn := dropInErr == nil
	previousPath := ""
	if hadDropIn {
		previousPath = hostCertificateDropInPath(previousDropIn)
	}
	files := &sshFiles{engine: e, apply: apply}
	if line == "" {
		if !hadDropIn {
			return SSHAccessResult{}, nil
		}
		previousCert, previousCertErr := e.Host.ReadFile(previousPath)
		if err := files.remove(SSHHostCertificateDropIn); err != nil {
			return SSHAccessResult{Changes: files.changes}, err
		}
		if previousPath != "" {
			if err := files.remove(previousPath); err != nil {
				return SSHAccessResult{Changes: files.changes}, err
			}
		}
		if !apply {
			return SSHAccessResult{Changes: files.changes}, nil
		}
		if err := e.testSSHD(ctx); err != nil {
			if previousPath != "" && previousCertErr == nil {
				_ = e.Host.WriteFile(previousPath, previousCert, 0o644)
			}
			_ = e.Host.WriteFile(SSHHostCertificateDropIn, previousDropIn, 0o600)
			return SSHAccessResult{Changes: files.changes}, fmt.Errorf("%w; the previous host certificate configuration was restored", err)
		}
		return SSHAccessResult{Changes: files.changes}, e.reloadSSHD(ctx)
	}
	canonical, keyPath, err := e.ValidateHostCertificate(ctx, line, hostNames)
	if err != nil {
		return SSHAccessResult{}, blocked("%s", err.Error())
	}
	certPath := keyPath + "-cert.pub"
	previousCert, certErr := e.Host.ReadFile(certPath)
	hadCert := certErr == nil
	rollback := func() {
		if hadCert {
			_ = e.Host.WriteFile(certPath, previousCert, 0o644)
		} else {
			_ = e.Host.Remove(certPath)
		}
		if hadDropIn {
			_ = e.Host.WriteFile(SSHHostCertificateDropIn, previousDropIn, 0o600)
		} else {
			_ = e.Host.Remove(SSHHostCertificateDropIn)
		}
	}
	content := []byte(canonical + "\n")
	if !hadCert || !bytes.Equal(previousCert, content) {
		if err := files.write(certPath, content, 0o644); err != nil {
			rollback()
			return SSHAccessResult{Changes: files.changes}, err
		}
	}
	if !hadDropIn || !bytes.Equal(previousDropIn, renderHostCertificateDropIn(certPath)) {
		if err := files.write(SSHHostCertificateDropIn, renderHostCertificateDropIn(certPath), 0o600); err != nil {
			rollback()
			return SSHAccessResult{Changes: files.changes}, err
		}
	}
	result := SSHAccessResult{Changes: files.changes, HostCertificatePath: certPath}
	if !apply {
		return result, nil
	}
	if err := e.testSSHD(ctx); err != nil {
		rollback()
		return result, fmt.Errorf("%w; the previous host certificate configuration was restored", err)
	}
	if err := e.reloadSSHD(ctx); err != nil {
		rollback()
		_ = e.reloadSSHD(ctx)
		return result, fmt.Errorf("reload sshd: %w; the previous host certificate configuration was restored", err)
	}
	effective, failure := e.effectiveSSHD(ctx)
	if effective == nil || !slices.Contains(effective.values["hostcertificate"], certPath) {
		rollback()
		_ = e.reloadSSHD(ctx)
		if effective == nil {
			return result, fmt.Errorf("the new sshd configuration could not be read back (%s); the previous host certificate configuration was restored", failure)
		}
		return result, errors.New("sshd does not load the installed host certificate; the previous host certificate configuration was restored")
	}
	if previousPath != "" && previousPath != certPath {
		if err := e.Host.Remove(previousPath); err != nil {
			result.CleanupPending = append(result.CleanupPending, "the previous host certificate file could not be removed: "+err.Error())
		}
	}
	return result, nil
}

// --- observation ----------------------------------------------------------------

func (o *observation) sshAccess(ctx context.Context) []Control {
	state := o.options.SSHAccess
	if state == nil {
		return nil
	}
	var controls []Control
	if control, ok := o.sshUserCA(ctx, *state); ok {
		controls = append(controls, control)
	}
	if control, ok := o.sshHostCertificate(ctx, *state); ok {
		controls = append(controls, control)
	}
	return controls
}

//nolint:gocyclo // One linear judgment over each way the CA trust can differ.
func (o *observation) sshUserCA(ctx context.Context, state SSHAccessState) (Control, bool) {
	c := o.control(ControlSSHUserCA, "sshd trusts the owner's SSH user CA for exactly the delivered principals, and nothing once it is revoked",
		Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlSSHUserCA})
	installed, err := o.engine.readUserCA()
	if err != nil {
		return o.unknown(c, "the installed SSH CA trust cannot be read: "+err.Error()), true
	}
	if state.Failure != "" {
		// Without readable custody the control only reports StackKits files
		// that exist; a host that never received SSH access stays unchanged.
		if !installed.any() {
			return Control{}, false
		}
		return o.unknown(c, "the recorded SSH access cannot be read: "+state.Failure), true
	}
	keys, principals, normalizeErr := NormalizeSSHUserCATrust(state.Desired.CAKeys, state.Desired.Principals)
	if normalizeErr != nil {
		c.Remediation = Remediation{Capability: RemediationManual, Action: "deliver the SSH CA trust again (ssh-ca-trust)"}
		return o.finish(c, StateDrifted, "the recorded SSH CA trust is invalid", normalizeErr.Error()), true
	}
	if len(state.CleanupPending) > 0 {
		c.ReasonCode = ReasonSSHCleanupPending
		return o.finish(c, StateDrifted, "an SSH CA revocation is not fully cleaned up", strings.Join(state.CleanupPending, "; ")), true
	}
	if len(keys) == 0 {
		if installed.any() {
			return o.finish(c, StateDrifted, "StackKits SSH CA trust files are installed", "the SSH CA trust was revoked or never delivered, but its files remain"), true
		}
		// Removing our first-match drop-in can expose another CA trust source.
		// Check sshd's effective policy even when our files are already gone.
		effective, failure := o.sshdEffective(ctx)
		if effective == nil {
			if !state.Recorded {
				return Control{}, false
			}
			return o.unknown(c, failure), true
		}
		for _, setting := range []string{"trustedusercakeys", "authorizedprincipalsfile"} {
			if value := effective.first(setting); value != "" && value != "none" {
				c.Remediation = Remediation{Capability: RemediationManual, Action: "review and remove the sshd " + setting + " setting outside StackKits custody"}
				return o.finish(c, StateDrifted, setting+" "+value, "sshd retains SSH CA trust or principals outside StackKits custody after trust was revoked or never delivered"), true
			}
		}
		if !state.Recorded {
			return Control{}, false
		}
		return o.finish(c, StateCompliant, "no StackKits SSH CA trust is installed", ""), true
	}
	switch {
	case !slices.Equal(installed.keys, keys):
		return o.finish(c, StateDrifted, fmt.Sprintf("%d CA keys installed", len(installed.keys)), "the installed CA keys differ from the delivered trust"), true
	case !samePrincipals(installed.principals, principals):
		return o.finish(c, StateDrifted, "principals differ", "the installed principals differ from the delivered trust"), true
	case !installed.hasDropIn || !bytes.Equal(installed.dropIn, renderUserCADropIn()):
		return o.finish(c, StateDrifted, "the CA trust drop-in is missing or changed", "sshd is not pointed at the StackKits CA trust"), true
	}
	effective, failure := o.sshdEffective(ctx)
	if effective == nil {
		return o.unknown(c, failure), true
	}
	if effective.first("trustedusercakeys") != strings.ToLower(SSHTrustedUserCAKeysPath) || effective.first("authorizedprincipalsfile") != strings.ToLower(SSHPrincipalsPattern) {
		return o.finish(c, StateDrifted, "trustedusercakeys "+effective.first("trustedusercakeys"), "another sshd setting overrides the StackKits CA trust"), true
	}
	users := make([]string, 0, len(principals))
	for user := range principals {
		users = append(users, user)
	}
	sort.Strings(users)
	return o.finish(c, StateCompliant, fmt.Sprintf("%d CA key(s) trusted for %s", len(keys), strings.Join(users, ", ")), ""), true
}

//nolint:gocyclo // One linear judgment over each way the host certificate can be wrong.
func (o *observation) sshHostCertificate(ctx context.Context, state SSHAccessState) (Control, bool) {
	c := o.control(ControlSSHHostCertificate, "sshd presents a valid host certificate for this server's host key",
		Remediation{Capability: RemediationAutomatic, Action: "stackkit host security repair --apply --control " + ControlSSHHostCertificate})
	renew := Remediation{Capability: RemediationManual, Action: "deliver a renewed host certificate (ssh-host-certificate)"}
	dropIn, dropInErr := o.engine.Host.ReadFile(SSHHostCertificateDropIn)
	if state.Failure != "" {
		if dropInErr != nil {
			return Control{}, false
		}
		return o.unknown(c, "the recorded SSH access cannot be read: "+state.Failure), true
	}
	installedPath := ""
	if dropInErr == nil {
		installedPath = hostCertificateDropInPath(dropIn)
	}
	desired := state.Desired.HostCertificate
	if desired == "" {
		if dropInErr != nil {
			return Control{}, false
		}
		return o.finish(c, StateDrifted, "a StackKits host certificate is installed at "+installedPath, "custody holds no host certificate for this server"), true
	}
	cert, canonical, err := parseHostCertificate(desired)
	if err != nil {
		c.Remediation = renew
		return o.finish(c, StateDrifted, "the recorded host certificate is invalid", err.Error()), true
	}
	now := o.now()
	validAfter, validBefore := time.Unix(int64(cert.ValidAfter), 0).UTC(), time.Unix(int64(cert.ValidBefore), 0).UTC()
	switch {
	case !now.Before(validBefore):
		c.Remediation, c.ReasonCode = renew, ReasonHostCertificateExpired
		return o.finish(c, StateDrifted, "expired at "+validBefore.Format(time.RFC3339), "the host certificate expired"), true
	case now.Before(validAfter):
		c.Remediation = renew
		return o.finish(c, StateDrifted, "valid only from "+validAfter.Format(time.RFC3339), "the host certificate is not valid yet"), true
	}
	if installedPath == "" {
		return o.finish(c, StateDrifted, "no host certificate drop-in", "sshd is not pointed at the StackKits host certificate"), true
	}
	installed, readErr := o.engine.Host.ReadFile(installedPath)
	if readErr != nil || strings.TrimSpace(string(installed)) != canonical {
		return o.finish(c, StateDrifted, "the installed host certificate differs", "the file at "+installedPath+" is not the recorded host certificate"), true
	}
	if public, err := o.engine.Host.ReadFile(strings.TrimSuffix(installedPath, "-cert.pub") + ".pub"); err == nil {
		key, _, _, _, parseErr := ssh.ParseAuthorizedKey(public)
		if parseErr != nil || !bytes.Equal(key.Marshal(), cert.Key.Marshal()) {
			c.Remediation, c.ReasonCode = renew, ReasonHostCertificateNotForKey
			return o.finish(c, StateDrifted, "the host key changed", "the host certificate certifies a different key than the host key beside it"), true
		}
	}
	effective, failure := o.sshdEffective(ctx)
	if effective == nil {
		return o.unknown(c, failure), true
	}
	if !slices.Contains(effective.values["hostcertificate"], installedPath) {
		return o.finish(c, StateDrifted, "sshd does not load "+installedPath, "another sshd setting keeps the host certificate from loading"), true
	}
	observed := fmt.Sprintf("valid until %s for %s", validBefore.Format(time.RFC3339), strings.Join(cert.ValidPrincipals, ", "))
	if lifetime := validBefore.Sub(validAfter); validBefore.Sub(now) < lifetime/hostCertificateRenewShare {
		c.Remediation, c.ReasonCode = renew, ReasonHostCertificateExpiring
		return o.finish(c, StateDrifted, observed, "less than a fifth of the host certificate's lifetime remains"), true
	}
	return o.finish(c, StateCompliant, observed, ""), true
}

// --- repair -------------------------------------------------------------------

func (r *repairer) sshAccessDesired() (*SSHAccessState, error) {
	state := r.options.SSHAccess
	if state == nil {
		return nil, blocked("the recorded SSH access was not loaded, so there is nothing to reinstall from")
	}
	if state.Failure != "" {
		return nil, blocked("the recorded SSH access cannot be read: %s", state.Failure)
	}
	return state, nil
}

func (r *repairer) repairSSHUserCA(ctx context.Context) error {
	state, err := r.sshAccessDesired()
	if err != nil {
		return err
	}
	result, err := r.engine.convergeUserCA(ctx, state.Desired.CAKeys, state.Desired.Principals, r.apply)
	r.changes = append(r.changes, result.Changes...)
	if err != nil {
		return err
	}
	if len(result.CleanupPending) > 0 {
		return errors.New(strings.Join(result.CleanupPending, "; "))
	}
	if len(state.CleanupPending) > 0 {
		// What was pending is sshd reading the cleaned-up configuration.
		r.changes = append(r.changes, "run sshd -t", "reload sshd")
		if r.apply {
			if err := r.engine.testSSHD(ctx); err != nil {
				return err
			}
			return r.engine.reloadSSHD(ctx)
		}
	}
	return nil
}

func (r *repairer) repairSSHHostCertificate(ctx context.Context) error {
	state, err := r.sshAccessDesired()
	if err != nil {
		return err
	}
	result, err := r.engine.convergeHostCertificate(ctx, state.Desired.HostCertificate, state.HostNames, r.apply)
	r.changes = append(r.changes, result.Changes...)
	return err
}
