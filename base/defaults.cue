// Package base_kit - Named release default profile.
//
// NOTE: This file previously carried a legacy variant/smart-default system
// (#ServiceVariant, #SmartDefaults, #ComputeTierDetector, #DomainConfig,
// #DefaultPorts, #DefaultVolumes, #LocalHostsConfig). None of it was
// consumed by the composition engine, and parts contradicted the product
// contract (hosts-file generation violates Golden Rules §1.10; service
// lists predated the platform baseline). Compute-tier resolution and
// service enablement live in the Go composition engine
// (internal/cue/bridge.go); per-service subdomain naming is declared on
// each service's `subdomain` triple in services.cue.
package base

// #BaseKitReleaseDefault is the named product default for normal BaseKit app
// composition. PaaS and reverse-proxy selection are resolved from context,
// domain, and explicit overrides by the StackSpec resolver; this profile must
// not pin Dokploy or Coolify directly.
#BaseKitReleaseDefault: {
	context: "local"
	domain:  "home.localhost"
	services: {
		homepage:      true
		"uptime-kuma": true
		whoami:        true
		vaultwarden:   true
		jellyfin:      false
		immich:        true
		files:         true
	}
}
