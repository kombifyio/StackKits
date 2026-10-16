package dbx

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
)

// Coolify refuses outgoing connections to private and local addresses (S3
// endpoints, secret managers, webhooks) unless the address is allowed under
// Settings > Advanced. A storage or secret manager that worked on the old
// server only because of such an entry needs the same entry on the new one.

// readInternalHosts returns the allowed internal hosts of this Coolify (host
// names, IPs or CIDR ranges); nil when the setting does not exist.
func readInternalHosts(ctx context.Context, in *coolify.Instance) []string {
	raw, err := in.Scalar(ctx, "SELECT webhook_allowed_internal_hosts FROM instance_settings WHERE id = 0")
	if err != nil {
		return nil
	}
	return parseInternalHosts(raw)
}

func parseInternalHosts(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		list = strings.Split(raw, ",")
	}
	var out []string
	for _, e := range list {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// allowedBy reports whether host matches an entry (same name, same IP, or an
// IP inside a CIDR range).
func allowedBy(host string, entries []string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	ip, ipErr := netip.ParseAddr(host)
	for _, e := range entries {
		if strings.Contains(e, "/") {
			if p, err := netip.ParsePrefix(e); err == nil && ipErr == nil && p.Contains(ip.Unmap()) {
				return true
			}
			continue
		}
		if e == host {
			return true
		}
		if ipErr == nil {
			if eip, err := netip.ParseAddr(e); err == nil && eip.Unmap() == ip.Unmap() {
				return true
			}
		}
	}
	return false
}

func urlHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if h, _, err := net.SplitHostPort(u.Host); err == nil {
		return h
	}
	return u.Hostname()
}

// endpointOf returns what a row connects to: an S3 storage's endpoint or a
// secret manager's base URL.
func endpointOf(table string, row coolify.Row) string {
	switch table {
	case "s3_storages":
		s, _ := PlainString(row["endpoint"])
		return s
	case "integration_tokens":
		var meta map[string]any
		switch m := row["metadata"].(type) {
		case map[string]any:
			meta = m
		case string:
			_ = json.Unmarshal([]byte(m), &meta)
		}
		s, _ := meta["base_url"].(string)
		return s
	}
	return ""
}

// lookupHost resolves a host name on this server (replaced in tests).
var lookupHost = func(host string) []netip.Addr {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil
	}
	return addrs
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// internalAddr: an address Coolify only connects to when it is allowed
// (private, local, link-local or carrier-grade NAT).
func internalAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

// refused reports whether a Coolify with these allowed hosts refuses host,
// which resolves to addrs.
func refused(host string, addrs []netip.Addr, allowed []string) bool {
	if allowedBy(host, allowed) {
		return false
	}
	for _, a := range addrs {
		if internalAddr(a) && !allowedBy(a.Unmap().String(), allowed) {
			return true
		}
	}
	return false
}

// InternalHostWarnings lists the storages and secret managers of the backup
// that this server's Coolify would refuse to connect to: their address (or
// what their host name resolves to here) is private and not allowed under
// Settings > Advanced. A name that does not resolve here but was allowed on
// the old server is listed too. The ones this server already has are its own
// and are not checked.
func InternalHostWarnings(ex *Export, ts *TargetState) []string {
	var out []string
	for _, table := range []string{"s3_storages", "integration_tokens"} {
		for _, row := range ex.Tables[table] {
			if u, _ := row["uuid"].(string); u != "" {
				if _, ok := ts.Existing[table][u]; ok {
					continue
				}
			}
			host := strings.Trim(urlHost(endpointOf(table, row)), "[]")
			if host == "" {
				continue
			}
			var addrs []netip.Addr
			if ip, err := netip.ParseAddr(host); err == nil {
				addrs = []netip.Addr{ip}
			} else {
				addrs = lookupHost(host)
			}
			unknownHere := len(addrs) == 0 && allowedBy(host, ex.InternalHosts) && !allowedBy(host, ts.InternalHosts)
			if !refused(host, addrs, ts.InternalHosts) && !unknownHere {
				continue
			}
			why := "a private address"
			if allowedBy(host, ex.InternalHosts) || anyAllowed(addrs, ex.InternalHosts) {
				why = "a private address the old server allows"
			}
			out = append(out, fmt.Sprintf("%s %q connects to %s, %s - allow it here first (Coolify > Settings > Advanced, "+
				"allowed internal hosts), or Coolify refuses to connect to it", humanTable(table), row["name"], host, why))
		}
	}
	return out
}

func anyAllowed(addrs []netip.Addr, allowed []string) bool {
	for _, a := range addrs {
		if allowedBy(a.Unmap().String(), allowed) {
			return true
		}
	}
	return false
}
