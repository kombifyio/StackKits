package identity

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

// The homelab DNS zone (homelab identity target §4): Techstack assembles,
// from its inventory, where each server of the homelab answers on the LAN,
// and every server's LAN resolver serves the whole zone, so each member
// device reaches every server by its names. A DNS answer is never identity
// (§5, ADR-0029 §5): TLS still verifies each name under the root, and each
// server admits a zone only as far as the join trust it holds vouches for it.
// The Authority admits a joined server's names only as the Owner approved
// them at join; a joined server admits the Authority's domain only at the
// Owner-signed LAN address of its bundle, its own names only at its own
// address, and another server's names only as the Authority's signed
// verification state lists them. No server ever lets one server answer for
// another's names or for the identity names.
const HomeDNSZoneSchema = "stackkit.homelab-dns-zone/v1"

// HomeDNSZoneServer is one joined server in the zone.
type HomeDNSZoneServer struct {
	// ServerRef is the joined server's owner reference.
	ServerRef string   `json:"serverRef"`
	Address   string   `json:"address"`
	Hostnames []string `json:"hostnames"`
}

// HomeDNSZone is the zone Techstack assembles and carries to each server.
type HomeDNSZone struct {
	Schema string `json:"schema"`
	// Domain is the homelab domain the Authority serves.
	Domain string `json:"domain"`
	// Authority is the Authority's LAN address.
	Authority string              `json:"authority"`
	Servers   []HomeDNSZoneServer `json:"servers"`
}

// HomeDNSZoneInstalled reports what this server's resolver now serves.
type HomeDNSZoneInstalled struct {
	Role    string                           `json:"role"`
	Records []localevidence.LANDNSZoneRecord `json:"records"`
	Changed bool                             `json:"changed"`
	// Skipped are servers the zone lists at another LAN address than the
	// one the Owner approved for them; their names are left out.
	Skipped []string `json:"skipped"`
}

// ParseHomeDNSZone decodes a carried zone strictly and checks its shape.
func ParseHomeDNSZone(raw []byte) (HomeDNSZone, error) {
	var zone HomeDNSZone
	if len(raw) == 0 || len(raw) > homeJoinMaxBytes || decodeHomeJoinStrict(raw, &zone) != nil || zone.Schema != HomeDNSZoneSchema ||
		!homeJoinHostname.MatchString(zone.Domain) || !homeZoneAddress(zone.Authority) || len(zone.Servers) > homeJoinMaxServers {
		return HomeDNSZone{}, ErrHomeJoinRefused
	}
	overlaps := func(a, b string) bool { return a == b || strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a) }
	identity := homeJoinIdentityNames(localevidence.IdentityRuntimeAddress{Domain: zone.Domain})
	var held []string
	for index, server := range zone.Servers {
		if !homeJoinRefPattern.MatchString(server.ServerRef) || !homeZoneAddress(server.Address) || server.Address == zone.Authority ||
			(index > 0 && zone.Servers[index-1].ServerRef >= server.ServerRef) ||
			len(server.Hostnames) == 0 || len(server.Hostnames) > homeJoinMaxHostnames || !slices.IsSorted(server.Hostnames) {
			return HomeDNSZone{}, ErrHomeJoinRefused
		}
		for _, host := range server.Hostnames {
			if !homeJoinHostname.MatchString(host) || !strings.HasSuffix(host, "."+zone.Domain) ||
				slices.ContainsFunc(identity, func(name string) bool { return overlaps(host, name) }) ||
				slices.ContainsFunc(held, func(name string) bool { return overlaps(host, name) }) {
				return HomeDNSZone{}, ErrHomeJoinRefused
			}
		}
		held = append(held, server.Hostnames...)
	}
	return zone, nil
}

func homeZoneAddress(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.String() == value && ObservedLANAddress(value)
}

// InstallHomeDNSZone admits the carried zone on this server's host as far as
// its join trust vouches for each record and installs the records into its
// LAN resolver custody.
func InstallHomeDNSZone(ctx context.Context, root string, raw []byte) (HomeDNSZoneInstalled, error) {
	zone, err := ParseHomeDNSZone(raw)
	if err != nil {
		return HomeDNSZoneInstalled{}, err
	}
	ownDomain, ownAddress, err := localevidence.BasementLANDNSSite(root)
	if err != nil {
		return HomeDNSZoneInstalled{}, err
	}
	installed := HomeDNSZoneInstalled{Records: []localevidence.LANDNSZoneRecord{}, Skipped: []string{}}
	add := func(name, address string) {
		if name == ownDomain {
			return // the resolver's own record
		}
		installed.Records = append(installed.Records, localevidence.LANDNSZoneRecord{Name: name, Address: netip.MustParseAddr(address)})
	}
	if homeJoined(root) {
		installed.Role = "joined"
		if err := admitHomeDNSZoneJoined(ctx, root, zone, ownAddress.String(), add, &installed); err != nil {
			return HomeDNSZoneInstalled{}, err
		}
	} else {
		installed.Role = "authority"
		if err := admitHomeDNSZoneAuthority(root, zone, ownAddress.String(), add, &installed); err != nil {
			return HomeDNSZoneInstalled{}, err
		}
	}
	slices.SortFunc(installed.Records, func(a, b localevidence.LANDNSZoneRecord) int { return strings.Compare(a.Name, b.Name) })
	installed.Changed, err = localevidence.InstallBasementLANDNSZone(root, installed.Records)
	return installed, err
}

func admitHomeDNSZoneAuthority(root string, zone HomeDNSZone, ownAddress string, add func(name, address string), installed *HomeDNSZoneInstalled) error {
	address, err := homeIdentityAddress(root)
	if err != nil {
		return errors.New("identity: only the Authority or a joined server installs the homelab zone")
	}
	if zone.Domain != address.Domain || zone.Authority != ownAddress {
		return errors.New("identity: the zone names another homelab domain or another Authority address than this Authority's")
	}
	servers, err := loadHomeJoinedServers(root)
	if err != nil && len(zone.Servers) > 0 {
		return errors.New("identity: this Authority has no joined servers to admit into the zone")
	}
	for _, entry := range zone.Servers {
		index := slices.IndexFunc(servers.Servers, func(server homeJoinedServer) bool {
			return server.Server.OwnerRef == entry.ServerRef && !server.Removed && time.Now().Before(server.ExpiresAt)
		})
		if index < 0 || !slices.Equal(servers.Servers[index].Server.Hostnames, entry.Hostnames) {
			return errors.New("identity: the zone lists names for " + entry.ServerRef + " that the Owner did not approve at join")
		}
		if approved := servers.Servers[index].Server.LANAddress; approved != "" && approved != entry.Address {
			installed.Skipped = append(installed.Skipped, entry.ServerRef)
			continue
		}
		for _, host := range entry.Hostnames {
			add(host, entry.Address)
		}
	}
	return nil
}

func admitHomeDNSZoneJoined(ctx context.Context, root string, zone HomeDNSZone, ownAddress string, add func(name, address string), installed *HomeDNSZoneInstalled) error {
	bundle, err := LoadHomeJoinedTrust(root)
	if err != nil {
		return err
	}
	if zone.Domain != bundle.IdentityDomain || zone.Authority != bundle.AuthorityLANAddress {
		return errors.New("identity: the zone names another homelab domain or another Authority address than the trust bundle")
	}
	// The other servers come from a current state only: read it now.
	read, cancel := context.WithTimeout(ctx, 15*time.Second)
	_ = RefreshHomeJoinedState(read, root, time.Now())
	cancel()
	state, err := loadHomeJoinedState(root, bundle)
	if err != nil || state.IssuedAt == nil || time.Since(state.IssuedAt.Time()) > homeJoinedStateFresh {
		return errors.New("identity: no current verified Authority state; the zone is admitted only while the Authority answers")
	}
	add(zone.Domain, zone.Authority)
	for _, entry := range zone.Servers {
		if entry.ServerRef == bundle.Server.OwnerRef {
			if entry.Address != ownAddress || !slices.Equal(entry.Hostnames, bundle.Server.Hostnames) {
				return errors.New("identity: the zone lists this server at another address or with other names than its own")
			}
		} else if listed, ok := state.Servers[entry.ServerRef]; !ok || !slices.Equal(listed.Hostnames, entry.Hostnames) {
			return errors.New("identity: the zone lists names for " + entry.ServerRef + " that the Authority's verification state does not")
		} else if listed.LANAddress != "" && listed.LANAddress != entry.Address {
			installed.Skipped = append(installed.Skipped, entry.ServerRef)
			continue
		}
		for _, host := range entry.Hostnames {
			add(host, entry.Address)
		}
	}
	return nil
}

// HomeDNSZoneSite is what one server reports for the zone Techstack
// assembles: its role, its own domain and LAN address, and on a joined server
// its node and the names the Owner approved for it.
type HomeDNSZoneSite struct {
	Role    string `json:"role"`
	Domain  string `json:"domain"`
	Address string `json:"address"`
	// ServerRef is a joined server's owner reference, the key of its join.
	ServerRef string   `json:"serverRef,omitempty"`
	Hostnames []string `json:"hostnames,omitempty"`
	// AuthorityDomain is the homelab domain a joined server's bundle names.
	AuthorityDomain string `json:"authorityDomain,omitempty"`
}

// LoadHomeDNSZoneSite reads this server's zone site from verified custody.
func LoadHomeDNSZoneSite(root string) (HomeDNSZoneSite, error) {
	domain, address, err := localevidence.BasementLANDNSSite(root)
	if err != nil {
		return HomeDNSZoneSite{}, err
	}
	site := HomeDNSZoneSite{Role: "authority", Domain: domain, Address: address.String()}
	if homeJoined(root) {
		bundle, err := LoadHomeJoinedTrust(root)
		if err != nil {
			return HomeDNSZoneSite{}, err
		}
		site.Role, site.ServerRef, site.Hostnames, site.AuthorityDomain = "joined", bundle.Server.OwnerRef, bundle.Server.Hostnames, bundle.IdentityDomain
	}
	return site, nil
}
