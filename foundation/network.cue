// Package foundation - Network configuration schemas
package foundation

// #NetworkDefaults defines default network settings
#NetworkDefaults: {
	// Primary domain for the homelab. The local default is the single
	// device-enrolled, resolver-backed zone; it has no .localhost or mDNS alias.
	domain: string | *"lab.home"

	// Subnet for internal services
	subnet: string | *"172.20.0.0/16"

	// Network driver
	driver?: string

	// Gateway IP
	gateway?: string

	// MTU size
	mtu: int & >=1280 & <=9000 | *1500

	// Enable IPv6
	ipv6: bool | *false

	// DHCP for nodes
	dhcp: bool | *true
}

// #DNSConfig defines DNS settings
#DNSConfig: {
	// DNS servers
	servers: [...string] | *["1.1.1.1", "8.8.8.8"]

	// Search domains
	search: [...string] | *[]

	// Local DNS resolver
	localResolver: bool | *false

	// Local resolver port
	localResolverPort: uint16 | *53

	// DNS over HTTPS
	doh: bool | *false

	// DoH upstream
	dohUpstream?: string

	// Custom DNS records
	records?: [...#DNSRecord]
}

// #DNSRecord defines a custom DNS record
#DNSRecord: {
	name:    string
	type:    "A" | "AAAA" | "CNAME" | "TXT" | "MX" | *"A"
	value:   string
	ttl:     int | *300
	weight?: int
}

// #NTPConfig defines time synchronization
#NTPConfig: {
	// Enable NTP
	enabled: bool | *true

	// NTP servers
	servers: [...string] | *["time.cloudflare.com", "time.google.com"]

	// NTP client
	client: "systemd-timesyncd" | "chrony" | "ntp" | *"systemd-timesyncd"
}

// #VPNAddOnToolV1 names the supported VPN add-on tools (D-2026-11-02-12).
#VPNAddOnToolV1: "wireguard" | "headscale" | "pangolin" | "tailscale"

// #VPNAddOnV1 is the input of the optional VPN add-on (homelab identity
// target §8, D-2026-11-02-12). A VPN is never the default in any mode or kit
// and never the realization of private remote access: the standard reach
// paths keep working for every device and service, and the Owner switches the
// add-on on in addition to them. It carries network-level reach to the LAN
// listener, including local-only and LAN-bound privileged routes, SMB and
// NFS. Its peers are exactly the enrolled, unrevoked, unexpired devices: an
// enrollment rolls the peer out (D-2026-11-02-11) and revoking the device
// removes it. Device keys are generated on the device and never leave it.
#VPNAddOnV1: {
	enabled: bool | *false
	tool:    #VPNAddOnToolV1
	optIn:   "owner"
	peers: {
		source:     "enrolled-devices"
		identity:   "device-key-thumbprint"
		allowedIPs: "single-host"
		rollout:    "at-enrollment"
		removal:    "on-revoke-or-expiry"
	}
	routing: {
		routeScope:        "declared-services-only"
		allowDefaultRoute: false
		allowBroadLAN:     false
	}
	keyCustody: {
		nodePrivateKey:   "node-generated-local-custody"
		devicePrivateKey: "device-generated-never-transferred"
		renderedOutput:   "public-keys-only"
	}
	// A VPN peer is reach, never identity: requests through it still carry
	// the Homelab account and device proof the route's access policy asks for.
	identity: "access-policy-unchanged"

	// The peer service is how an enrolled device receives its peer in the
	// same enrollment: the device client, holding its first credential, asks
	// the Authority's host for its peer with the device credential and a DPoP
	// proof, joins with the tool's own client and ends its enrollment with a
	// probe through the VPN, which binds a coordinated tool's node to the
	// device. A pending (away) enrollment gets no peer until it is confirmed.
	// The service runs on the Authority's host beside the tool's custody,
	// never in the core container. Devices reach it on the node site address
	// at home, through the VPN, and away at the Owner-declared endpoint host
	// (WireGuard, Headscale); its certificate is issued by step-ca.
	peerService: {
		listener:       "authority-host"
		port:           9444
		transport:      "tls-step-ca"
		authentication: "device-credential-dpop"
		// A coordinated node that no active device bound within this time
		// is deleted, so a peer never outlives an unfinished enrollment.
		unboundGraceSeconds: 900
	}
	// Every route a peer receives is the node site address as a single host:
	// no subnet, no default route, no other device.
	reach: {
		destination: "node-site-address"
		peerToPeer:  false
		// A local-only homelab (lab.home) hands its peers the StackKits
		// resolver for the homelab domain: an Android device, which has no
		// per-domain DNS, reaches lab.home through the add-on (target §5).
		localOnlyDNS: "site-resolver-for-homelab-domain"
	}

	// The self-hosted WireGuard tool: one interface on the Authority whose
	// node key stays in local custody.
	if tool == "wireguard" {
		wireguard: {
			interfaceName: "wg-stackkit"
			transport:     "udp"
			listenPort:    uint16 & >=1024 | *51820
			// One /24 pool: the node holds the first host address, devices
			// receive one /32 each from the rest of the pool.
			addressPool:                "10.8.0.0/24"
			nodeAddress:                "10.8.0.1"
			persistentKeepaliveSeconds: int & >=10 & <=120 | *25
			endpointDiscovery:          "owner-declared"
			// wg reads the node key from its custody path. Where the
			// distribution confines wg to /etc/wireguard (Ubuntu 26.04's
			// enforced AppArmor profile), the add-on adds exactly that one read
			// rule to the profile's site-local include and reloads it; the
			// profile stays enforced and the key stays in custody.
			nodeKeyDelivery: "custody-path-confinement-grant"
		}
	}
	if tool != "wireguard" {
		wireguard?: _|_
	}

	// Headscale: the self-hosted Tailscale coordination server on the
	// Authority. The Authority joins as a tagged node that routes only its own
	// site address; each device joins with a single-use pre-auth key.
	if tool == "headscale" {
		headscale: #VPNCoordinatedToolV1 & {
			coordination:      "self-hosted-on-authority"
			controlEndpoint:   "owner-declared-through-peer-service"
			deviceJoin:        "single-use-pre-auth-key"
			deviceJoinSeconds: 600
		}
	}
	if tool != "headscale" {
		headscale?: _|_
	}

	// Tailscale: the Owner's tailnet on Tailscale's coordination server. The
	// Owner supplies an OAuth client (scopes auth_keys and devices:core for
	// the two tags); without it the tool stays unavailable.
	if tool == "tailscale" {
		tailscale: #VPNCoordinatedToolV1 & {
			coordination:      "owner-tailscale-account"
			controlEndpoint:   "tailscale-control-plane"
			deviceJoin:        "single-use-pre-auth-key"
			deviceJoinSeconds: 600
			credential:        "owner-oauth-client"
		}
	}
	if tool != "tailscale" {
		tailscale?: _|_
	}

	// Pangolin: the Owner's Pangolin server (self-hosted on a public server,
	// as upstream deploys it, or Pangolin Cloud). The Authority runs Newt as
	// the site connector, dialling out; each device is one machine client
	// with access to exactly the site address resource.
	if tool == "pangolin" {
		pangolin: {
			coordination:    "owner-pangolin-server"
			siteConnector:   "newt"
			// Newt runs on its own bridge, which reaches only the site address,
			// its Pangolin server and the internet.
			siteConnectorNetwork: "own-bridge-site-address-only"
			initiation:      "home-outbound"
			deviceJoin:      "machine-client-per-device"
			deviceClient:    "olm"
			credential:      "owner-organization-api-key"
			resource:        "site-address-host-resource"
			removalOnRevoke: "delete-client"
		}
	}
	if tool != "pangolin" {
		pangolin?: _|_
	}
}

// #VPNCoordinatedToolV1 is the shared shape of the Tailscale-protocol tools
// (Headscale and Tailscale): the Authority is a tagged node that advertises
// only its site address, devices are tagged nodes that may reach only that
// address, and revocation deletes the device's node.
#VPNCoordinatedToolV1: {
	coordination:      string
	controlEndpoint:   string
	deviceJoin:        "single-use-pre-auth-key"
	deviceJoinSeconds: int & >=60 & <=3600
	credential?:       string
	nodeTag:           "tag:stackkit-node"
	deviceTag:         "tag:stackkit-device"
	nodeInterface:     "tailscale0"
	subnetRoute:       "site-address-host-route"
	binding:           "probe-source-address-whois"
	removalOnRevoke:   "delete-node"
}

// #VPNConfig defines VPN/overlay network settings of the legacy v1 StackKit
// schema; its tools are the VPN add-on tools (#VPNAddOnV1).
#VPNConfig: {
	// VPN enabled
	enabled: bool | *false

	// VPN type
	type: #VPNAddOnToolV1 | "none" | *"none"

	// VPN subnet
	subnet?: string

	// VPN port
	port?: uint16

	// Headscale-specific
	headscale?: {
		serverUrl: string
		authKey?:  =~"^secret://"
		namespace: string | *"default"
		exitNode:  bool | *false
		advertiseRoutes?: [...string]
	}

	// WireGuard-specific
	wireguard?: {
		privateKey: =~"^secret://"
		publicKey:  string
		endpoint?:  string
		peers?: [...#WireGuardPeer]
	}
}

// #WireGuardPeer defines a WireGuard peer
#WireGuardPeer: {
	publicKey: string
	endpoint?: string
	allowedIPs: [...string]
	keepAlive?: int
}

// #ProxyConfig defines HTTP/HTTPS proxy settings
#ProxyConfig: {
	// Enable proxy
	enabled: bool | *false

	// HTTP proxy
	http?: string

	// HTTPS proxy
	https?: string

	// No proxy list
	noProxy: [...string] | *["localhost", "127.0.0.1", "::1"]
}
