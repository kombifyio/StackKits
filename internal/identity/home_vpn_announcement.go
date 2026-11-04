package identity

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// The VPN add-on's peer service announced to device clients (homelab
// identity target §6.2: an enrolled device receives an active VPN add-on).
// The selection lives in the add-on's custody on the host; the Basement core
// holds none, so the host projects the selected tool, which is no secret,
// into the verification custody whenever the Owner switches the add-on on or
// off. The enrollment bootstrap and the answer of an enrollment that issued a
// credential carry the announcement; the Hub PWA runs no VPN, native device
// clients ask the peer service for their peer.
const (
	homeVPNAnnouncementPath   = ".stackkit/custody/home-verifier/vpn-announcement.json"
	homeVPNAnnouncementSchema = "stackkit.vpn-addon-announcement/v1"
)

// HomeVPNAnnouncement is where an enrolled device asks for its VPN peer.
type HomeVPNAnnouncement struct {
	Tool          string `json:"tool"`
	PeerService   string `json:"peerService"`
	TLSServerName string `json:"tlsServerName"`
}

type homeVPNAnnouncementRecord struct {
	Schema string `json:"schema"`
	Tool   string `json:"tool"`
}

// homeVPNAnnouncement is the announcement of this Authority, nil while the
// add-on is off: from the selection where this process holds the add-on's
// custody (the host), from the projection otherwise (the core).
func homeVPNAnnouncement(root string) *HomeVPNAnnouncement {
	tool := ""
	selection, err := LoadHomeVPNSelection(root)
	switch {
	case err == nil:
		tool = selection.Tool
	case errors.Is(err, ErrHomeVPNOff):
		raw, readErr := readHomePrivate(root, homeVPNAnnouncementPath, 4<<10)
		var record homeVPNAnnouncementRecord
		if readErr != nil || !strictHomeJSON(raw, &record) || record.Schema != homeVPNAnnouncementSchema || !slices.Contains(HomeVPNTools, record.Tool) {
			return nil
		}
		tool = record.Tool
	default:
		return nil
	}
	authority, err := homeAuthority(root)
	if err != nil || authority.LANSiteAddress == "" {
		return nil
	}
	return &HomeVPNAnnouncement{
		Tool: tool, TLSServerName: HomeVPNServerName,
		PeerService: "https://" + net.JoinHostPort(authority.LANSiteAddress, strconv.Itoa(HomeVPNPeerServicePort)),
	}
}

// SyncHomeVPNAnnouncement projects the selected tool into the verification
// custody, or removes the projection while the add-on is off (host only).
func SyncHomeVPNAnnouncement(root string) error {
	selection, err := LoadHomeVPNSelection(root)
	if errors.Is(err, ErrHomeVPNOff) {
		if removeErr := os.Remove(filepath.Join(root, filepath.FromSlash(homeVPNAnnouncementPath))); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return removeErr
		}
		return nil
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(homeVPNAnnouncementRecord{Schema: homeVPNAnnouncementSchema, Tool: selection.Tool})
	if err != nil {
		return err
	}
	return writeHomePrivate(root, homeVPNAnnouncementPath, raw, false)
}

// homeVPNCoordinatedCustodyHeld reports whether this process holds the
// add-on's peer ledger (the host).
func homeVPNCoordinatedCustodyHeld(root string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(homeVPNCustodyDir)))
	return err == nil && info.IsDir()
}
