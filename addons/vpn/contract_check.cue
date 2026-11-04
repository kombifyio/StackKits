// Package vpn - VPN add-on contract checks.
//
// Each _assert* unifies only when its property holds; `cue vet` fails
// otherwise.
package vpn

import "list"

// Never a default: choosing a tool does not switch the add-on on (the
// comparison resolves the default), and the catalog metadata says so. The
// tool itself has no default value: an opt-in names it.
_assertOffByDefault: true & ((#Config & {tool: "headscale"}).enabled == false)
_assertOffByDefaultWireGuard: true & ((#Config & {tool: "wireguard"}).enabled == false)
_assertOffByDefaultMetadata: true & (#AddonCatalogMetadata.default == false)

// Every tool is an alternative of the same contract and names its licences.
_assertFourTools: true & (len([for id, _ in #Tools {id}]) == 4)
_licensed: [for id, tool in #Tools for name, component in tool.components if component.licence.spdx != "" {id + "/" + name}]
_assertLicences: true & (len(_licensed) == len([for id, tool in #Tools for name, _ in tool.components {name}]))
_assertLicenceCount: true & list.MinItems(_licensed, 11)

// Every container StackKits runs is pinned by digest.
_containers: [for id, tool in #Tools for name, component in tool.components if component.kind == "container" {component}]
_assertContainersPinned: true & (len([for c in _containers if c.image.digest =~ "^sha256:[0-9a-f]{64}$" {c}]) == len(_containers))
_assertContainerCount: true & (len(_containers) == 4)

// Peers reach only the node site address; no default route, no subnet, no
// device-to-device reach, for every tool.
_wireguard: #Config & {tool: "wireguard", enabled: true}
_headscale: #Config & {tool: "headscale", enabled: true}
_tailscale: #Config & {tool: "tailscale", enabled: true}
_pangolin:  #Config & {tool: "pangolin", enabled: true}
_assertSingleHost: true & (_wireguard.peers.allowedIPs == "single-host" && _headscale.reach.destination == "node-site-address" && _pangolin.reach.peerToPeer == false)
_assertNoDefaultRoute: true & (_tailscale.routing.allowDefaultRoute == false)
_assertNoBroadLAN:     true & (_headscale.routing.allowBroadLAN == false)

// Removal follows revocation for every tool.
_assertRemovalWireGuard: true & (_wireguard.peers.removal == "on-revoke-or-expiry")
_assertRemovalHeadscale: true & (_headscale.headscale.removalOnRevoke == "delete-node")
_assertRemovalTailscale: true & (_tailscale.tailscale.removalOnRevoke == "delete-node")
_assertRemovalPangolin:  true & (_pangolin.pangolin.removalOnRevoke == "delete-client")
_assertUnboundGrace:     true & (_tailscale.peerService.unboundGraceSeconds == 900)

// Device keys never leave devices; the node key never leaves custody.
_assertDeviceKey: true & (_wireguard.keyCustody.devicePrivateKey == "device-generated-never-transferred")
_assertNodeKey:   true & (_wireguard.wireguard.nodeKeyDelivery == "custody-path-confinement-grant")
