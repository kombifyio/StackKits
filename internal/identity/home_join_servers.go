package identity

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// The Authority's record of its joined servers: written by its host when the
// Owner approves a join, read by the verification-state route and the Hub.
type homeJoinedServer struct {
	Server             HomeJoinServer  `json:"server"`
	ClientKey          jose.JSONWebKey `json:"clientKey"`
	IntermediateSerial string          `json:"intermediateSerial"`
	ApprovedAt         time.Time       `json:"approvedAt"`
	ExpiresAt          time.Time       `json:"expiresAt"`
	Removed            bool            `json:"removed,omitempty"`
	RemovedAt          *time.Time      `json:"removedAt,omitempty"`
}

type homeJoinedServers struct {
	Schema  string             `json:"schema"`
	Servers []homeJoinedServer `json:"servers"`
}

// HomeJoinedServer is one joined server as the Authority's console lists it.
type HomeJoinedServer struct {
	SiteRef    string     `json:"siteRef"`
	NodeRef    string     `json:"nodeRef"`
	OwnerRef   string     `json:"ownerRef"`
	Hostnames  []string   `json:"hostnames"`
	ApprovedAt time.Time  `json:"approvedAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	Removed    bool       `json:"removed"`
	RemovedAt  *time.Time `json:"removedAt,omitempty"`
}

func loadHomeJoinedServers(root string) (homeJoinedServers, error) {
	raw, err := readHomePrivate(root, homeJoinedServersPath, homeJoinedStateMax)
	if err != nil {
		return homeJoinedServers{Schema: homeJoinedServerSchema}, err
	}
	var servers homeJoinedServers
	if decodeHomeJoinStrict(raw, &servers) != nil || servers.Schema != homeJoinedServerSchema {
		return homeJoinedServers{Schema: homeJoinedServerSchema}, errors.New("identity: unreadable joined server registry")
	}
	return servers, nil
}

// recordHomeJoinedServer records (or replaces) a joined server on the
// Authority after the Owner approved its join.
func recordHomeJoinedServer(root string, request HomeJoinRequest, intermediate *x509.Certificate, now time.Time) error {
	servers, err := loadHomeJoinedServers(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	servers.Servers = slices.DeleteFunc(servers.Servers, func(entry homeJoinedServer) bool {
		return entry.Server.OwnerRef == request.Server.OwnerRef
	})
	if len(servers.Servers) >= homeJoinMaxServers {
		return ErrHomeRegistryFull
	}
	servers.Servers = append(servers.Servers, homeJoinedServer{
		Server: request.Server, ClientKey: request.ClientKey, IntermediateSerial: intermediate.SerialNumber.Text(16),
		ApprovedAt: now.UTC(), ExpiresAt: intermediate.NotAfter.UTC(),
	})
	sort.Slice(servers.Servers, func(i, j int) bool { return servers.Servers[i].Server.OwnerRef < servers.Servers[j].Server.OwnerRef })
	raw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	return writeHomePrivate(root, homeJoinedServersPath, raw, false)
}

// ListHomeJoinedServers lists the Authority's joined servers.
func ListHomeJoinedServers(root string) ([]HomeJoinedServer, error) {
	servers, err := loadHomeJoinedServers(root)
	if errors.Is(err, os.ErrNotExist) {
		return []HomeJoinedServer{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]HomeJoinedServer, 0, len(servers.Servers))
	for _, entry := range servers.Servers {
		out = append(out, HomeJoinedServer{SiteRef: entry.Server.SiteRef, NodeRef: entry.Server.NodeRef, OwnerRef: entry.Server.OwnerRef,
			Hostnames: entry.Server.Hostnames, ApprovedAt: entry.ApprovedAt, ExpiresAt: entry.ExpiresAt, Removed: entry.Removed, RemovedAt: entry.RemovedAt})
	}
	return out, nil
}

// RemoveHomeJoinedServer ends a joined server's trust at the Authority: its
// state reads are refused at once, so it opens no new browser session, and
// every credential it still accepts expires within the credential lifetime.
// Removal only removes access, so it needs no step-up. ref is the server's
// owner reference (every Basement server is node main of site home, so only
// the owner reference tells servers apart); a node reference is accepted
// only while exactly one current joined server carries it.
func RemoveHomeJoinedServer(root, ref string, now time.Time) error {
	servers, err := loadHomeJoinedServers(root)
	if err != nil {
		return ErrHomeDeviceNotFound
	}
	match := -1
	for index, entry := range servers.Servers {
		if !entry.Removed && entry.Server.OwnerRef == ref {
			match = index
		}
	}
	if match < 0 {
		for index, entry := range servers.Servers {
			if !entry.Removed && entry.Server.NodeRef == ref {
				if match >= 0 {
					return errors.New("identity: several joined servers are node " + ref + "; name the server by its owner reference")
				}
				match = index
			}
		}
	}
	if match < 0 {
		return ErrHomeDeviceNotFound
	}
	at := now.UTC()
	servers.Servers[match].Removed, servers.Servers[match].RemovedAt = true, &at
	raw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	return writeHomePrivate(root, homeJoinedServersPath, raw, false)
}

// homeJoinedLaunchHosts are the hostnames of every current joined server:
// the Hub signs launch proofs for them too.
func homeJoinedLaunchHosts(root string, now time.Time) []string {
	servers, err := loadHomeJoinedServers(root)
	if err != nil {
		return nil
	}
	hosts := []string{}
	for _, entry := range servers.Servers {
		if !entry.Removed && now.Before(entry.ExpiresAt) {
			hosts = append(hosts, entry.Server.Hostnames...)
		}
	}
	return hosts
}

const homeAuthorityHostsPath = ".stackkit/custody/home-verifier/authority-hosts.json"

// RecordHomeAuthorityHosts records the hosts this server's Access Gate
// serves (its route table), so a join never hands another server a
// certificate scope over one of them. The core writes it at start.
func RecordHomeAuthorityHosts(root string, hosts []string) error {
	hosts = normalizeHomeJoinHostnames(hosts)
	raw, err := json.Marshal(struct {
		Hosts []string `json:"hosts"`
	}{hosts})
	if err != nil {
		return err
	}
	return writeHomePrivate(root, homeAuthorityHostsPath, raw, false)
}

func loadHomeAuthorityHosts(root string) []string {
	raw, err := readHomePrivate(root, homeAuthorityHostsPath, homeJoinedStateMax)
	if err != nil {
		return nil
	}
	var record struct {
		Hosts []string `json:"hosts"`
	}
	if json.Unmarshal(raw, &record) != nil {
		return nil
	}
	return record.Hosts
}
