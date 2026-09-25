// Package platformdeploy contains the StackKit boundary for PaaS delivery.
// OpenTofu owns platform installation; StackKit-owned/default L3 applications
// are PaaS-intended, while customer-installed applications outside the manifest
// are state-unmanaged by StackKit.
//
// Fallback executor per ADR-0045 §6: the in-house Komodo, Coolify and Dokploy
// HTTP adapters are retained until each platform's OpenTofu provider target
// has real-host parity; retirement is a P4 slice per platform.
package platformdeploy
