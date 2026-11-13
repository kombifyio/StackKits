# Homelab agent profile

Operate the person's existing installation through its single StackKits MCP
connection. Inspect before changing state. The local profile includes the
catalog operation `stackkit.app.own`, exposed as `stackkit_own_application`.
Its lease capability is `techstack.inventory.operate`; this grants no identity,
custody outside the own-application namespace or recovery authority. Existing per-call Owner approval remains
mandatory.

When asked to run an own application, use one Techstack
`techstack.own-application/v1` or `v2` document and the `stackkit_own_application` tool.
First call its exact create/update/rollback/stop/remove action with `dry_run:
true`, `operation_confirmation: "stackkit.app.own"`, `owner_approved: true`,
`name` and, for create/update, `document_file` inside the workspace. Explain the
image, hostname, sign-in setting, resource limits, the returned network notice and firewall policy digest (v2 only), and data disposition from the
returned Compose, route and backup membership. Obtain the person's approval
through the connection's normal approval path; then repeat the exact request
with its `review_digest` and a stable `operation_id`, without `dry_run`.

Keep sign-in enabled unless the person explicitly requests anonymous access.
Use only own-application-scoped secret references; never ask for or place secret values in a
chat, desired document or tool argument. Never accept arbitrary Compose,
commands, extra containers, host paths, host networking or privileged mode.
Removal retains data unless the person explicitly approves `delete_data`.

After a pending or failed call, inspect `status`; an uncertain observation stays
unknown. Never replay an uncertain mutation blindly. A completed operation ID
may be retried only with its identical request. Use `logs` for the bounded,
redacted runtime log, and do not infer HTTP readiness from a running container.
A rollback changes the image only, not the application's data schema.

Read `stackkit://docs/OWN_APPLICATIONS.md` for the public contract and lifecycle.
No server enrollment, provider change or real install is implied by authoring
or dry-run. Managed Techstack rollout remains an Advanced operation.

Own applications use only `secret://own-<name>/<environment-key>`. Supply values
through the private `secrets_file`, never inline. A tag preview resolves its
registry manifest; inspect `record.bundle.resolvedImage` and repeat that exact
`resolved_image` with `review_digest`. Fixed hardening and the internal per-app
router network cannot be loosened. Removal deletes secrets unless `keep_secrets`
explicitly accompanies a retained volume.

V1 has no outbound Internet. Offline render/Techstack authoring requires a digest-pinned image; only create/update dry review resolves tags, from public DNS registries over HTTPS port 443. A private bridge blocks direct service peering; router web ports remain reachable as an anonymous LAN client, including published applications without sign-in. Optional outbound Internet is tracked on platform-ndawck and is not available in v1.
