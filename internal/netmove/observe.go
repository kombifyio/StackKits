// Package netmove holds the host-side logic for moving a standalone StackKits
// server to another network: observing the current address and wireless link,
// deciding whether the recorded site address is stale, pre-staging destination
// WiFi, and keeping a watcher installed that re-binds the stack by itself.
//
// Everything that touches the host goes through Sys so the decisions stay
// unit-testable with fakes.
package netmove

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/siteaddress"
)

const (
	sysClassNet    = "/sys/class/net"
	procNetRoute   = "/proc/net/route"
	observeTimeout = 5 * time.Second

	// VerdictInPlace means every recorded address equals the current one.
	VerdictInPlace = "in-place"
	// VerdictMoved means a recorded address differs from the current one.
	VerdictMoved = "moved"
	// VerdictUnknown means there is no current or no recorded address to compare.
	VerdictUnknown = "unknown"
)

// Sys is the narrow host boundary used by this package.
type Sys struct {
	Discover func() (netip.Addr, error)
	ReadFile func(string) ([]byte, error)
	Exists   func(string) bool
	Glob     func(string) ([]string, error)
	LookPath func(string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// DefaultSys binds Sys to the running host.
func DefaultSys() Sys {
	return Sys{
		Discover: siteaddress.DiscoverSiteAddress,
		ReadFile: os.ReadFile,
		Exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		Glob:     filepath.Glob,
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // fixed argv, no shell
		},
	}
}

// Observation is what the host reports about its current network position.
type Observation struct {
	Address   string `json:"address,omitempty"`
	Interface string `json:"interface,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
	Wireless  bool   `json:"wireless"`
	SSID      string `json:"ssid,omitempty"`
}

// Observe reads the current site address, default route and wireless link.
// Each fact is independent: a missing one stays empty.
func Observe(ctx context.Context, sys Sys) Observation {
	var observed Observation
	if sys.Discover != nil {
		if address, err := sys.Discover(); err == nil && address.IsValid() {
			observed.Address = address.String()
		}
	}
	if sys.ReadFile != nil {
		if raw, err := sys.ReadFile(procNetRoute); err == nil {
			observed.Interface, observed.Gateway = ParseDefaultRoute(raw)
		}
	}
	if observed.Interface != "" && sys.Exists != nil {
		observed.Wireless = sys.Exists(sysClassNet + "/" + observed.Interface + "/wireless")
	}
	if observed.Wireless {
		observed.SSID = currentSSID(ctx, sys, observed.Interface)
	}
	return observed
}

// ParseDefaultRoute returns the interface and gateway of the lowest-metric
// default IPv4 route in the /proc/net/route format.
func ParseDefaultRoute(raw []byte) (iface, gateway string) {
	best := -1
	for i, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) < 8 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil || flags&0x2 == 0 { // RTF_GATEWAY
			continue
		}
		metric, err := strconv.Atoi(fields[6])
		if err != nil || (best >= 0 && metric >= best) {
			continue
		}
		word, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			continue
		}
		var octets [4]byte
		binary.LittleEndian.PutUint32(octets[:], uint32(word))
		best, iface, gateway = metric, fields[0], netip.AddrFrom4(octets).String()
	}
	return iface, gateway
}

func currentSSID(ctx context.Context, sys Sys, iface string) string {
	if sys.Run == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, observeTimeout)
	defer cancel()
	if sys.LookPath != nil {
		if _, err := sys.LookPath("nmcli"); err == nil {
			if out, err := sys.Run(ctx, "nmcli", "-t", "-f", "active,ssid", "dev", "wifi"); err == nil {
				if ssid := ParseNMCLIActiveSSID(out); ssid != "" {
					return ssid
				}
			}
		}
		if _, err := sys.LookPath("iw"); err == nil {
			if out, err := sys.Run(ctx, "iw", "dev", iface, "link"); err == nil {
				return ParseIWLinkSSID(out)
			}
		}
	}
	return ""
}

// ParseNMCLIActiveSSID returns the SSID of the active row of
// `nmcli -t -f active,ssid dev wifi` (terse mode escapes ':' as '\:').
func ParseNMCLIActiveSSID(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "yes:"); ok {
			return strings.NewReplacer(`\:`, ":", `\\`, `\`).Replace(rest)
		}
	}
	return ""
}

// ParseIWLinkSSID returns the SSID line of `iw dev <if> link`.
func ParseIWLinkSSID(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "SSID:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// WirelessInterfaces lists the host's wireless interface names.
func WirelessInterfaces(sys Sys) []string {
	if sys.Glob == nil {
		return nil
	}
	matches, err := sys.Glob(sysClassNet + "/*/wireless")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, filepath.Base(filepath.Dir(match)))
	}
	return names
}

// Verdict compares the current address with every recorded one. Empty
// recorded values are ignored. It is "moved" as soon as one recorded address
// differs, because every recorded copy must be re-issued.
func Verdict(current string, recorded ...string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return VerdictUnknown
	}
	currentAddr, err := netip.ParseAddr(current)
	// A link-local address means DHCP has not answered yet, not that the
	// server moved; acting on it would re-issue custody for a transient value.
	if err != nil || currentAddr.IsLinkLocalUnicast() {
		return VerdictUnknown
	}
	seen := false
	for _, value := range recorded {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		seen = true
		address, err := netip.ParseAddr(value)
		if err != nil || address.Unmap() != currentAddr.Unmap() {
			return VerdictMoved
		}
	}
	if !seen {
		return VerdictUnknown
	}
	return VerdictInPlace
}

// ErrNoWirelessInterface reports that WiFi cannot be staged on this host.
var ErrNoWirelessInterface = errors.New("netmove: this host has no wireless interface; use --force to stage the network anyway")
