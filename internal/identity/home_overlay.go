package identity

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// This is the WireGuard tool of the optional VPN add-on (#VPNAddOnV1 in
// foundation/network.cue, D-2026-11-02-12). A VPN is never the default and
// never the realization of private remote access; the Owner switches it on in
// addition to the standard reach paths. Its peers are exactly the enrolled
// devices that registered a WireGuard public key and are neither revoked nor
// expired. The node private key is generated here into local custody and
// never leaves it: the rendered interface configuration loads it from the
// custody file at interface start.
const (
	HomeOverlayInterfaceName       = "wg-stackkit"
	HomeOverlayListenPort          = 51820
	HomeOverlayKeepaliveSeconds    = 25
	homeOverlayNodePrivateKeyPath  = ".stackkit/custody/home-overlay/node.key"
	homeOverlayPeerRegistryPath    = ".stackkit/custody/home-overlay/peers.json"
	homeOverlayPeerRegistryMaxSize = 256 << 10
	homeOverlayPeerRegistrySchema  = "stackkit.home-overlay-peers/v1"
)

var (
	homeOverlayPool        = netip.MustParsePrefix("10.8.0.0/24")
	homeOverlayNodeAddress = netip.MustParseAddr("10.8.0.1")

	ErrHomeOverlayDeviceInactive = errors.New("identity: only an enrolled, unrevoked, unexpired device can join the overlay")
	ErrHomeOverlayKeyInvalid     = errors.New("identity: overlay public key must be a base64 32-byte WireGuard key")
	ErrHomeOverlayKeyInUse       = errors.New("identity: another device already registered this overlay key")
	ErrHomeOverlayPoolExhausted  = errors.New("identity: the overlay address pool has no free address")
	// ErrHomeOverlayCustodyAbsent: this workspace holds no VPN add-on custody
	// (the core's identity workspace, or a host without the add-on).
	ErrHomeOverlayCustodyAbsent = errors.New("identity: no VPN add-on custody in this workspace")
)

// HomeOverlayPeer is the public peer record of one enrolled device. It holds
// only the device's WireGuard public key and its overlay address.
type HomeOverlayPeer struct {
	DeviceID  string `json:"deviceId"`
	PublicKey string `json:"publicKey"`
	Address   string `json:"address"`
}

// HomeOverlayNode is what a device needs to configure its side of the tunnel.
// The endpoint stays owner-declared and is not part of this record.
type HomeOverlayNode struct {
	InterfaceName       string `json:"interfaceName"`
	PublicKey           string `json:"publicKey"`
	Address             string `json:"address"`
	ListenPort          int    `json:"listenPort"`
	KeepaliveSeconds    int    `json:"persistentKeepaliveSeconds"`
	PrivateKeyInCustody bool   `json:"privateKeyInLocalCustody"`
}

type homeOverlayPeerRegistry struct {
	Schema string            `json:"schema"`
	Peers  []HomeOverlayPeer `json:"peers"`
}

// EnsureHomeOverlayNodeKey generates the node's WireGuard private key into
// local custody on first use and returns the node's public overlay identity.
// The private key is never returned.
func EnsureHomeOverlayNodeKey(root string) (HomeOverlayNode, error) {
	private, err := loadHomeOverlayNodeKey(root)
	if errors.Is(err, fs.ErrNotExist) {
		generated, genErr := ecdh.X25519().GenerateKey(rand.Reader)
		if genErr != nil {
			return HomeOverlayNode{}, genErr
		}
		encoded := []byte(base64.StdEncoding.EncodeToString(generated.Bytes()) + "\n")
		// No-replace: a key that appeared concurrently wins and is read back.
		writeErr := writeHomePrivate(root, homeOverlayNodePrivateKeyPath, encoded, true)
		if private, err = loadHomeOverlayNodeKey(root); err != nil && writeErr != nil {
			return HomeOverlayNode{}, writeErr
		}
	}
	if err != nil {
		return HomeOverlayNode{}, err
	}
	return HomeOverlayNode{
		InterfaceName: HomeOverlayInterfaceName, PublicKey: base64.StdEncoding.EncodeToString(private.PublicKey().Bytes()),
		Address: homeOverlayNodeAddress.String(), ListenPort: HomeOverlayListenPort,
		KeepaliveSeconds: HomeOverlayKeepaliveSeconds, PrivateKeyInCustody: true,
	}, nil
}

// BindHomeOverlayPeer registers the device-generated WireGuard public key of
// an active enrolled device and assigns it one overlay address. Rebinding the
// same device replaces its key and keeps its address. The device is checked
// under the peer registry lock, which every revocation also takes after it
// revoked the entry, so a peer of a revoked device never survives.
func BindHomeOverlayPeer(root, deviceID, publicKey string, now time.Time) (HomeOverlayPeer, error) {
	publicKey = strings.TrimSpace(publicKey)
	if !validHomeOverlayPublicKey(publicKey) {
		return HomeOverlayPeer{}, ErrHomeOverlayKeyInvalid
	}
	node, err := EnsureHomeOverlayNodeKey(root)
	if err != nil {
		return HomeOverlayPeer{}, err
	}
	if publicKey == node.PublicKey {
		return HomeOverlayPeer{}, ErrHomeOverlayKeyInUse
	}
	var peer HomeOverlayPeer
	err = updateHomeOverlayPeerRegistry(root, func(registry *homeOverlayPeerRegistry) (bool, error) {
		devices, err := loadHomeDeviceRegistry(root)
		if err != nil {
			return false, err
		}
		if !homeOverlayDeviceActive(devices.Devices, deviceID, now) {
			return false, ErrHomeOverlayDeviceInactive
		}
		used := map[netip.Addr]bool{homeOverlayNodeAddress: true}
		peer = HomeOverlayPeer{DeviceID: deviceID, PublicKey: publicKey}
		kept := registry.Peers[:0]
		for _, existing := range registry.Peers {
			if existing.PublicKey == publicKey && existing.DeviceID != deviceID {
				return false, ErrHomeOverlayKeyInUse
			}
			if existing.DeviceID == deviceID {
				peer.Address = existing.Address
				continue
			}
			if address, parseErr := netip.ParseAddr(existing.Address); parseErr == nil {
				used[address] = true
			}
			kept = append(kept, existing)
		}
		if peer.Address == "" {
			for address := homeOverlayNodeAddress.Next(); homeOverlayPool.Contains(address) && address != homeOverlayBroadcast(); address = address.Next() {
				if !used[address] {
					peer.Address = address.String()
					break
				}
			}
			if peer.Address == "" {
				return false, ErrHomeOverlayPoolExhausted
			}
		}
		registry.Peers = append(kept, peer)
		return true, nil
	})
	if err != nil {
		return HomeOverlayPeer{}, err
	}
	return peer, nil
}

// removeHomeOverlayPeers deletes the peer records of revoked devices, so a
// revoked device keeps no overlay address or key on the node. It reports how
// many peers it removed.
//
// The core's identity workspace holds no overlay custody (the node key lives
// beside the peer registry); there it reports ErrHomeOverlayCustodyAbsent and
// the host's resync, which renders only active devices, removes the records.
func removeHomeOverlayPeers(root string, deviceIDs []string) (int, error) {
	if len(deviceIDs) == 0 {
		return 0, nil
	}
	if info, err := os.Stat(filepath.Join(root, filepath.Dir(filepath.FromSlash(homeOverlayPeerRegistryPath)))); err != nil || !info.IsDir() {
		return 0, ErrHomeOverlayCustodyAbsent
	}
	removed := 0
	err := updateHomeOverlayPeerRegistry(root, func(registry *homeOverlayPeerRegistry) (bool, error) {
		kept := registry.Peers[:0]
		for _, peer := range registry.Peers {
			if slices.Contains(deviceIDs, peer.DeviceID) {
				removed++
				continue
			}
			kept = append(kept, peer)
		}
		registry.Peers = kept
		return removed > 0, nil
	})
	return removed, err
}

// updateHomeOverlayPeerRegistry serializes every read-modify-write of the
// peer registry across goroutines and processes; mutate reports whether it
// changed the registry.
func updateHomeOverlayPeerRegistry(root string, mutate func(*homeOverlayPeerRegistry) (bool, error)) error {
	release, err := localevidence.AcquireCustodyLock(root, homeOverlayPeerRegistryPath, 101)
	if err != nil {
		return err
	}
	defer release()
	registry, err := loadHomeOverlayPeerRegistry(root)
	if err != nil {
		return err
	}
	changed, err := mutate(&registry)
	if err != nil || !changed {
		return err
	}
	return writeHomeOverlayPeerRegistry(root, registry)
}

// RenderHomeOverlayConfig renders the node's wg-quick interface configuration.
// It carries public keys and addresses only: the private key is loaded from
// local custody by PostUp. Peers of revoked or expired devices are omitted, so
// the next render and interface reload removes their access.
func RenderHomeOverlayConfig(root string, now time.Time) ([]byte, error) {
	if _, err := EnsureHomeOverlayNodeKey(root); err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	devices, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return nil, err
	}
	// Revocations through the core's identity routes cannot reach the peer
	// registry; the host's render drops their records for good.
	revoked := []string{}
	for _, device := range devices.Devices {
		if device.Revoked {
			revoked = append(revoked, device.ID)
		}
	}
	if _, err := removeHomeOverlayPeers(root, revoked); err != nil && !errors.Is(err, ErrHomeOverlayCustodyAbsent) {
		return nil, err
	}
	registry, err := loadHomeOverlayPeerRegistry(root)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, device := range devices.Devices {
		// Only a valid device name reaches the configuration comment.
		if ValidHomeDeviceName(device.Name) {
			names[device.ID] = device.Name
		}
	}
	peers := make([]HomeOverlayPeer, 0, len(registry.Peers))
	for _, peer := range registry.Peers {
		if homeOverlayDeviceActive(devices.Devices, peer.DeviceID, now) {
			peers = append(peers, peer)
		}
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Address < peers[j].Address })
	keyPath := filepath.Join(absRoot, filepath.FromSlash(homeOverlayNodePrivateKeyPath))
	var out bytes.Buffer
	fmt.Fprintf(&out, "# Rendered by stackkit for the VPN add-on (WireGuard). No private key is stored here.\n")
	fmt.Fprintf(&out, "[Interface]\nAddress = %s/%d\nListenPort = %d\n", homeOverlayNodeAddress, homeOverlayPool.Bits(), HomeOverlayListenPort)
	fmt.Fprintf(&out, "PostUp = wg set %%i private-key %s\n", keyPath)
	for _, peer := range peers {
		fmt.Fprintf(&out, "\n# device %s (%s)\n[Peer]\nPublicKey = %s\nAllowedIPs = %s/32\n", names[peer.DeviceID], peer.DeviceID, peer.PublicKey, peer.Address)
	}
	return out.Bytes(), nil
}

func homeOverlayDeviceActive(devices []HomeDevice, id string, now time.Time) bool {
	for _, device := range devices {
		if device.ID == id {
			return device.Active(now)
		}
	}
	return false
}

func homeOverlayBroadcast() netip.Addr {
	last := homeOverlayPool.Addr().As4()
	last[3] = 255
	return netip.AddrFrom4(last)
}

func validHomeOverlayPublicKey(value string) bool {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != 32 || bytes.Equal(raw, make([]byte, 32)) {
		return false
	}
	_, err = ecdh.X25519().NewPublicKey(raw)
	return err == nil
}

func loadHomeOverlayNodeKey(root string) (*ecdh.PrivateKey, error) {
	raw, err := readHomePrivate(root, homeOverlayNodePrivateKeyPath, 256)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("identity: unreadable overlay node key in custody")
	}
	return ecdh.X25519().NewPrivateKey(decoded)
}

func loadHomeOverlayPeerRegistry(root string) (homeOverlayPeerRegistry, error) {
	registry := homeOverlayPeerRegistry{Schema: homeOverlayPeerRegistrySchema}
	raw, err := readHomePrivate(root, homeOverlayPeerRegistryPath, homeOverlayPeerRegistryMaxSize)
	if errors.Is(err, fs.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return registry, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&registry) != nil || decoder.Decode(new(any)) != io.EOF || registry.Schema != homeOverlayPeerRegistrySchema {
		return registry, errors.New("identity: unreadable overlay peer registry")
	}
	// The root resync renders these fields into the wg-quick configuration,
	// and processes without root (the core server) can write this file, so
	// every field is checked to its exact form: a device ID is a key
	// thumbprint, a public key a WireGuard key and an address a unique host
	// address of the pool.
	seen := map[string]bool{}
	for _, peer := range registry.Peers {
		address, err := netip.ParseAddr(peer.Address)
		if !validHomeThumbprint(peer.DeviceID) || !validHomeOverlayPublicKey(peer.PublicKey) || err != nil || !address.Is4() ||
			!homeOverlayPool.Contains(address) || address == homeOverlayPool.Addr() || address == homeOverlayNodeAddress ||
			address == homeOverlayBroadcast() || address.String() != peer.Address || seen[peer.DeviceID] || seen[peer.PublicKey] || seen[peer.Address] {
			return homeOverlayPeerRegistry{Schema: homeOverlayPeerRegistrySchema}, errors.New("identity: unreadable overlay peer registry")
		}
		seen[peer.DeviceID], seen[peer.PublicKey], seen[peer.Address] = true, true, true
	}
	return registry, nil
}

// validHomeThumbprint admits an RFC 7638 SHA-256 thumbprint in base64url.
func validHomeThumbprint(value string) bool {
	return len(value) == 43 && strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") == ""
}

func writeHomeOverlayPeerRegistry(root string, registry homeOverlayPeerRegistry) error {
	registry.Schema = homeOverlayPeerRegistrySchema
	raw, err := json.Marshal(registry)
	if err != nil {
		return err
	}
	return writeHomePrivate(root, homeOverlayPeerRegistryPath, raw, false)
}

// HomeOverlayPeerRegistryFile is the VPN add-on peer registry below the
// workspace root.
const HomeOverlayPeerRegistryFile = homeOverlayPeerRegistryPath

// HomeOverlayNodeKeyFile is the overlay node private key in custody (wg's
// base64 form), below the workspace root. Only the root resync reads it, to
// set the key on a running interface again after wg syncconf.
const HomeOverlayNodeKeyFile = homeOverlayNodePrivateKeyPath
