package hostsecurity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

const marketplaceSSHKeys = SSHAccessDir + "/marketplace_root_authorized_keys"
const marketplaceSSHMatch = SSHAccessDir + "/marketplace_root.conf"
const marketplaceSSHMain = "/etc/ssh/sshd_config"
const marketplaceSSHInclude = "\n# BEGIN StackKits Marketplace own-host SSH\nInclude " + marketplaceSSHMatch + "\n# END StackKits Marketplace own-host SSH\n"

// MarketplaceRootSSH grants the application's own key access only from its
// observed Docker interface. It leaves the owner's global SSH policy intact.
type MarketplaceRootSSH struct {
	OwnerRef  string
	Address   string
	PublicKey string
}

type MarketplaceRootSSHObservation struct {
	Address                string `json:"address"`
	Port                   int    `json:"port"`
	KeyFingerprint         string `json:"keyFingerprint"`
	OutsidePolicyPreserved bool   `json:"outsidePolicyPreserved"`
}

func marketplaceSSHHeader(owner string) (string, error) {
	if owner == "" || strings.ContainsAny(owner, "\r\n\x00") {
		return "", errors.New("Marketplace SSH requires its node custody owner")
	}
	hash := sha256.Sum256([]byte(owner))
	return managedSSHHeader + "\n# Marketplace owner sha256:" + hex.EncodeToString(hash[:]) + "\n", nil
}

// EnsureMarketplaceRootSSH uses the central host-security writer and validates
// sshd's effective connection policy before reloading. The dedicated key is
// never installed in the owner's ordinary authorized_keys file.
func (e Engine) EnsureMarketplaceRootSSH(ctx context.Context, request MarketplaceRootSSH) (MarketplaceRootSSHObservation, error) {
	if ctx == nil || e.Host == nil || !e.isRoot() {
		return MarketplaceRootSSHObservation{}, errors.New("Marketplace SSH requires the root node lifecycle owner")
	}
	address, err := netip.ParseAddr(request.Address)
	if err != nil || !address.Is4() || !address.IsPrivate() {
		return MarketplaceRootSSHObservation{}, errors.New("Marketplace SSH requires its exact observed private Docker IPv4 address")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(request.PublicKey))
	if err != nil || key.Type() != ssh.KeyAlgoED25519 || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return MarketplaceRootSSHObservation{}, errors.New("Marketplace SSH requires its single node-owned Ed25519 public key")
	}
	header, err := marketplaceSSHHeader(request.OwnerRef)
	if err != nil {
		return MarketplaceRootSSHObservation{}, err
	}
	main, err := e.Host.ReadFile(marketplaceSSHMain)
	if err != nil {
		return MarketplaceRootSSHObservation{}, err
	}
	mainInfo, err := e.Host.Stat(marketplaceSSHMain)
	if err != nil {
		return MarketplaceRootSSHObservation{}, err
	}
	if strings.Contains(string(main), marketplaceSSHMatch) && !strings.Contains(string(main), marketplaceSSHInclude) {
		return MarketplaceRootSSHObservation{}, errors.New("Marketplace SSH include differs from its owned configuration")
	}
	outside, err := e.marketplaceEffectiveSSH(ctx, "192.0.2.1", "root")
	if err != nil {
		return MarketplaceRootSSHObservation{}, err
	}
	paths := []string{marketplaceSSHKeys, marketplaceSSHMatch}
	previous := map[string][]byte{}
	for _, path := range paths {
		before, readErr := e.Host.ReadFile(path)
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return MarketplaceRootSSHObservation{}, readErr
		}
		if readErr == nil && !bytes.HasPrefix(before, []byte(header)) {
			return MarketplaceRootSSHObservation{}, errors.New("Marketplace SSH configuration belongs to another owner")
		}
		if readErr == nil {
			previous[path] = before
		}
	}
	rollback := func() {
		_ = e.Host.WriteFile(marketplaceSSHMain, main, mainInfo.Mode().Perm())
		for _, path := range paths {
			if raw, exists := previous[path]; exists {
				mode := os.FileMode(0600)
				if path == marketplaceSSHMatch {
					mode = 0644
				}
				_ = e.Host.WriteFile(path, raw, mode)
			} else {
				_ = e.Host.Remove(path)
			}
		}
	}
	line := fmt.Sprintf("from=\"%s/32\",no-agent-forwarding,no-port-forwarding,no-X11-forwarding,no-user-rc,no-pty %s", address, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
	profile := header + fmt.Sprintf("Match User root Address %s/32\n  PermitRootLogin prohibit-password\n  AuthenticationMethods publickey\n  PubkeyAuthentication yes\n  PasswordAuthentication no\n  KbdInteractiveAuthentication no\n  AuthorizedKeysFile %s\n  DisableForwarding yes\n  PermitTTY no\n", address, marketplaceSSHKeys)
	if err := e.Host.WriteFile(marketplaceSSHKeys, []byte(header+line+"\n"), 0600); err != nil {
		rollback()
		return MarketplaceRootSSHObservation{}, err
	}
	if err := e.Host.WriteFile(marketplaceSSHMatch, []byte(profile), 0644); err != nil {
		rollback()
		return MarketplaceRootSSHObservation{}, err
	}
	if !strings.Contains(string(main), marketplaceSSHInclude) {
		if err := e.Host.WriteFile(marketplaceSSHMain, append(append([]byte{}, main...), []byte(marketplaceSSHInclude)...), mainInfo.Mode().Perm()); err != nil {
			rollback()
			return MarketplaceRootSSHObservation{}, err
		}
	}
	if err := e.testSSHD(ctx); err != nil {
		rollback()
		return MarketplaceRootSSHObservation{}, err
	}
	inside, err := e.marketplaceEffectiveSSH(ctx, address.String(), "root")
	if err != nil {
		rollback()
		return MarketplaceRootSSHObservation{}, err
	}
	rootLogin := inside.first("permitrootlogin")
	ports := inside.ports()
	if len(ports) != 1 || (rootLogin != "prohibit-password" && rootLogin != "without-password") || inside.first("authorizedkeysfile") != marketplaceSSHKeys || inside.first("authenticationmethods") != "publickey" || inside.first("passwordauthentication") != "no" || inside.first("kbdinteractiveauthentication") != "no" || inside.first("disableforwarding") != "yes" {
		rollback()
		return MarketplaceRootSSHObservation{}, errors.New("Marketplace SSH exception did not establish its exact key-only Docker policy")
	}
	after, err := e.marketplaceEffectiveSSH(ctx, "192.0.2.1", "root")
	if err != nil || !sameSSHEffectivePolicy(outside, after) || strings.Contains(after.first("authorizedkeysfile"), marketplaceSSHKeys) {
		rollback()
		return MarketplaceRootSSHObservation{}, errors.New("Marketplace SSH changed the owner's outside policy")
	}
	if err := e.reloadSSHD(ctx); err != nil {
		rollback()
		_ = e.reloadSSHD(ctx)
		return MarketplaceRootSSHObservation{}, err
	}
	return MarketplaceRootSSHObservation{Address: address.String(), Port: ports[0], KeyFingerprint: ssh.FingerprintSHA256(key), OutsidePolicyPreserved: true}, nil
}

func (e Engine) marketplaceEffectiveSSH(ctx context.Context, address, user string) (sshdObservation, error) {
	output, err := e.run(ctx, sshdBinary, "-T", "-C", "user="+user+",host=stackkit-own-host,addr="+address)
	if err != nil || output.ExitCode != 0 {
		return sshdObservation{}, errors.New("Marketplace SSH effective policy could not be read")
	}
	return parseSSHD(output.Stdout)
}

func sameSSHEffectivePolicy(a, b sshdObservation) bool {
	if len(a.values) != len(b.values) {
		return false
	}
	for name, values := range a.values {
		if strings.Join(values, "\n") != strings.Join(b.values[name], "\n") {
			return false
		}
	}
	return true
}

// RemoveMarketplaceRootSSH revokes only this installation's exception and key.
func (e Engine) RemoveMarketplaceRootSSH(ctx context.Context, owner string) error {
	if ctx == nil || e.Host == nil || !e.isRoot() {
		return errors.New("Marketplace SSH revocation requires its root node owner")
	}
	header, err := marketplaceSSHHeader(owner)
	if err != nil {
		return err
	}
	for _, path := range []string{marketplaceSSHKeys, marketplaceSSHMatch} {
		raw, err := e.Host.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || !bytes.HasPrefix(raw, []byte(header)) {
			return errors.New("Marketplace SSH revocation refuses another owner's files")
		}
	}
	main, err := e.Host.ReadFile(marketplaceSSHMain)
	if err != nil {
		return err
	}
	info, err := e.Host.Stat(marketplaceSSHMain)
	if err != nil {
		return err
	}
	// Emptying the key file revokes access even if a later reload fails.
	if err := e.Host.WriteFile(marketplaceSSHKeys, []byte(header), 0600); err != nil {
		return err
	}
	if err := e.Host.WriteFile(marketplaceSSHMain, []byte(strings.ReplaceAll(string(main), marketplaceSSHInclude, "")), info.Mode().Perm()); err != nil {
		return err
	}
	if err := e.Host.Remove(marketplaceSSHMatch); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := e.testSSHD(ctx); err != nil {
		return err
	}
	return e.reloadSSHD(ctx)
}
