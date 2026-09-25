---
name: mail-server
description: Operate the owner's own Stalwart mail server installed by StackKits on a dedicated Cloud node - create the mail domain and first mailbox, optionally send through a relay, publish the printed DNS records, get a trusted certificate, connect Roundcube or mail apps - without handling mailbox, relay or administrator passwords in chat.
---

# Own mail server

StackKits installs Stalwart Mail Server on one dedicated Cloud node with a
fixed public IPv4 (ADR-0046 and its 2026-09-25 amendment). kombify runs no
mail server for customers; an owner who wants managed mail uses Paperwork.
The mail host name is the workload's route host, `mail-server.<domain>`:

| Port | Service |
| --- | --- |
| TCP 25 | SMTP: receiving mail from other servers, sending to them |
| TCP 465 | submissions (implicit TLS) for mail apps |
| TCP 587 | submission with STARTTLS for mail apps |
| TCP 993 | IMAPS |
| TCP 4190 | ManageSieve (filter rules) |
| HTTPS | web administration at `https://mail-server.<domain>` through the router |

The administrator is `admin` with the password in owner custody (slot
`admin-password`). It is Stalwart's fallback administrator; never print it.
StackKits never creates DNS records, port forwards or reverse DNS.

## Dedicated node

The mail server never shares a node with another application. Plan
resolution refuses a StackSpec where it lands on more than one node or next to
another application workload; platform services such as `cloud-core` are
fine.

- One Cloud node: initialize it with only the mail server,
  `stackkit init cloud-kit --use-case mail-server`. Adding another
  application use case to that node is refused.
- Several Cloud nodes: add a worker node for mail, set
  `workloads.mail-server.placement.nodeRefs` to that node only, and set the
  other applications' `placement.nodeRefs` to the other nodes.
- The node needs a fixed public IPv4. IPv6-only is refused because mail from
  IPv4-only senders would be lost. Setup checks that `mail-server.<domain>`
  has a public IPv4 address record.
- kombify-managed IONOS Cloud (DCD) mail nodes are provisioned by Techstack:
  a Basic Cube XS (1 vCPU, 2 GB, 60 GB), a reserved public IPv4, the PTR
  record through the IONOS Cloud DNS reverse-record API and a NIC firewall
  opening 25, 465, 587, 993, 4190 and 443. Whether DCD lets the node send on
  port 25 is not confirmed yet; if it does not, configure a relay.

## Set up the mail domain

Creating the domain and the first mailbox is an owner-approved setup action:

```sh
stackkit setup mail-server --owner-approve --credentials-file .stackkit/setup/mail-server.json
```

The owner writes the private credentials file, readable only by the owner:

```json
{"domain":"example.com","localPart":"owner","password":"<owner-chosen, 12+ characters>"}
```

- The action creates the domain (Stalwart generates Ed25519 and RSA DKIM
  keys) and the mailbox `owner@example.com`, then signs in over IMAPS on 993
  and runs EHLO, STARTTLS and AUTH on 587 on the node. It sends no mail.
- Running it again is safe: an existing domain or mailbox is reused and only
  verified; the password of an existing mailbox is not changed.
- `"requestCertificate": false` skips the certificate request (see below).
- Run it without `--json` to see the DNS records it prints.

Never ask the owner to paste a password into chat, and never repeat one back.

## Send through a relay

Without a relay, Stalwart delivers directly to each recipient's mail server
on port 25. If the provider blocks port 25 or the owner prefers a smarthost,
add the relay to the same private file and rerun setup:

```json
{"domain":"example.com","localPart":"owner","password":"<owner-chosen>","relay":{"host":"smtp.relay.example","port":587,"security":"starttls","username":"<relay user>","password":"<relay password>"}}
```

- `security` is `"starttls"` (usually port 587) or `"ssl"` (implicit TLS,
  usually 465). TLS is always required and the relay's certificate is
  verified; there is no plaintext or invalid-certificate fallback.
- Every non-local recipient then goes through the relay; the owner's own
  domains stay local. The change applies without a restart, and rerunning
  setup updates the relay.
- The relay password goes to Stalwart once. Stalwart shows it masked, and
  StackKits never logs or stores it.
- Add the relay provider's SPF include to the domain's SPF record.

## Publish DNS records

The action prints every record. The owner publishes them at their DNS
provider:

- **Required:** `MX` for the domain pointing at `mail-server.<domain>`, SPF
  `TXT` records for the domain and the mail host, one DKIM `TXT` record per
  printed selector, and the DMARC `TXT` record.
- **Optional:** the `SRV` hints for IMAPS, submission and submissions, which
  let mail apps find the servers.
- **At the server provider:** reverse DNS (PTR) of the node's fixed IPv4,
  which setup prints, set to `mail-server.<domain>`. Without it many
  receivers reject mail.

Some VPS providers block outbound port 25 until the owner asks them to lift
it. If mail stays queued, check that first, or send through a relay.

## Certificates

IMAP and SMTP start with Stalwart's self-signed certificate. By default the
setup action asks Let's Encrypt for a certificate for `mail-server.<domain>`
through TLS-ALPN-01: the router passes only those challenges to Stalwart, so
the address must already point at the node, which it does once the route
works. The action reports whether the node already sees a trusted certificate;
a pending order completes on its own.

## Connect mail clients

- **Roundcube (`mail` workload):** run `stackkit setup mail` with
  `"imapHost":"mail-server.<domain>","imapPort":993,"imapSecurity":"ssl"`,
  `"smtpHost":"mail-server.<domain>","smtpPort":587,"smtpSecurity":"starttls"`
  and the new mailbox as `username`. Roundcube then serves the device setup
  documents for this mailbox too. The two workloads stay independent.
- **Mail apps:** IMAP `mail-server.<domain>:993` SSL/TLS, SMTP
  `mail-server.<domain>:587` STARTTLS (or 465 SSL/TLS), user name is the full
  address.

## Home nodes

The own mail server is refused on home nodes. The relay input exists, but a
home kit publishes public routes only outbound through an external access
fabric, so a home node cannot receive mail directly at its own fixed public
IP. Use a dedicated Cloud node, or use the `mail` client with an existing
provider.

## Backups

StackKits backs up Stalwart's whole data store (mailboxes, accounts, DKIM
keys and settings) after quiescing the container. Restoring it restores the
mail.
