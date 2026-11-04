// Package vpn - the tools of the optional VPN add-on.
//
// This file is the single source of the tool catalog: the stackkit CLI embeds
// it (addons/vpn/embed.go) and runs exactly the images pinned here. It imports
// nothing so the CLI can evaluate it on its own; addon.cue binds it to the
// foundation contract #VPNAddOnV1.
//
// The four tools are alternatives; none is a default in any mode or kit. Each
// is upstream-faithful: StackKits runs the upstream image or the
// distribution's package unmodified and configures it through the upstream
// interfaces (wg-quick, the headscale CLI, the Tailscale API, the Pangolin
// Integration API).
package vpn

#ImagePin: {
	// The tag is informational; the digest is what runs.
	ref:    =~"^[a-z0-9.-]+(/[a-z0-9._-]+)+:[A-Za-z0-9._-]+$"
	digest: =~"^sha256:[0-9a-f]{64}$"
}

#Licence: {
	spdx:   string & !=""
	source: =~"^https://"
	note?:  string
}

#Component: {
	// container: StackKits runs the pinned image on the Authority.
	// host-package: the distribution's package (wireguard-tools); StackKits
	//   bundles nothing and runs the host's own binaries.
	// device-client: runs on the person's device, installed by the person
	//   from upstream; StackKits only hands it its peer.
	// external-service: the Owner's own service or account.
	kind:    "container" | "host-package" | "device-client" | "external-service"
	licence: #Licence
	if kind == "container" {
		image: #ImagePin
	}
	if kind != "container" {
		image?: _|_
	}
}

#Tool: {
	id:          "wireguard" | "headscale" | "tailscale" | "pangolin"
	displayName: string
	// self-hosted tools need nothing outside the homelab; coordinated tools
	// stay unavailable without their authenticated coordination path.
	coordination: "none" | "self-hosted" | "external-account"
	// What the Owner must supply before the tool can be switched on.
	ownerInputs: [...string]
	components: [string]: #Component
}

#Tools: [ID=string]: #Tool & {id: ID}
#Tools: {
	wireguard: {
		displayName:  "WireGuard"
		coordination: "none"
		ownerInputs: ["endpoint (public host:port that reaches UDP 51820 on the Authority)"]
		components: {
			"wireguard-tools": {kind: "host-package", licence: {spdx: "GPL-2.0-only", source: "https://git.zx2c4.com/wireguard-tools"}}
			"wireguard-client": {kind: "device-client", licence: {spdx: "LicenseRef-WireGuard-Clients", source: "https://www.wireguard.com/install/", note: "The official app of each platform under its own licence (wireguard-tools GPL-2.0-only, Android Apache-2.0, Apple and Windows MIT)."}}
		}
	}
	headscale: {
		displayName:  "Headscale"
		coordination: "self-hosted"
		ownerInputs: ["endpoint (host:port that reaches the peer service, which carries the Headscale control endpoint)"]
		components: {
			headscale: {
				kind:    "container"
				image:   {ref: "docker.io/headscale/headscale:0.29.7", digest: "sha256:11f81665e842809db9fe3006a38c56a66e968baa5abd4bac498e86445572b970"}
				licence: {spdx: "BSD-3-Clause", source: "https://github.com/juanfont/headscale/tree/v0.29.7"}
			}
			"tailscale-node": {
				kind:    "container"
				image:   {ref: "docker.io/tailscale/tailscale:v1.104.4", digest: "sha256:ed6480b33c6eb67c9cbd203ed42b9fb57e361986725b6856def71b7a3d9e276c"}
				licence: {spdx: "BSD-3-Clause", source: "https://github.com/tailscale/tailscale/tree/v1.104.4"}
			}
			"tailscale-client": {kind: "device-client", licence: {spdx: "BSD-3-Clause", source: "https://tailscale.com/download", note: "Open-source client; the GUI apps on some platforms carry Tailscale's own terms."}}
		}
	}
	tailscale: {
		displayName:  "Tailscale"
		coordination: "external-account"
		ownerInputs: ["OAuth client of the Owner's tailnet (scopes auth_keys, devices:core and devices:routes, tags tag:stackkit-node and tag:stackkit-device)", "tailnet policy that lets tag:stackkit-device reach only the site address"]
		components: {
			"tailscale-node": {
				kind:    "container"
				image:   {ref: "docker.io/tailscale/tailscale:v1.104.4", digest: "sha256:ed6480b33c6eb67c9cbd203ed42b9fb57e361986725b6856def71b7a3d9e276c"}
				licence: {spdx: "BSD-3-Clause", source: "https://github.com/tailscale/tailscale/tree/v1.104.4"}
			}
			"tailscale-coordination": {kind: "external-service", licence: {spdx: "LicenseRef-Tailscale-Terms", source: "https://tailscale.com/terms", note: "Tailscale's hosted coordination server under the Owner's own account."}}
			"tailscale-client": {kind: "device-client", licence: {spdx: "BSD-3-Clause", source: "https://tailscale.com/download", note: "Open-source client; the GUI apps on some platforms carry Tailscale's own terms."}}
		}
	}
	pangolin: {
		displayName:  "Pangolin"
		coordination: "external-account"
		ownerInputs: ["Pangolin server endpoint (self-hosted on a public server, or Pangolin Cloud)", "organization API key", "Newt site ID and secret of a site for this Authority"]
		components: {
			newt: {
				kind:    "container"
				image:   {ref: "docker.io/fosrl/newt:1.19.1", digest: "sha256:c89f01cb387bf6c598959036cbf56306b068a43e8784345bee7398b7af8ee2e1"}
				licence: {spdx: "AGPL-3.0-only", source: "https://github.com/fosrl/newt/tree/1.19.1"}
			}
			"pangolin-server": {kind: "external-service", licence: {spdx: "AGPL-3.0-only", source: "https://github.com/fosrl/pangolin", note: "Community edition; files marked with the Fossorial Commercial License header are not AGPL."}}
			olm: {kind: "device-client", licence: {spdx: "AGPL-3.0-only", source: "https://github.com/fosrl/olm"}}
		}
	}
}
