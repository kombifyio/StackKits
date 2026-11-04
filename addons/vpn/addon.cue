// Package vpn - the optional VPN add-on (homelab identity target §8, §11;
// D-2026-11-02-11, D-2026-11-02-12).
//
// A VPN is never the default in any mode or kit and never the realization of
// private remote access: the standard reach paths (kombify.me, the Owner's
// Cloudflare Tunnel, the public edge) keep working for every device, and the
// Owner switches the add-on on, with one of four alternative tools, in
// addition to them. It carries network-level reach to the node site address
// (local-only and LAN-bound routes, SMB, NFS). Its peers are exactly the
// enrolled, unrevoked, unexpired devices: an enrollment rolls the device's
// peer out, revoking the device (or removing its person) removes it and the
// live interface or the tool's coordination server drops it within the
// revocation target.
//
// The Owner switches it on at the Authority's console:
//
//	sudo stackkit vpn enable --tool wireguard --endpoint vpn.example.net:51820
//
// The add-on runs on the host, beside its custody: never in the core container
// and never as a StackSpec default. See README.md.
package vpn

import "github.com/kombifyio/stackkits/foundation"

#AddonCatalogMetadata: {
	name:        "vpn"
	displayName: "VPN"
	version:     "1.0.0"
	layer:       "NETWORK"
	description: "Optional network-level reach to the Authority for enrolled devices: WireGuard, Headscale, Tailscale or Pangolin; never a default."
	optIn:       "owner"
	default:     false
}

// #Config is one Owner opt-in: the foundation contract with its tool and the
// tool's pinned components.
#Config: foundation.#VPNAddOnV1 & {
	_addon: #AddonCatalogMetadata
	tool:   foundation.#VPNAddOnToolV1
	_tool:  #Tools[tool]
}
