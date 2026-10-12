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
established by this skill. RDP/VNC and physical-device behavior remain
unverified until their own authorized probes provide evidence. Correct the
reported cause without replacing custody, then resume a pending setup with
`--operation-id ID`.

Pinned upstream contracts:
[Guacamole 1.6.0 token creation and revocation](https://github.com/apache/guacamole-client/blob/0537c89cd783681b986ff8f8c4e0b97ec6873371/guacamole/src/main/java/org/apache/guacamole/rest/auth/TokenRESTService.java),
[static user mapping](https://github.com/apache/guacamole-client/blob/0537c89cd783681b986ff8f8c4e0b97ec6873371/guacamole/doc/example/user-mapping.xml),
and the official [configuration and connection guide](https://guacamole.apache.org/doc/gug/configuring-guacamole.html).
The static mapping is an explicit runtime selection, not a fallback from
another authentication mode.
