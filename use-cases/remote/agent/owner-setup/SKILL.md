---
name: remote-owner-setup
description: Verify the custodied owner of the applied database-free Apache Guacamole workload.
---

# Remote owner login

After the existing standalone lifecycle applies Remote, use:

```sh
stackkit setup remote --owner-approve --json
```

The explicitly selected Guacamole 1.6.0 static XML variant has no database.
Its private user mapping binds the existing `owner` login to the workload's
local credential custody and one explicitly configured connection. Setup
authenticates that owner through the admitted application API and records the
existing Plan-bound result. It does not create or reset an account, replace
connection credentials, or guess a fleet host. Do not print the user mapping,
passwords, private keys, session tokens or credential files.

Select the target protocol, host and port explicitly before generation. For
SSH, supply the exact OpenSSH `known_hosts` entry for that same host: the host
name at port 22, or `[host]:port` for a nondefault port. A key for another host
or port does not authorize this connection. Preserve the existing pin and
stop on a mismatch; do not discover or substitute a credential to make login
pass. RDP and VNC also require their own explicitly authorized targets.

Use the declared protected HTTPS `remote` route. The guacd daemon stays on the
internal workload network; do not expose its port. Guacamole is the session
gateway, not an operating-system desktop provisioner. The existing Generate
handoff equips the StackKits lifecycle MCP and this skill. Guacamole's native
APIs own application sessions and connections; no native product MCP is
declared.

The login check revokes its temporary application session after observation,
including on failure. Verify revocation before reporting setup complete. For
an authorized session probe, read a task-owned marker through the selected
connection, disconnect, reconnect, and read it again without printing secrets.
Close and revoke the probe session afterward. A successful login alone does
not prove remote transport, reconnect, target permissions, backup or restore.

The owned SSH fixture must provide observed session and reconnect evidence
before SSH is reported as verified. No current receipt or support claim is
established by this skill. The current public0.53.2 VNC module probe observed the upstream password
challenge, a rendered frame and actual target input after connect and reconnect.
Its temporary application token was revoked and independently rejected. This
nil-route development boundary does not establish Core/private HTTPS, PocketID,
household or physical-device acceptance. RDP was additionally observed against an owned xrdp target after its public
fixture certificate was explicitly trusted in that owned guacd container. The
untrusted certificate was rejected first. This added fixture trust is not
native CA delivery, Windows/device support or full Core acceptance. Correct the
reported cause without replacing custody, then resume a pending setup with
`--operation-id ID`.

Pinned upstream contracts:
[Guacamole 1.6.0 token creation and revocation](https://github.com/apache/guacamole-client/blob/0537c89cd783681b986ff8f8c4e0b97ec6873371/guacamole/src/main/java/org/apache/guacamole/rest/auth/TokenRESTService.java),
[static user mapping](https://github.com/apache/guacamole-client/blob/0537c89cd783681b986ff8f8c4e0b97ec6873371/guacamole/doc/example/user-mapping.xml),
and the official [configuration and connection guide](https://guacamole.apache.org/doc/gug/configuring-guacamole.html).
The static mapping is an explicit runtime selection, not a fallback from
another authentication mode.

## VNC destination feedback

Keep destination credentials outside the public Plan and XML connection. The
upstream client requests the VNC password through its required-argument stream;
provide it only from the authorized destination custody and keep it in memory.
Do not replace this challenge with credentials in generated parameters, tool
arguments or logs. Verify a frame and an actual input effect on the destination,
then disconnect and repeat an input effect after reconnect. Mere client state
or browser transport does not prove desktop control. Revoke the application
session afterward and observe rejection before returning.

The VNC development observation is bound to the checksum-verified published
archive and its public source 3d71bfe9dc6e499e86ef0a3726bb9fffc0fe883e. It used an isolated Ubuntu26.04
TigerVNC/Xterm target with no exposed host port or database. This is the VNC
module boundary only; it neither replaces the owning released-lifecycle
producer nor marks Remote supported. Preserve actual target identity and
credential choices. RDP trust/identity is a separate boundary; this skill does
not authorize ignoring its certificate validation.

## RDP destination feedback

Keep the target identity and certificate verification explicit. Never infer a
trusted RDP peer from a browser connected/sync state; observe the actual desktop
and its input effects. The untrusted RDP fixture returned a temporary client
state before guacd reported certificate validation failure and refused TLS.
Do not enable ignore-cert or implicit trust-on-first-use to make a probe pass.

For an admitted target, use the upstream authentication prompt or actual RDP
login UI. Target passwords remain outside public Plan/XML and logs. Observe an
actual destination effect, disconnect/reconnect and verify a second effect,
then revoke and verify rejection of the temporary Guacamole token.

The RDP development observation records actual public 0.53.2 behavior
with fixture-only public certificate trust, preserved private key, non-owner
Unix desktop and no database. Only that public certificate entered the owned
guacd system trust store. It did not change host/company/C CA authority or
prove automated production trust delivery. Fixture graphics and IPC repairs
are recorded separately from product behavior; no product code was changed.

## Resource profiles

The selected module compute profile governs a single aggregate CPU/RAM envelope
shared equally across the two resident daemons. Both remain present and use the
same upstream authentication, SSH/RDP/VNC protocols, destination prompts and
normal input/reconnect behavior. Profile choice never changes application
features, trust policy or credential custody. Existing v2 artifacts retain
unbounded allocations; a new v3 artifact needs its exact selected profile.

Allocation is policy, measured combined demand is observation, and physical
small-device/high-profile/client-capacity acceptance is separate. A higher cap
alone does not demonstrate a performance benefit or a simultaneous-client SLA.
The same existing CLI/State Console fit engine includes the whole host floor;
a per-container cap is not a small-host support claim.
