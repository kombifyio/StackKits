// Package base - Network configuration schemas
package base

// #NetworkDefaults defines default network settings
#NetworkDefaults: {
	// Primary domain for the homelab
	domain: string | *"local"

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
	name:   string
	type:   "A" | "AAAA" | "CNAME" | "TXT" | "MX" | *"A"
	value:  string
	ttl:    int | *300
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

// #VPNConfig defines VPN/overlay network settings
#VPNConfig: {
	// VPN enabled
	enabled: bool | *false

	// VPN type
	type: "headscale" | "tailscale" | "wireguard" | "zerotier" | "none" | *"none"

	// VPN subnet
	subnet?: string

	// VPN port
	port?: uint16

	// Headscale-specific
	headscale?: {
		serverUrl:   string
		authKey?:    =~"^secret://"
		namespace:   string | *"default"
		exitNode:    bool | *false
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
	publicKey:  string
	endpoint?:  string
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
