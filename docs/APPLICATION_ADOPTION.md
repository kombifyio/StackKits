# Existing application adoption

StackKits takes over an existing application in one of two ways:

| Path | Container | Contract | Takes over |
| --- | --- | --- | --- |
| `stackkit application` (this page) | kept in place, same container ID | `home-assistant-container-in-place/v1` lifecycle profile | power control and source verification only |
| `stackkit app own --from-container` ("Stack it") | **recreated** as a new container (new ID, bounded downtime); the original is stopped and retained | `techstack.own-application/v4`, profile `own-application-adopted/v1` | the full own-application lifecycle: update (also to a newer tag), rollback, start, stop, restart, backup, logs, status |

"Stack it" works for any single container that fits its rules and is described
in [Run your own container](OWN_APPLICATIONS.md#stack-an-existing-container-v4).
It keeps the container's published host ports with their original bind
addresses, also when outbound Internet is off (`--no-outbound`): the ports then
run on the managed firewall bridge with an ingress-only ruleset that only
answers connections made to the application
([Published ports without outbound Internet](OWN_APPLICATIONS.md#published-ports-without-outbound-internet)).
After a host reboot such an application comes back through its boot-time
restore unit (systemd 244 or newer).
The in-place path below stays as it is for its supported profile.

### Plan row codes

Every row of a "Stack it" `inspect`, `plan` or `adopt` result
([`stackkit.own-application-adoption/v1`](../schemas/stackkit-own-application-adoption-v1.schema.json))
carries a stable machine `code` and its structured `values` (all strings) next
to the English `reason`. Clients translate and match by code and values; the
reason text may change between releases, the codes do not.

| Code | Status | Values |
| --- | --- | --- |
| `image_pinned_by_digest` | kept | `image`, `pinned` |
| `volume_copied_checksummed` | kept | `source`, `mountPath`, `madeWritable` |
| `data_size_measured` / `data_size_not_measured` | kept | `bytes`, `files`, `freeBytes` / `source` |
| `environment_empty_kept` | kept | `names` |
| `environment_values_to_custody` | changed | `names` |
| `secrets_classified` | kept | `names` |
| `http_port_routed` | kept | `port`, `candidates` |
| `port_kept` | kept | `hostIP`, `hostPort`, `containerPort`, `protocol` |
| `port_random_dropped`, `port_ipv6_only_dropped` | changed | `binding` |
| `port_router_owned_dropped` | changed | `binding`, `hostPort` |
| `outbound_none_kept`, `outbound_kept_firewalled`, `outbound_turned_off` | kept / changed | none |
| `restart_unless_stopped`, `restart_by_firewall_watcher` | changed | `from`, `to` |
| `own_private_network` | changed | `networks` |
| `hostname_route_with_sign_in` | changed | none |
| `container_recreated` | changed | `containerId` (12 characters) |
| `downtime_estimated` | changed | `seconds`, `maxSeconds` |
| `readiness_answered`, `readiness_wrong_status`, `readiness_no_answer`, `readiness_not_probed` | kept / changed | `port`, `httpStatus` |
| `healthcheck_from_image`, `cgroupns_daemon_default`, `read_only_root_dropped` | changed | none |
| `capabilities_fixed_policy` | changed | `dropped` |
| `host_override_dropped` | changed | `override` (extra-hosts, dns, sysctls, tmpfs, group-add, ulimits), `count` |
| `tmpfs_mount_dropped` | changed | `mountPath` |
| `timezone_mount_dropped` | changed | `mountPath`, `source` |
| `resources_bounded` | changed | `memoryMiB`, `cpus` |
| `data_volume_replaced` | changed | `volume` |

Refusals (status `refused`): `refused_already_managed` (`container`),
`refused_compose_project` (`project`, `service`),
`refused_shared_user_network` (`network`, `containers`),
`refused_local_only_image` and `refused_image_reference` (`image`),
`refused_registry_image` (`image`), `refused_custom_command` (`arguments`),
`refused_user_override` (`user`), `refused_host_network`,
`refused_container_network` (`mode`), `refused_privileged`,
`refused_added_capabilities` (`capabilities`), `refused_devices` (`count`),
`refused_shared_namespace` (`namespace`, `mode`), `refused_relaxed_security`
(`option`), `refused_volumes_from` (`containers`), `refused_custom_runtime`
(`runtime`), `refused_legacy_links` (`count`), `refused_host_user_namespace`,
`refused_docker_socket` (`mountPath`), `refused_bind_outside_data_roots`
(`mountPath`, `source`, `dataRoots`), `refused_mount_type` (`mountPath`,
`type`), `refused_multiple_data_mounts` (`count`), `refused_image_volume`
(`mountPath`), `refused_environment_entry`, `refused_multiline_environment`
(`name`), `refused_environment_value_size` (`name`, `limitBytes`),
`refused_environment_count` (`variables`, `secrets`, `limit`),
`refused_unknown_secret_env` (`name`), `refused_http_port_ambiguous`
(`candidates`), `refused_name` (`name`), `refused_existing_application_volume`
(`volume`), `refused_data_unmeasurable` (`source`), `refused_data_disk_space`
(`bytes`, `freeBytes`), `refused_contract`, `refused_review` and
`refused_review_outbound` (both carry the create review error as
`reason`; the outbound one suggests `--no-outbound`).

`stackkit application` adopts a supported existing native application without
recreating its container or bootstrapping native accounts. The first admitted
profile is Home Assistant Container 2026.9.4 at the exact CUE-owned image digest,
with one initialized, exclusive persistent `/config` mount, unchanged image
startup configuration and a published local listener. Host networking, shared
configuration, multi-container dependencies, custom integrations, external
recorders and unqualified images remain unsupported and unstacked.

The workspace needs its existing canonical StackSpec, resolved plan and local
Owner custody. Smart Home need not already be selected: inspection prepares its
proposed selection through the existing CUE authoring and resolver in memory,
at the Owner's exact local site and node. It preserves the running StackSpec and
plan files, and binds the proposal to their retained baseline. No generation or
Apply runs. Later operations deterministically derive the same authority and
refuse a changed baseline.

Sign in through native Home Assistant and place an existing current Owner grant
in a private workspace-relative file, default
`.stackkit/setup/home-assistant-owner.json`:

```json
{"accessToken":"<existing native Owner access token>"}
```

Restrict the file to its OS owner. Adoption does not initiate native sign-in,
issue tokens, reset passwords or replace accounts. An absent, expired or
unverified Owner grant returns actionable denial guidance.

```sh
stackkit application inspect smart-home --container-id <full-container-id> --json
stackkit application adopt smart-home --container-id <full-container-id> \
  --plan-digest <reviewed-plan-digest> --operation-id <stable-id> --owner-approve --json
stackkit application verify smart-home --json
stackkit application control smart-home --action restart \
  --operation-id <stable-id> --owner-approve --json
stackkit application release smart-home --operation-id <stable-id> --owner-approve --json
```

The existing lifecycle journal holds signed Owner evidence. The adopted scope
is container power control and source verification; native configuration,
accounts and persistent data stay native-owned. Verification checks the exact
container/image and current configuration/account digests. A dispatched power
operation is durably marked before execution; retries reconcile read-only and
never blindly repeat a restart. Fresh generation, Apply, setup and removal are
blocked while the binding remains active. Release relinquishes power-control
custody and retains the native running application and its data.
