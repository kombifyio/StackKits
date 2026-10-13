---
name: ntfy-live-notifications
description: Operate the explicitly selected node-local ntfy live relay and verify its real connected-client delivery without treating it as durable push.
---

# Live notifications

Confirm the verified Plan selects automation-notifications=ntfy-live and its pinned image. Preserve the existing Automation selection. The optional relay forwards only to currently connected clients; offline notifications are lost. It is not an automatic fallback for durable ntfy, mobile Web Push, household accounts or the backup-digest recipe. Those require actual application PostgreSQL/CA/egress/roles and recipient-sealed Wallet delivery.

Use the existing StackKits lifecycle on this node. The renderer binds an administrator-only private HTTPS route, never a public route. No native ntfy login, signup, message cache, Web Push or attachments is configured. PocketID/private HTTPS client acceptance remains separate; an inner HTTP health response does not prove it. Never enable a local or in-memory SQL cache/auth store to complete a probe.

Run the declared ntfy-live-delivery-check setup action through the installed workload's existing native transport. It subscribes to one fresh check topic, publishes a nonce, observes actual live receipt and checks that polling replays no message. The signed result describes delivery, not an upstream administrator account. No credentials file or onboarding-completion flag is needed. Do not publish user content in the delivery check.

Application features stay upstream-owned. No repository credentials, provider resources or another node belong to this action. Removal has no message dataset to retain or delete; retain native owner custody and signed lifecycle evidence under their existing rules. Standard is configured policy, not measured capacity or a named high benefit. No support grade follows without current-pin lifecycle receipts.
