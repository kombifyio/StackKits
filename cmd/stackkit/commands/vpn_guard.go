package commands

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The guard confines the VPN add-on (#VPNAddOnV1 routing): traffic that
// arrives through the tool may reach only the node site address (its
// published services included, which the container engine translates) and
// the tool's own node address; no other LAN host, no other peer, no
// forwarding. For a coordinated tool only the addresses of bound peers of
// active devices get past the peer service, so a node the registry does not
// hold reaches nothing but the probe. Newt's own bridge may reach the site
// address, its Pangolin server and the internet, never another private
// address.
//
// The guard is one nftables table, replaced atomically on every reconcile,
// and loaded at boot by stackkit-vpn-guard.service before the container
// engine and wg-quick start, so it is in force before the tool's interface
// carries a packet.
const (
	vpnGuardTable = "stackkit_vpn_guard"
	vpnGuardFile  = "/etc/stackkit/vpn-guard.nft"
	vpnGuardUnit  = "stackkit-vpn-guard.service"
	// vpnNewtBridge is Newt's own Docker bridge; the host firewall trusts
	// br-* and this guard confines it.
	vpnNewtBridge = "br-stackkit-vpn"
)

var vpnPrivateDestinations = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/3",
}

type vpnGuardSpec struct {
	iface string
	// local are further local destinations besides the site address.
	local []string
	// sources, when restricted, are the only sources that pass beyond the
	// peer service port.
	restricted bool
	sources    []string
	// bridge marks a container bridge whose tool dials out; egress are the
	// private destinations it may reach besides the site address.
	bridge bool
	egress []string
}

func vpnSiteAddress(root string) (netip.Addr, error) {
	raw, err := localevidence.BasementLANDNSResolverAddress(root)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("VPN add-on: no node site address in custody for the guard: %w", err)
	}
	address, err := netip.ParseAddr(raw)
	if err != nil || !address.Is4() {
		return netip.Addr{}, errors.New("VPN add-on: the guard needs an IPv4 node site address")
	}
	return address, nil
}

func vpnAddressSet(values []string) (string, error) {
	for _, value := range values {
		if _, err := netip.ParseAddr(value); err != nil {
			if _, prefixErr := netip.ParsePrefix(value); prefixErr != nil {
				return "", fmt.Errorf("VPN add-on: %q is not an address", value)
			}
		}
	}
	return "{ " + strings.Join(values, ", ") + " }", nil
}

func renderVPNGuard(site netip.Addr, spec vpnGuardSpec) (string, error) {
	in := `iifname "` + spec.iface + `"`
	local, err := vpnAddressSet(append([]string{site.String()}, spec.local...))
	if err != nil {
		return "", err
	}
	input := []string{in + " ct state established,related accept"}
	forward := []string{}
	switch {
	case spec.restricted:
		if len(spec.sources) > 0 {
			sources, err := vpnAddressSet(spec.sources)
			if err != nil {
				return "", err
			}
			input = append(input, in+" ip saddr "+sources+" ip daddr "+local+" accept")
			forward = append(forward, in+" ip saddr "+sources+" ct status dnat ct original ip daddr "+site.String()+" accept")
		}
		input = append(input, in+" ip daddr "+site.String()+" tcp dport "+strconv.Itoa(identity.HomeVPNPeerServicePort)+" accept")
	default:
		input = append(input, in+" ip daddr "+local+" accept")
		forward = append(forward, in+" ct status dnat ct original ip daddr "+site.String()+" accept")
	}
	input = append(input, in+" drop")
	if spec.bridge {
		if len(spec.egress) > 0 {
			egress, err := vpnAddressSet(spec.egress)
			if err != nil {
				return "", err
			}
			forward = append(forward, in+" ip daddr "+egress+" accept")
		}
		private, _ := vpnAddressSet(vpnPrivateDestinations)
		forward = append(forward, in+" ip daddr "+private+" drop", in+" meta nfproto ipv6 drop")
	} else {
		out := `oifname "` + spec.iface + `"`
		forward = append(forward, out+" ct state established,related accept", in+" drop", out+" drop")
	}
	var text strings.Builder
	text.WriteString("table inet " + vpnGuardTable + " {}\ndelete table inet " + vpnGuardTable + "\ntable inet " + vpnGuardTable + " {\n")
	text.WriteString("\tchain input {\n\t\ttype filter hook input priority filter - 5; policy accept;\n")
	for _, rule := range input {
		text.WriteString("\t\t" + rule + "\n")
	}
	text.WriteString("\t}\n\tchain forward {\n\t\ttype filter hook forward priority filter - 5; policy accept;\n")
	for _, rule := range forward {
		text.WriteString("\t\t" + rule + "\n")
	}
	text.WriteString("\t}\n}\n")
	return text.String(), nil
}

// applyVPNGuard renders, loads and persists the guard and its boot unit.
func applyVPNGuard(ctx context.Context, root string, spec vpnGuardSpec) error {
	nft, err := homeOverlayLookPath("nft")
	if err != nil {
		return errors.New("VPN add-on: nft is not installed; install nftables (apt install nftables), which confines the add-on to the site address")
	}
	site, err := vpnSiteAddress(root)
	if err != nil {
		return err
	}
	rules, err := renderVPNGuard(site, spec)
	if err != nil {
		return err
	}
	if current, readErr := os.ReadFile(vpnGuardFile); readErr != nil || string(current) != rules {
		if err := writeVPNPrivateFile(vpnGuardFile, []byte(rules)); err != nil {
			return fmt.Errorf("VPN add-on: write the guard: %w", err)
		}
	}
	if _, err := homeOverlayRun(ctx, "nft", "-f", vpnGuardFile); err != nil {
		return fmt.Errorf("VPN add-on: load the guard: %w", err)
	}
	unit := "[Unit]\nDescription=StackKits VPN add-on guard (before the container engine and the VPN interfaces)\n" +
		"DefaultDependencies=no\nAfter=local-fs.target\nBefore=network-pre.target docker.service wg-quick@" + identity.HomeOverlayInterfaceName + ".service " + vpnServiceUnit + "\n" +
		"Wants=network-pre.target\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=" + nft + " -f " + vpnGuardFile + "\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
	path := filepath.Join(vpnSystemdDir, vpnGuardUnit)
	if current, readErr := os.ReadFile(path); readErr != nil || string(current) != unit {
		if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
			return fmt.Errorf("VPN add-on: write %s: %w", path, err)
		}
		if _, err := homeOverlayRun(ctx, "systemctl", "daemon-reload"); err != nil {
			return err
		}
		if _, err := homeOverlayRun(ctx, "systemctl", "enable", vpnGuardUnit); err != nil {
			return err
		}
	}
	return nil
}

// removeVPNGuard drops the guard table, its file and its boot unit.
func removeVPNGuard(ctx context.Context) error {
	var failures []string
	if _, err := homeOverlayLookPath("nft"); err == nil {
		if _, err := homeOverlayRunInput(ctx, []byte("table inet "+vpnGuardTable+" {}\ndelete table inet "+vpnGuardTable+"\n"), "nft", "-f", "-"); err != nil {
			failures = append(failures, err.Error())
		}
	}
	path := filepath.Join(vpnSystemdDir, vpnGuardUnit)
	if _, err := os.Stat(path); err == nil {
		if _, err := homeOverlayRun(ctx, "systemctl", "disable", vpnGuardUnit); err != nil {
			failures = append(failures, err.Error())
		}
	}
	for _, file := range []string{path, vpnGuardFile} {
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New("VPN add-on: the guard was not fully removed: " + strings.Join(failures, "; "))
	}
	return nil
}
