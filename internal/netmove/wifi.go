package netmove

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	// NetplanFile is the only netplan file this package writes.
	NetplanFile = "/etc/netplan/90-kombify-wifi.yaml"
	// NMConnectionDir holds NetworkManager keyfile connections.
	NMConnectionDir = "/etc/NetworkManager/system-connections"

	netplanDeviceKey = "kombify-wifi"
	nmFilePrefix     = "kombify-wifi-"
	nmFileSuffix     = ".nmconnection"
)

// Network is one staged WiFi network. The password is never part of it.
type Network struct {
	SSID     string `json:"ssid"`
	Priority int    `json:"priority,omitempty"`
	Backend  string `json:"backend"`
}

// Credential is a network to stage.
type Credential struct {
	SSID     string
	Password string // empty means an open network
	Priority int
}

// Validate checks the SSID and WPA passphrase shapes.
func (c Credential) Validate() error {
	if c.SSID == "" || len(c.SSID) > 32 {
		return errors.New("netmove: the SSID must be 1 to 32 bytes")
	}
	for _, r := range c.SSID + c.Password {
		if unicode.IsControl(r) {
			return errors.New("netmove: the SSID and password must not contain control characters")
		}
	}
	if c.Password != "" {
		hexKey := len(c.Password) == 64 && strings.Trim(strings.ToLower(c.Password), "0123456789abcdef") == ""
		if !hexKey && (len(c.Password) < 8 || len(c.Password) > 63) {
			return errors.New("netmove: a WPA passphrase must be 8 to 63 characters")
		}
	}
	return nil
}

// Backend stages WiFi networks through one host network manager.
type Backend interface {
	Name() string
	Add(ctx context.Context, credential Credential, applyNow bool) error
	List() ([]Network, error)
	Remove(ctx context.Context, ssid string, applyNow bool) (bool, error)
}

// ErrNoBackend reports that neither NetworkManager nor netplan can stage WiFi.
var ErrNoBackend = errors.New("netmove: neither a running NetworkManager nor netplan was found; install one of them or configure the WiFi through the distribution's own tooling")

// DetectBackend prefers a running NetworkManager and falls back to netplan.
func DetectBackend(ctx context.Context, sys Sys) (Backend, error) {
	if sys.LookPath != nil && sys.Run != nil {
		if _, err := sys.LookPath("nmcli"); err == nil {
			out, runErr := sys.Run(ctx, "nmcli", "-t", "-f", "RUNNING", "general")
			if runErr == nil && strings.TrimSpace(string(out)) == "running" {
				return &NMBackend{Dir: NMConnectionDir, Sys: sys}, nil
			}
		}
		if _, err := sys.LookPath("netplan"); err == nil {
			return &NetplanBackend{Path: NetplanFile, Sys: sys}, nil
		}
	}
	return nil, ErrNoBackend
}

// ---- NetworkManager (keyfile) ----

// NMBackend writes keyfile connections and asks NetworkManager to reload them.
// A keyfile keeps the passphrase out of any process argument list.
type NMBackend struct {
	Dir string
	Sys Sys
}

func (b *NMBackend) Name() string { return "networkmanager" }

func nmFileName(ssid string) string {
	sum := sha256.Sum256([]byte(ssid))
	var safe strings.Builder
	for _, r := range ssid {
		if len(safe.String()) >= 24 {
			break
		}
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			safe.WriteRune(r)
		}
	}
	return fmt.Sprintf("%s%s-%x%s", nmFilePrefix, safe.String(), sum[:4], nmFileSuffix)
}

func nmEscape(value string) string {
	return strings.NewReplacer(`\`, `\\`, `;`, `\;`).Replace(value)
}

func nmUnescape(value string) string {
	return strings.NewReplacer(`\;`, `;`, `\\`, `\`).Replace(value)
}

// NMKeyfile renders the NetworkManager connection for a staged network.
// DHCP is requested for both families and the connection autoconnects on any
// wireless device, so it only ever takes effect where its SSID is in range.
func NMKeyfile(credential Credential) []byte {
	sum := sha256.Sum256([]byte("kombify-wifi:" + credential.SSID))
	uuid := fmt.Sprintf("%x-%x-5%03x-a%03x-%x", sum[0:4], sum[4:6],
		binary.BigEndian.Uint16(sum[6:8])&0x0fff, binary.BigEndian.Uint16(sum[8:10])&0x0fff, sum[10:16])
	var out strings.Builder
	out.WriteString("# Managed by `stackkit network wifi`. Remove with `stackkit network wifi remove`.\n")
	out.WriteString("[connection]\n")
	fmt.Fprintf(&out, "id=%s%s\nuuid=%s\ntype=wifi\nautoconnect=true\nautoconnect-priority=%d\n\n", nmFilePrefix, nmEscape(credential.SSID), uuid, credential.Priority)
	fmt.Fprintf(&out, "[wifi]\nmode=infrastructure\nssid=%s\n\n", nmEscape(credential.SSID))
	if credential.Password != "" {
		fmt.Fprintf(&out, "[wifi-security]\nkey-mgmt=wpa-psk\npsk=%s\n\n", strings.ReplaceAll(credential.Password, `\`, `\\`))
	}
	out.WriteString("[ipv4]\nmethod=auto\n\n[ipv6]\naddr-gen-mode=default\nmethod=auto\n")
	return []byte(out.String())
}

func (b *NMBackend) Add(ctx context.Context, credential Credential, _ bool) error {
	if err := credential.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return fmt.Errorf("netmove: create %s: %w (staging WiFi needs root)", b.Dir, err)
	}
	if err := writeAtomic(filepath.Join(b.Dir, nmFileName(credential.SSID)), NMKeyfile(credential)); err != nil {
		return fmt.Errorf("netmove: write the NetworkManager connection: %w (staging WiFi needs root)", err)
	}
	return b.reload(ctx)
}

func (b *NMBackend) reload(ctx context.Context) error {
	if out, err := b.Sys.Run(ctx, "nmcli", "connection", "reload"); err != nil {
		return fmt.Errorf("netmove: nmcli connection reload: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (b *NMBackend) List() ([]Network, error) {
	entries, err := os.ReadDir(b.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var networks []Network
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), nmFilePrefix) || !strings.HasSuffix(entry.Name(), nmFileSuffix) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(b.Dir, entry.Name())) //nolint:gosec // fixed system directory
		if err != nil {
			continue
		}
		network := Network{Backend: b.Name()}
		inWifi := false
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "["):
				inWifi = line == "[wifi]"
			case inWifi && strings.HasPrefix(line, "ssid="):
				network.SSID = nmUnescape(strings.TrimPrefix(line, "ssid="))
			case strings.HasPrefix(line, "autoconnect-priority="):
				network.Priority, _ = strconv.Atoi(strings.TrimPrefix(line, "autoconnect-priority="))
			}
		}
		if network.SSID != "" {
			networks = append(networks, network)
		}
	}
	sort.Slice(networks, func(i, j int) bool { return networks[i].SSID < networks[j].SSID })
	return networks, nil
}

func (b *NMBackend) Remove(ctx context.Context, ssid string, _ bool) (bool, error) {
	err := os.Remove(filepath.Join(b.Dir, nmFileName(ssid)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, b.reload(ctx)
}

// ---- netplan ----

// NetplanBackend owns one netplan file with a single wireless device that
// matches every wl* interface. It never writes an ethernet definition, and it
// sets the networkd renderer on that device only, so the global renderer and
// the host's existing wired DHCP stay as they are.
type NetplanBackend struct {
	Path string
	Sys  Sys
}

func (b *NetplanBackend) Name() string { return "netplan" }

// NetplanAdd merges one access point into an existing netplan document
// (nil for a new file) and keeps every other key, including ethernets.
func NetplanAdd(existing []byte, credential Credential) ([]byte, error) {
	if err := credential.Validate(); err != nil {
		return nil, err
	}
	document, err := decodeNetplan(existing)
	if err != nil {
		return nil, err
	}
	network := childMap(document, "network")
	network["version"] = 2
	device := childMap(childMap(network, "wifis"), netplanDeviceKey)
	device["renderer"] = "networkd"
	device["match"] = map[string]any{"name": "wl*"}
	device["dhcp4"] = true
	device["optional"] = true
	accessPoint := map[string]any{}
	if credential.Password != "" {
		accessPoint["password"] = credential.Password
	}
	childMap(device, "access-points")[credential.SSID] = accessPoint
	return yaml.Marshal(document)
}

// NetplanRemove drops one access point. out is nil when nothing but the
// version header remains and the file can be deleted.
func NetplanRemove(existing []byte, ssid string) (out []byte, found bool, err error) {
	document, err := decodeNetplan(existing)
	if err != nil {
		return nil, false, err
	}
	network := childMap(document, "network")
	wifis := childMap(network, "wifis")
	device := childMap(wifis, netplanDeviceKey)
	accessPoints := childMap(device, "access-points")
	if _, found = accessPoints[ssid]; !found {
		return existing, false, nil
	}
	delete(accessPoints, ssid)
	if len(accessPoints) == 0 {
		delete(wifis, netplanDeviceKey)
	}
	if len(wifis) == 0 {
		delete(network, "wifis")
	}
	delete(network, "version")
	if len(network) == 0 {
		return nil, true, nil
	}
	network["version"] = 2
	out, err = yaml.Marshal(document)
	return out, true, err
}

// NetplanList returns the staged SSIDs of a netplan document.
func NetplanList(existing []byte) ([]string, error) {
	document, err := decodeNetplan(existing)
	if err != nil {
		return nil, err
	}
	accessPoints := childMap(childMap(childMap(childMap(document, "network"), "wifis"), netplanDeviceKey), "access-points")
	ssids := make([]string, 0, len(accessPoints))
	for ssid := range accessPoints {
		ssids = append(ssids, ssid)
	}
	sort.Strings(ssids)
	return ssids, nil
}

func decodeNetplan(raw []byte) (map[string]any, error) {
	document := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return document, nil
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("netmove: existing netplan file is not valid YAML: %w", err)
	}
	if document == nil {
		document = map[string]any{}
	}
	return document, nil
}

// childMap returns parent[key] as a map, creating or normalising it in place.
func childMap(parent map[string]any, key string) map[string]any {
	switch value := parent[key].(type) {
	case map[string]any:
		return value
	case map[any]any:
		converted := make(map[string]any, len(value))
		for k, v := range value {
			converted[fmt.Sprint(k)] = v
		}
		parent[key] = converted
		return converted
	}
	created := map[string]any{}
	parent[key] = created
	return created
}

func (b *NetplanBackend) read() ([]byte, error) {
	raw, err := os.ReadFile(b.Path) //nolint:gosec // fixed system path
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

// write installs the new document, validates it with `netplan generate` and
// restores the previous content when that fails, so a bad file never lingers.
func (b *NetplanBackend) write(ctx context.Context, previous, next []byte, applyNow bool) error {
	if next == nil {
		if err := os.Remove(b.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if err := writeAtomic(b.Path, next); err != nil {
		return fmt.Errorf("netmove: write %s: %w (staging WiFi needs root)", b.Path, err)
	}
	if out, err := b.Sys.Run(ctx, "netplan", "generate"); err != nil {
		if previous == nil {
			_ = os.Remove(b.Path)
		} else {
			_ = writeAtomic(b.Path, previous)
		}
		return fmt.Errorf("netmove: netplan generate rejected the change and it was rolled back: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if applyNow {
		if out, err := b.Sys.Run(ctx, "netplan", "apply"); err != nil {
			return fmt.Errorf("netmove: netplan apply: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func (b *NetplanBackend) Add(ctx context.Context, credential Credential, applyNow bool) error {
	previous, err := b.read()
	if err != nil {
		return err
	}
	next, err := NetplanAdd(previous, credential)
	if err != nil {
		return err
	}
	return b.write(ctx, previous, next, applyNow)
}

func (b *NetplanBackend) List() ([]Network, error) {
	raw, err := b.read()
	if err != nil {
		return nil, err
	}
	ssids, err := NetplanList(raw)
	if err != nil {
		return nil, err
	}
	networks := make([]Network, 0, len(ssids))
	for _, ssid := range ssids {
		networks = append(networks, Network{SSID: ssid, Backend: b.Name()})
	}
	return networks, nil
}

func (b *NetplanBackend) Remove(ctx context.Context, ssid string, applyNow bool) (bool, error) {
	previous, err := b.read()
	if err != nil || previous == nil {
		return false, err
	}
	next, found, err := NetplanRemove(previous, ssid)
	if err != nil || !found {
		return false, err
	}
	return true, b.write(ctx, previous, next, applyNow)
}

// writeAtomic replaces path with 0600 content through a temporary file.
func writeAtomic(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kombify-wifi-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
