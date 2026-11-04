package identity

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The Basement core runs stackkit-server in its container network and
// publishes the LAN enrollment listener on the node site address only. Inside
// that namespace the process cannot see the host's site interface, so the
// host records it at Apply: the site address, the prefix of the interface that
// carries it and every address of the host. Docker forwards a connection to a
// published port with its source address intact, so the listener applies the
// same home rule as in the host namespace: a direct peer inside the site
// prefix that is none of the host's addresses and not in the overlay pool. A
// connection from the host itself, a container or a connector arrives from a
// host or bridge address and is never home.
const (
	homeSiteNetworkPath     = ".stackkit/custody/home-verifier/site-network.json"
	homeSiteNetworkSchema   = "stackkit.home-site-network/v1"
	homeSiteNetworkMaxBytes = 16 << 10
	homeSiteNetworkMaxHosts = 256
)

type homeSiteNetworkRecord struct {
	Schema        string    `json:"schema"`
	SiteAddress   string    `json:"siteAddress"`
	Prefix        string    `json:"prefix"`
	HostAddresses []string  `json:"hostAddresses"`
	RecordedAt    time.Time `json:"recordedAt"`
}

// RecordHomeSiteNetwork records the host's site network for the LAN
// enrollment listener of the Basement core. It runs on the host (Apply), where
// the site interface is visible, and fails when the node site address is on
// no interface of the host.
func RecordHomeSiteNetwork(root string, now time.Time) error {
	site, err := localevidence.BasementLANDNSResolverAddress(root)
	if err != nil {
		return err
	}
	siteAddress, err := netip.ParseAddr(site)
	if err != nil || !ObservedLANAddress(site) {
		return errors.New("identity: the node site address is not a LAN address")
	}
	network, err := interfaceHomeSiteNetwork(siteAddress.Unmap())
	if err != nil {
		return err
	}
	if !network.prefix.IsValid() {
		return errors.New("identity: the node site address is on no interface of this host")
	}
	record := homeSiteNetworkRecord{
		Schema: homeSiteNetworkSchema, SiteAddress: siteAddress.Unmap().String(), Prefix: network.prefix.String(), RecordedAt: now.UTC(),
	}
	for _, address := range network.local {
		if len(record.HostAddresses) < homeSiteNetworkMaxHosts && !slices.Contains(record.HostAddresses, address.String()) {
			record.HostAddresses = append(record.HostAddresses, address.String())
		}
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if current, readErr := readHomePrivate(root, homeSiteNetworkPath, homeSiteNetworkMaxBytes); readErr == nil {
		var prior homeSiteNetworkRecord
		if json.Unmarshal(current, &prior) == nil && prior.SiteAddress == record.SiteAddress && prior.Prefix == record.Prefix &&
			slices.Equal(prior.HostAddresses, record.HostAddresses) {
			return nil
		}
	}
	return writeHomePrivate(root, homeSiteNetworkPath, raw, false)
}

// loadRecordedHomeSiteNetwork reads the host's record for exactly the site
// address in custody. A record for another address, an address outside its
// own prefix or a malformed record attests nothing.
func loadRecordedHomeSiteNetwork(root string, siteAddress netip.Addr) (homeSiteNetwork, error) {
	raw, err := readHomePrivate(root, homeSiteNetworkPath, homeSiteNetworkMaxBytes)
	if err != nil {
		return homeSiteNetwork{}, err
	}
	var record homeSiteNetworkRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || record.Schema != homeSiteNetworkSchema ||
		len(record.HostAddresses) == 0 || len(record.HostAddresses) > homeSiteNetworkMaxHosts {
		return homeSiteNetwork{}, errors.New("identity: unreadable site network record")
	}
	recorded, err := netip.ParseAddr(record.SiteAddress)
	if err != nil || recorded != siteAddress {
		return homeSiteNetwork{}, errors.New("identity: the site network record is for another site address")
	}
	prefix, err := netip.ParsePrefix(record.Prefix)
	if err != nil || prefix != prefix.Masked() || !prefix.Contains(siteAddress) || prefix.Bits() < 8 {
		return homeSiteNetwork{}, errors.New("identity: the site network record has no valid prefix")
	}
	network := homeSiteNetwork{prefix: prefix}
	for _, value := range record.HostAddresses {
		address, parseErr := netip.ParseAddr(value)
		if parseErr != nil {
			return homeSiteNetwork{}, errors.New("identity: the site network record has an invalid host address")
		}
		network.local = append(network.local, address.Unmap())
	}
	if !slices.Contains(network.local, siteAddress) {
		return homeSiteNetwork{}, errors.New("identity: the site network record does not list the site address")
	}
	return network, nil
}

// homeHubOrigin is the Hub origin of the identity runtime custody.
func homeHubOrigin(root string) (string, bool) {
	address, err := homeIdentityAddress(root)
	if err != nil {
		return "", false
	}
	origin := "https://" + address.ServiceHost("base")
	return origin, validateHomeOrigin(origin) == nil
}

// homeHubCORS lets the Hub origin, and no other, call the LAN enrollment
// listener from a browser: the Hub PWA is served on the Hub origin and the
// listener on its own port. A preflight is answered only for that origin.
func homeHubCORS(root string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		hub, ok := homeHubOrigin(root)
		if origin != "" && ok && origin == hub {
			w.Header().Set("Access-Control-Allow-Origin", hub)
			w.Header().Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "POST")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, DPoP")
				w.Header().Set("Access-Control-Max-Age", "600")
				if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
					w.Header().Set("Access-Control-Allow-Private-Network", "true")
				}
			}
		} else if r.Method == http.MethodOptions {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// homeDefaultGateways returns this namespace's default gateways. The router
// relays connections (port forwards with masquerade, router VPNs, subnet
// routers) with its own LAN address, so a gateway is never home. Outside Linux
// it returns none.
func homeDefaultGateways() []netip.Addr {
	gateways := []netip.Addr{}
	if raw, err := os.ReadFile("/proc/net/route"); err == nil {
		gateways = append(gateways, parseHomeIPv4DefaultGateways(string(raw))...)
	}
	if raw, err := os.ReadFile("/proc/net/ipv6_route"); err == nil {
		gateways = append(gateways, parseHomeIPv6DefaultGateways(string(raw))...)
	}
	return gateways
}

// parseHomeIPv4DefaultGateways reads /proc/net/route: the default routes
// (destination 0, gateway flag) and their gateway, in little-endian hex.
func parseHomeIPv4DefaultGateways(table string) []netip.Addr {
	gateways := []netip.Addr{}
	for _, line := range strings.Split(table, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		raw, hexErr := hex.DecodeString(fields[2])
		if err != nil || hexErr != nil || len(raw) != 4 || flags&0x2 == 0 {
			continue
		}
		var address [4]byte
		binary.BigEndian.PutUint32(address[:], binary.LittleEndian.Uint32(raw))
		gateways = append(gateways, netip.AddrFrom4(address))
	}
	return gateways
}

// parseHomeIPv6DefaultGateways reads /proc/net/ipv6_route: routes to ::/0 and
// their next hop.
func parseHomeIPv6DefaultGateways(table string) []netip.Addr {
	gateways := []netip.Addr{}
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != strings.Repeat("0", 32) || fields[1] != "00" {
			continue
		}
		raw, err := hex.DecodeString(fields[4])
		if err != nil || len(raw) != 16 {
			continue
		}
		address := netip.AddrFrom16([16]byte(raw))
		if !address.IsUnspecified() {
			gateways = append(gateways, address)
		}
	}
	return gateways
}
