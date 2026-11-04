package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The optional VPN add-on (#VPNAddOnV1 in foundation/network.cue, the tool
// catalog in addons/vpn, homelab identity target §6.2 and §8). It is never a
// default: it exists only after the Owner's opt-in at the Authority's
// console, which this file records. Its peers are exactly the enrolled,
// unrevoked, unexpired devices of the registry:
//
//   - an active device asks the host's peer service for its peer with its
//     device credential and a DPoP proof (IssueHomeVPNPeer); a pending away
//     enrollment is not active and gets none;
//   - a coordinated tool's node binds to the device that received its join
//     key, either by that key (Headscale) or by the device's probe through
//     the VPN (BindHomeVPNPeer);
//   - every registry change and a periodic pass reconcile the tool's live
//     nodes with the registry (ReconcileHomeVPNPeers): the nodes of revoked,
//     expired or removed devices, and nodes no active device bound within the
//     grace, are deleted. A ledger entry is forgotten only after its node and
//     key are gone, so a failed deletion is retried, never lost.
//
// The WireGuard tool keeps its peers in the overlay peer registry
// (home_overlay.go); this ledger holds the coordinated tools.
const (
	// HomeVPNPeerServicePort is the peer service's TLS listener on the node
	// site address and the VPN.
	HomeVPNPeerServicePort = 9444
	// HomeVPNServerName is the first DNS name of the peer service's
	// certificate, which native clients pin with the step-ca root.
	HomeVPNServerName = localevidence.HomeVPNServerName

	HomeVPNInfoPath    = "/api/v1/vpn"
	HomeVPNPeerPath    = "/api/v1/vpn/peer"
	HomeVPNConfirmPath = "/api/v1/vpn/peer/confirm"

	// HomeVPNUnboundGrace bounds how long a join key and an unbound node of a
	// coordinated tool live before the reconciler removes them.
	HomeVPNUnboundGrace = 15 * time.Minute

	HomeVPNToolWireGuard = "wireguard"
	HomeVPNToolHeadscale = "headscale"
	HomeVPNToolTailscale = "tailscale"
	HomeVPNToolPangolin  = "pangolin"

	homeVPNCustodyDir      = ".stackkit/custody/vpn-addon"
	homeVPNSelectionPath   = homeVPNCustodyDir + "/addon.json"
	homeVPNLedgerPath      = homeVPNCustodyDir + "/peers.json"
	homeVPNSelectionSchema = "stackkit.vpn-addon/v1"
	homeVPNLedgerSchema    = "stackkit.vpn-addon-peers/v1"
	homeVPNMaxBytes        = 256 << 10
	homeVPNMaxPeers        = 1024
)

// Ledger states of one device's peer.
const (
	HomeVPNPeerIssued   = "issued"
	HomeVPNPeerBound    = "bound"
	HomeVPNPeerRemoving = "removing"
)

// HomeVPNSelectionFile is the Owner's opt-in below the workspace root; the
// overlay resync watches it with the registries.
const HomeVPNSelectionFile = homeVPNSelectionPath

var (
	ErrHomeVPNOff            = errors.New("identity: the VPN add-on is off; the Owner switches it on with stackkit vpn enable")
	ErrHomeVPNDeviceInactive = errors.New("identity: only an enrolled, unrevoked, unexpired device receives a VPN peer")
	ErrHomeVPNNodeBound      = errors.New("identity: this VPN node belongs to another device")
	ErrHomeVPNNoPeer         = errors.New("identity: this device has no VPN peer of the selected tool; ask for one first")
	ErrHomeVPNLedgerFull     = errors.New("identity: the VPN peer ledger is full")
	errHomeVPNSelection      = errors.New("identity: unreadable VPN add-on selection")
	errHomeVPNLedger         = errors.New("identity: unreadable VPN peer ledger")
)

// HomeVPNTools are the add-on's alternative tools (#VPNAddOnToolV1).
var HomeVPNTools = []string{HomeVPNToolWireGuard, HomeVPNToolHeadscale, HomeVPNToolTailscale, HomeVPNToolPangolin}

// HomeVPNSelection is the Owner's opt-in: the one tool and what it needs that
// is not secret. Secrets of a tool (an OAuth client, an API key, Newt's
// secret) are separate custody files of the tool, never in this record.
type HomeVPNSelection struct {
	Schema    string    `json:"schema"`
	Tool      string    `json:"tool"`
	EnabledAt time.Time `json:"enabledAt"`
	// Endpoint is the Owner-declared host:port devices dial from away: the
	// WireGuard UDP endpoint, or the peer service that carries Headscale's
	// control endpoint. Tailscale and Pangolin have none.
	Endpoint  string                     `json:"endpoint,omitempty"`
	Tailscale *HomeVPNTailscaleSelection `json:"tailscale,omitempty"`
	Pangolin  *HomeVPNPangolinSelection  `json:"pangolin,omitempty"`
}

// HomeVPNTailscaleSelection names the Owner's tailnet.
type HomeVPNTailscaleSelection struct {
	Tailnet string `json:"tailnet"`
}

// HomeVPNPangolinSelection names the Owner's Pangolin server (the origin Newt
// and the devices' Olm connect to), its Integration API, the organization,
// the Newt site of this Authority and the site address resource the add-on
// created there.
type HomeVPNPangolinSelection struct {
	Endpoint   string `json:"endpoint"`
	API        string `json:"api"`
	OrgID      string `json:"orgId"`
	SiteID     int    `json:"siteId"`
	NewtID     string `json:"newtId"`
	ResourceID int    `json:"resourceId,omitempty"`
}

// HomeVPNPeer is one device's peer of a coordinated tool. NodeRef is the
// tool's node (Headscale node ID, Tailscale device ID, Pangolin client ID),
// KeyRef its join key. Neither is a secret.
type HomeVPNPeer struct {
	DeviceID  string     `json:"deviceId"`
	Tool      string     `json:"tool"`
	State     string     `json:"state"`
	KeyRef    string     `json:"keyRef,omitempty"`
	NodeRef   string     `json:"nodeRef,omitempty"`
	Addresses []string   `json:"addresses,omitempty"`
	IssuedAt  time.Time  `json:"issuedAt"`
	BoundAt   *time.Time `json:"boundAt,omitempty"`
}

// HomeVPNLiveNode is one node the tool's coordination holds under the
// add-on's device tag (or, for Pangolin, one of the add-on's clients).
type HomeVPNLiveNode struct {
	Ref       string    `json:"ref"`
	KeyRef    string    `json:"keyRef,omitempty"`
	Addresses []string  `json:"addresses,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// HomeVPNRemovals is what the tool must delete now: nodes and join keys.
// Settled are the ledger entries to forget once both succeeded.
type HomeVPNRemovals struct {
	Nodes   []HomeVPNLiveNode `json:"nodes"`
	Keys    []string          `json:"keys"`
	Settled []HomeVPNPeer     `json:"settled"`
}

type homeVPNLedger struct {
	Schema string        `json:"schema"`
	Peers  []HomeVPNPeer `json:"peers"`
}

// LoadHomeVPNSelection reads the Owner's opt-in; ErrHomeVPNOff without one.
func LoadHomeVPNSelection(root string) (HomeVPNSelection, error) {
	raw, err := readHomePrivate(root, homeVPNSelectionPath, homeVPNMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return HomeVPNSelection{}, ErrHomeVPNOff
	}
	if err != nil {
		return HomeVPNSelection{}, err
	}
	var selection HomeVPNSelection
	if !strictHomeJSON(raw, &selection) || validateHomeVPNSelection(selection) != nil {
		return HomeVPNSelection{}, errHomeVPNSelection
	}
	return selection, nil
}

// ValidateHomeVPNSelection checks an opt-in before anything is recorded.
func ValidateHomeVPNSelection(selection HomeVPNSelection) error {
	selection.Schema = homeVPNSelectionSchema
	return validateHomeVPNSelection(selection)
}

// SaveHomeVPNSelection records the Owner's opt-in after checking it.
func SaveHomeVPNSelection(root string, selection HomeVPNSelection) error {
	selection.Schema = homeVPNSelectionSchema
	if err := validateHomeVPNSelection(selection); err != nil {
		return err
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	if err := writeHomePrivate(root, homeVPNSelectionPath, raw, false); err != nil {
		return err
	}
	// The Basement core announces the peer service from this projection.
	return SyncHomeVPNAnnouncement(root)
}

// ClearHomeVPNSelection switches the add-on off. The caller removes the
// tool's peers first.
func ClearHomeVPNSelection(root string) error {
	err := os.Remove(filepath.Join(root, filepath.FromSlash(homeVPNSelectionPath)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return SyncHomeVPNAnnouncement(root)
}

func validateHomeVPNSelection(selection HomeVPNSelection) error {
	if selection.Schema != homeVPNSelectionSchema || !slices.Contains(HomeVPNTools, selection.Tool) || selection.EnabledAt.IsZero() {
		return errors.New("identity: the VPN add-on selection needs one of the tools wireguard, headscale, tailscale or pangolin")
	}
	switch selection.Tool {
	case HomeVPNToolWireGuard, HomeVPNToolHeadscale:
		if err := ValidateHomeVPNEndpoint(selection.Endpoint); err != nil {
			return err
		}
	default:
		if selection.Endpoint != "" {
			return fmt.Errorf("identity: the %s tool takes no endpoint", selection.Tool)
		}
	}
	if (selection.Tool == HomeVPNToolTailscale) != (selection.Tailscale != nil) || (selection.Tool == HomeVPNToolPangolin) != (selection.Pangolin != nil) {
		return errors.New("identity: the VPN add-on selection carries the settings of another tool")
	}
	if selection.Tailscale != nil && !validHomeVPNName(selection.Tailscale.Tailnet) {
		return errors.New("identity: the tailnet name is not valid")
	}
	if p := selection.Pangolin; p != nil {
		api, apiErr := url.Parse(p.API)
		if validateHomeOrigin(p.Endpoint) != nil || apiErr != nil || api.Scheme != "https" || api.Host == "" || api.User != nil || api.RawQuery != "" ||
			api.Fragment != "" || !validHomeVPNName(p.OrgID) || p.SiteID <= 0 || !validHomeVPNName(p.NewtID) || p.ResourceID < 0 {
			return errors.New("identity: Pangolin needs an https server origin, an https Integration API URL, an organization ID, a positive site ID and the site's Newt ID")
		}
	}
	return nil
}

// WriteHomeVPNSecret keeps one secret of the selected tool (an OAuth client,
// an API key, Newt's secret) in the add-on's custody, 0600.
func WriteHomeVPNSecret(root, name string, raw []byte) error {
	if !validHomeVPNSecretName(name) || len(raw) == 0 || len(raw) > 64<<10 {
		return errors.New("identity: invalid VPN add-on secret")
	}
	return writeHomePrivate(root, homeVPNCustodyDir+"/secrets/"+name, raw, false)
}

// ReadHomeVPNSecret reads one secret of the selected tool from custody.
func ReadHomeVPNSecret(root, name string) ([]byte, error) {
	if !validHomeVPNSecretName(name) {
		return nil, errors.New("identity: invalid VPN add-on secret")
	}
	return readHomePrivate(root, homeVPNCustodyDir+"/secrets/"+name, 64<<10)
}

func validHomeVPNSecretName(name string) bool {
	return name != "" && len(name) <= 64 && strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-.") == "" && !strings.HasPrefix(name, ".")
}

// HomeVPNCustodyDir is the add-on's custody directory below the workspace
// root: the selection, the peer ledger, the tools' secrets and state.
const HomeVPNCustodyDir = homeVPNCustodyDir

// ValidateHomeVPNEndpoint admits an Owner-declared host:port: a DNS name or an
// IP address and a port, nothing else.
func ValidateHomeVPNEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 || host == "" || len(endpoint) > 255 {
		return errors.New("identity: the VPN endpoint must be host:port")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return errors.New("identity: the VPN endpoint host must be a lowercase DNS name or an IP address")
		}
	}
	return nil
}

func validHomeVPNName(value string) bool {
	return value != "" && len(value) <= 128 && strings.Trim(value, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._@-") == ""
}

// IssueHomeVPNPeer gives an active device a new join key of a coordinated
// tool. The device is checked under the ledger lock and issue runs only for
// an active device; a peer the device held before is marked for removal, so
// a device holds at most one peer.
func IssueHomeVPNPeer(root, deviceID, tool string, now time.Time, issue func() (HomeVPNPeer, error)) (HomeVPNPeer, error) {
	if tool == HomeVPNToolWireGuard || !slices.Contains(HomeVPNTools, tool) {
		return HomeVPNPeer{}, errors.New("identity: the ledger holds only coordinated VPN tools")
	}
	var peer HomeVPNPeer
	err := updateHomeVPNLedger(root, func(ledger *homeVPNLedger) error {
		devices, err := loadHomeDeviceRegistry(root)
		if err != nil {
			return err
		}
		if !homeOverlayDeviceActive(devices.Devices, deviceID, now) {
			return ErrHomeVPNDeviceInactive
		}
		if len(ledger.Peers) >= homeVPNMaxPeers {
			return ErrHomeVPNLedgerFull
		}
		issued, err := issue()
		if err != nil {
			return err
		}
		peer = issued
		peer.DeviceID, peer.Tool, peer.IssuedAt = deviceID, tool, now
		if peer.State != HomeVPNPeerBound {
			peer.State = HomeVPNPeerIssued
		}
		if peer.State == HomeVPNPeerBound {
			peer.BoundAt = &now
		}
		for index := range ledger.Peers {
			if ledger.Peers[index].DeviceID == deviceID && ledger.Peers[index].State != HomeVPNPeerRemoving {
				ledger.Peers[index].State = HomeVPNPeerRemoving
			}
		}
		ledger.Peers = append(ledger.Peers, peer)
		return nil
	})
	return peer, err
}

// BindHomeVPNPeer binds a coordinated node to the active device whose probe
// arrived from it through the VPN. A node bound to another device, a node
// joined with another device's key and a device without a current peer are
// refused.
func BindHomeVPNPeer(root, deviceID, tool string, node HomeVPNLiveNode, now time.Time) (HomeVPNPeer, error) {
	if node.Ref == "" {
		return HomeVPNPeer{}, ErrHomeVPNNoPeer
	}
	var bound HomeVPNPeer
	err := updateHomeVPNLedger(root, func(ledger *homeVPNLedger) error {
		devices, err := loadHomeDeviceRegistry(root)
		if err != nil {
			return err
		}
		if !homeOverlayDeviceActive(devices.Devices, deviceID, now) {
			return ErrHomeVPNDeviceInactive
		}
		current := -1
		for index, peer := range ledger.Peers {
			if peer.Tool != tool || peer.State == HomeVPNPeerRemoving {
				if peer.NodeRef == node.Ref && peer.DeviceID != deviceID {
					return ErrHomeVPNNodeBound
				}
				continue
			}
			if peer.NodeRef == node.Ref && peer.DeviceID != deviceID {
				return ErrHomeVPNNodeBound
			}
			if peer.KeyRef != "" && peer.KeyRef == node.KeyRef && peer.DeviceID != deviceID {
				return ErrHomeVPNNodeBound
			}
			if peer.DeviceID == deviceID {
				current = index
			}
		}
		if current < 0 {
			return ErrHomeVPNNoPeer
		}
		peer := &ledger.Peers[current]
		switch {
		case peer.KeyRef != "" && node.KeyRef != "" && peer.KeyRef != node.KeyRef:
			return ErrHomeVPNNodeBound
		case peer.State == HomeVPNPeerBound && peer.NodeRef != node.Ref:
			return ErrHomeVPNNodeBound
		case peer.State == HomeVPNPeerIssued && now.Sub(peer.IssuedAt) > HomeVPNUnboundGrace:
			return ErrHomeVPNNoPeer
		}
		if peer.State != HomeVPNPeerBound {
			peer.State, peer.NodeRef, peer.BoundAt = HomeVPNPeerBound, node.Ref, &now
		}
		peer.Addresses = slices.Clone(node.Addresses)
		bound = *peer
		return nil
	})
	return bound, err
}

// ReconcileHomeVPNPeers compares the tool's live nodes with the registry. It
// binds a node that joined with a device's own key, marks the peers of
// inactive devices and expired join keys for removal, and returns every node
// and key the tool must delete now: those of entries being removed, nodes no
// active device bound within the grace, and, because a Tailscale node does
// not name its key, every unbound node that appeared after a removed device's
// unbound key was issued.
func ReconcileHomeVPNPeers(root, tool string, live []HomeVPNLiveNode, now time.Time) (HomeVPNRemovals, error) {
	var removals HomeVPNRemovals
	err := updateHomeVPNLedger(root, func(ledger *homeVPNLedger) error {
		devices, err := loadHomeDeviceRegistry(root)
		if err != nil {
			return err
		}
		removals = planHomeVPNRemovals(devices.Devices, ledger, tool, live, now)
		return nil
	})
	return removals, err
}

func planHomeVPNRemovals(devices []HomeDevice, ledger *homeVPNLedger, tool string, live []HomeVPNLiveNode, now time.Time) HomeVPNRemovals {
	removals := HomeVPNRemovals{Nodes: []HomeVPNLiveNode{}, Keys: []string{}, Settled: []HomeVPNPeer{}}
	// Bind by join key first: a Headscale node names the key it joined with.
	for _, node := range live {
		if node.KeyRef == "" {
			continue
		}
		for index := range ledger.Peers {
			peer := &ledger.Peers[index]
			if peer.Tool == tool && peer.State == HomeVPNPeerIssued && peer.KeyRef == node.KeyRef && !homeVPNNodeBoundElsewhere(ledger, node.Ref, peer.DeviceID) {
				bound := now
				peer.State, peer.NodeRef, peer.BoundAt, peer.Addresses = HomeVPNPeerBound, node.Ref, &bound, slices.Clone(node.Addresses)
			}
		}
	}
	for index := range ledger.Peers {
		peer := &ledger.Peers[index]
		if peer.Tool != tool || peer.State == HomeVPNPeerRemoving {
			continue
		}
		if !homeOverlayDeviceActive(devices, peer.DeviceID, now) || (peer.State == HomeVPNPeerIssued && now.Sub(peer.IssuedAt) > HomeVPNUnboundGrace) {
			peer.State = HomeVPNPeerRemoving
		}
	}
	keep := map[string]bool{}
	planned := map[string]bool{}
	// The unbound join keys of inactive devices: a node that does not name
	// its key and appeared after such a key was issued may be that device's.
	// Their entries stay as tombstones (key gone, no node) through the grace,
	// so a node that joins or is listed late is still recognized.
	var revokedUnboundSince []time.Time
	for _, peer := range ledger.Peers {
		if peer.Tool == tool && peer.State != HomeVPNPeerRemoving && peer.NodeRef != "" {
			keep[peer.NodeRef] = true
		}
	}
	for _, peer := range ledger.Peers {
		if peer.Tool != tool || peer.State != HomeVPNPeerRemoving {
			continue
		}
		if peer.KeyRef != "" {
			removals.Keys = append(removals.Keys, peer.KeyRef)
		}
		// A known node is deleted by its reference, whether the tool lists it
		// yet or not, unless a current peer holds it.
		if peer.NodeRef != "" && !planned[peer.NodeRef] && !keep[peer.NodeRef] {
			planned[peer.NodeRef] = true
			removals.Nodes = append(removals.Nodes, HomeVPNLiveNode{Ref: peer.NodeRef, KeyRef: peer.KeyRef})
		}
		if peer.NodeRef == "" && !homeOverlayDeviceActive(devices, peer.DeviceID, now) {
			revokedUnboundSince = append(revokedUnboundSince, peer.IssuedAt)
		}
		removals.Settled = append(removals.Settled, peer)
	}
	for _, node := range live {
		if keep[node.Ref] || planned[node.Ref] {
			continue
		}
		// A node that names its join key and is not bound by now joined with
		// a key that is not a current peer's: it goes at once.
		if node.KeyRef != "" {
			removals.Nodes = append(removals.Nodes, node)
			continue
		}
		// A node without a key name waits for its device's probe within the
		// grace, unless a revoked device's unbound key could have made it.
		young := !node.CreatedAt.IsZero() && now.Sub(node.CreatedAt) <= HomeVPNUnboundGrace
		suspect := false
		for _, since := range revokedUnboundSince {
			if !node.CreatedAt.Before(since.Add(-time.Minute)) {
				suspect = true
			}
		}
		if young && !suspect {
			continue
		}
		removals.Nodes = append(removals.Nodes, node)
	}
	return removals
}

func homeVPNNodeBoundElsewhere(ledger *homeVPNLedger, nodeRef, deviceID string) bool {
	for _, peer := range ledger.Peers {
		if peer.NodeRef == nodeRef && peer.DeviceID != deviceID {
			return true
		}
	}
	return false
}

// ForgetHomeVPNPeers settles entries after the tool deleted their node and
// key: an entry is dropped, except an unbound one still inside the grace,
// which stays as a tombstone without key (see planHomeVPNRemovals). An entry
// that changed meanwhile is kept as it is.
func ForgetHomeVPNPeers(root string, settled []HomeVPNPeer, now time.Time) error {
	if len(settled) == 0 {
		return nil
	}
	return updateHomeVPNLedger(root, func(ledger *homeVPNLedger) error {
		kept := ledger.Peers[:0]
		for _, peer := range ledger.Peers {
			done := false
			for _, entry := range settled {
				if peer.State == HomeVPNPeerRemoving && peer.DeviceID == entry.DeviceID && peer.Tool == entry.Tool && peer.KeyRef == entry.KeyRef &&
					peer.NodeRef == entry.NodeRef && peer.IssuedAt.Equal(entry.IssuedAt) {
					done = true
				}
			}
			switch {
			case !done:
				kept = append(kept, peer)
			case peer.NodeRef == "" && now.Sub(peer.IssuedAt) <= HomeVPNUnboundGrace:
				peer.KeyRef = ""
				kept = append(kept, peer)
			}
		}
		ledger.Peers = kept
		return nil
	})
}

// HomeVPNBoundAddresses are the VPN addresses of the bound peers of active
// devices: the only sources the interface guard of a coordinated tool lets
// past the peer service.
func HomeVPNBoundAddresses(root, tool string, now time.Time) ([]string, error) {
	ledger, err := loadHomeVPNLedger(root)
	if err != nil {
		return nil, err
	}
	devices, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return nil, err
	}
	var addresses []string
	for _, peer := range ledger.Peers {
		if peer.Tool != tool || peer.State != HomeVPNPeerBound || !homeOverlayDeviceActive(devices.Devices, peer.DeviceID, now) {
			continue
		}
		for _, raw := range peer.Addresses {
			if address, parseErr := netip.ParseAddr(raw); parseErr == nil && address.Is4() && !slices.Contains(addresses, address.String()) {
				addresses = append(addresses, address.String())
			}
		}
	}
	slices.Sort(addresses)
	return addresses, nil
}

// RemoveAllHomeVPNPeers marks every peer of a tool for removal (the Owner
// switches the add-on off) and returns them.
func RemoveAllHomeVPNPeers(root, tool string) ([]HomeVPNPeer, error) {
	var marked []HomeVPNPeer
	err := updateHomeVPNLedger(root, func(ledger *homeVPNLedger) error {
		for index := range ledger.Peers {
			if ledger.Peers[index].Tool == tool {
				ledger.Peers[index].State = HomeVPNPeerRemoving
				marked = append(marked, ledger.Peers[index])
			}
		}
		return nil
	})
	return marked, err
}

// ListHomeVPNPeers returns the ledger.
func ListHomeVPNPeers(root string) ([]HomeVPNPeer, error) {
	ledger, err := loadHomeVPNLedger(root)
	return ledger.Peers, err
}

// HomeVPNPeerOf returns the device's current peer of a tool.
func HomeVPNPeerOf(root, deviceID, tool string) (HomeVPNPeer, error) {
	ledger, err := loadHomeVPNLedger(root)
	if err != nil {
		return HomeVPNPeer{}, err
	}
	for _, peer := range ledger.Peers {
		if peer.DeviceID == deviceID && peer.Tool == tool && peer.State != HomeVPNPeerRemoving {
			return peer, nil
		}
	}
	return HomeVPNPeer{}, ErrHomeVPNNoPeer
}

// HomeVPNDeviceActive reports whether a device is an active registry entry.
func HomeVPNDeviceActive(root, deviceID string, now time.Time) (bool, error) {
	devices, err := loadHomeDeviceRegistry(root)
	if err != nil {
		return false, err
	}
	return homeOverlayDeviceActive(devices.Devices, deviceID, now), nil
}

func updateHomeVPNLedger(root string, mutate func(*homeVPNLedger) error) error {
	release, err := localevidence.AcquireCustodyLock(root, homeVPNLedgerPath, 101)
	if err != nil {
		return err
	}
	defer release()
	ledger, err := loadHomeVPNLedger(root)
	if err != nil {
		return err
	}
	before, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	if err := mutate(&ledger); err != nil {
		return err
	}
	ledger.Schema = homeVPNLedgerSchema
	after, err := json.Marshal(ledger)
	if err != nil || bytes.Equal(before, after) {
		return err
	}
	return writeHomePrivate(root, homeVPNLedgerPath, after, false)
}

func loadHomeVPNLedger(root string) (homeVPNLedger, error) {
	ledger := homeVPNLedger{Schema: homeVPNLedgerSchema, Peers: []HomeVPNPeer{}}
	raw, err := readHomePrivate(root, homeVPNLedgerPath, homeVPNMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return ledger, nil
	}
	if err != nil {
		return ledger, err
	}
	if !strictHomeJSON(raw, &ledger) || ledger.Schema != homeVPNLedgerSchema {
		return homeVPNLedger{Schema: homeVPNLedgerSchema}, errHomeVPNLedger
	}
	for _, peer := range ledger.Peers {
		if !validHomeThumbprint(peer.DeviceID) || !slices.Contains(HomeVPNTools[1:], peer.Tool) || !slices.Contains([]string{HomeVPNPeerIssued, HomeVPNPeerBound, HomeVPNPeerRemoving}, peer.State) ||
			(peer.KeyRef != "" && !validHomeVPNName(peer.KeyRef)) || (peer.NodeRef != "" && !validHomeVPNName(peer.NodeRef)) {
			return homeVPNLedger{Schema: homeVPNLedgerSchema}, errHomeVPNLedger
		}
	}
	return ledger, nil
}

func strictHomeJSON(raw []byte, out any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out) == nil && decoder.Decode(new(any)) == io.EOF
}

// AuthenticateHomeVPNRequest admits a peer service request only with the
// current credential of an active device and a fresh, unused DPoP proof for
// exactly this method, path and one of the service's own origins (from
// custody, never the request's Host).
func AuthenticateHomeVPNRequest(root string, r *http.Request, origins []string) (HomeAuthentication, error) {
	credential, proof, err := ParseHomeDeviceAuthorization(r.Header)
	if err != nil {
		return HomeAuthentication{}, ErrHomeAccessDenied
	}
	origin, ok := homeIdentityRouteOrigin(proofTarget(proof), r.URL.Path)
	if !ok || !slices.Contains(origins, origin) {
		return HomeAuthentication{}, ErrHomeAccessDenied
	}
	verifier, err := HomeVerifierFor(root)
	if err != nil {
		return HomeAuthentication{}, err
	}
	return verifier.Authenticate(HomeDeviceRequest{Method: r.Method, RouteOrigin: origin, Path: r.URL.Path, Credential: credential, Proof: proof})
}
