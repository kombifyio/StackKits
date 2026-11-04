# VPN add-on

An optional, network-level path from an enrolled device to the Authority, in
addition to the standard reach paths of the homelab identity target (§8). It
is never a default in any mode or kit: it exists only after the Owner switches
it on at the Authority, and a VPN is never the realization of private remote
access. A peer is reach, not identity: every request through it still carries
the sign-in and device proof its route asks for.

Contract: `#VPNAddOnV1` in `foundation/network.cue`. Tool catalog with pinned
images and licences: `tools.cue` (the CLI runs exactly these pins).

## Tools

One tool at a time, chosen by the Owner. Each runs upstream-faithful: the
distribution's `wireguard-tools`, or the upstream image pinned by digest.

| Tool | Runs on the Authority | The Owner supplies | Device client |
| --- | --- | --- | --- |
| WireGuard | host `wireguard-tools`, interface `wg-stackkit` (UDP 51820) | `--endpoint host:port` that reaches UDP 51820; the peer service is `host:9444` | any WireGuard client with its own key pair |
| Headscale | `headscale` and a Tailscale node (`tailscale0`) | `--endpoint host:port` that reaches TCP 9444 (the peer service carries the control endpoint and the embedded DERP relay); UDP 3478 (STUN) and 41641 for direct paths | the stock Tailscale client |
| Tailscale | a Tailscale node (`tailscale0`) in the Owner's tailnet | `--tailnet` and an OAuth client (scopes `auth_keys`, `devices:core`, `devices:routes`; owner of `tag:stackkit-node` and `tag:stackkit-device`) and a tailnet policy that lets `tag:stackkit-device` reach only the site address | the stock Tailscale client |
| Pangolin | Newt, dialling out to the Owner's Pangolin server | the server origin, its Integration API URL, organization, the site's Newt ID and secret, an organization API key (`<keyId>.<secret>`; clients and site resources), optionally the server's CA | the stock Olm client |

The Headscale and Tailscale tools need the host's Tailscale node for the
add-on alone: an Authority that already runs its own Tailscale is refused. A
coordinated tool stays unavailable without its authenticated coordination
path: a missing or refused credential fails `enable`, and the add-on stays
off.

## Owner console (root, at the Authority)

```sh
sudo stackkit vpn enable --tool wireguard --endpoint vpn.example.net:51820
sudo stackkit vpn status
sudo stackkit vpn reconcile
sudo stackkit vpn disable
```

`enable` records the opt-in, starts the tool, registers the Authority as the
tool's node that routes only its site address (`/32`), confines the tool's
interface to the site address with an nftables guard, starts
`stackkit-vpn.service` (the peer service and the reconciler) and opens exactly
the tool's public ports in the StackKits Home firewall, which otherwise admits
only private sources (WireGuard: UDP 51820 and TCP 9444; Headscale: TCP 9444,
UDP 3478 and 41641; Tailscale: UDP 41641; Pangolin: none, Newt dials out).
`disable` removes every peer from the tool, the tool's runtime, the service
and those ports. Tool
secrets are read from files into the add-on's custody
(`.stackkit/custody/vpn-addon/`, 0600) and never printed.

## Peers at enrollment

The device client, holding the first credential its enrollment returned,
asks the peer service for its peer right away, so the device receives it in
the same enrollment. Every call carries the device credential
(`Authorization: DPoP …`) and a DPoP proof for exactly its method and URL; a
pending away enrollment has no credential and gets no peer.

| Call | Body | Answer |
| --- | --- | --- |
| `GET https://<site>:9444/api/v1/vpn` | | the tool and this device's peer |
| `POST https://<site>:9444/api/v1/vpn/peer` | WireGuard: `{"publicKey": "<the device's own key>"}`; otherwise `{}` | WireGuard: its address and a wg-quick configuration without the private key; Headscale and Tailscale: a single-use join key (10 minutes); Pangolin: a machine client's ID and secret. Secrets appear once and are never stored. |
| `POST https://<site>:9444/api/v1/vpn/peer/confirm` | `{}`, sent through the VPN | `connected`; binds a Headscale or Tailscale node to the device |

The confirmation is the enrollment's probe from the device's own vantage. The
service's certificate comes from the homelab's step-ca; native clients pin
the root the enrollment bootstrap names and the server name
`stackkit-home-vpn.internal`. Away, WireGuard and Headscale devices reach the
service at the Owner-declared endpoint host; Tailscale and Pangolin devices
receive their peer at home or through another enabled path.

A homelab declared `local-only` (`lab.home`) hands its peers the StackKits
resolver on the site address: WireGuard as the tunnel's DNS server, Headscale
as split DNS for the homelab domain. An Android device, which has no
per-domain DNS, reaches `lab.home` this way.

## Revocation

Revoking a device, undoing its enrollment or removing its person (revocation
registry artifact `homelab.vpn_peer`) removes its peer: the WireGuard peer
leaves the interface (`wg syncconf`), a Headscale or Tailscale node and its
join key are deleted, a Pangolin client is deleted. The reconciler watches
the device registry and applies a change at once, and passes over every
device at least every 30 seconds for expiries, inside the 60 second
revocation target. A node that no active device bound within 15 minutes is
deleted too, so a peer never outlives an unfinished enrollment. A failed
deletion stays in the peer ledger and is retried.

## Confinement

A peer reaches only the node site address (and the WireGuard node address):
the guard (`stackkit_vpn_guard`, loaded at boot by `stackkit-vpn-guard.service`
before the container engine and wg-quick, and refreshed on every reconcile)
drops everything else that arrives through the tool, and no peer reaches
another peer. On `tailscale0` only the addresses of bound peers of active
devices pass; any other tailnet node reaches nothing but the peer service's
probe. Newt runs on its own bridge (`br-stackkit-vpn`), which may reach the
site address, its Pangolin server and the internet, never another private
address. Headscale approves no route automatically; the Authority node's
single host route is approved by its node ID. Ubuntu 26.04 enforces AppArmor profiles for `wg`
and `wg-quick` that admit only `/etc/wireguard`; the add-on keeps the node
key in custody and adds exactly one read rule for it to
`/etc/apparmor.d/local/wg`, the profile's site-local include, in a marked
block it alone manages, then reloads the profile. The profile stays enforced.
