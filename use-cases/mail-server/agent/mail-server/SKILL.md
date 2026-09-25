---
name: mail-server
description: Operate the owner's own Stalwart mail server installed by StackKits on a Cloud node - create the mail domain and first mailbox, publish the printed DNS records, get a trusted certificate, connect Roundcube or mail apps - without handling mailbox or administrator passwords in chat.
---

# Own mail server

StackKits installs Stalwart Mail Server on one public Cloud node (ADR-0046).
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

## Publish DNS records

The action prints every record. The owner publishes them at their DNS
provider:

- **Required:** `MX` for the domain pointing at `mail-server.<domain>`, SPF
  `TXT` records for the domain and the mail host, one DKIM `TXT` record per
  printed selector, and the DMARC `TXT` record.
- **Optional:** the `SRV` hints for IMAPS, submission and submissions, which
  let mail apps find the servers.
- **At the server provider:** reverse DNS (PTR) of the node's address set to
  `mail-server.<domain>`. Without it many receivers reject mail.

Some VPS providers block outbound port 25 until the owner asks them to lift
it. If mail stays queued, check that first.

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

The own mail server is refused on home nodes for now. Sending mail directly
from a home connection is widely blocked, and StackKits has no owner input for
an outbound relay (smarthost) yet. Use the Cloud Kit, or use the `mail`
client with an existing provider.

## Backups

StackKits backs up Stalwart's whole data store (mailboxes, accounts, DKIM
keys and settings) after quiescing the container. Restoring it restores the
mail.
