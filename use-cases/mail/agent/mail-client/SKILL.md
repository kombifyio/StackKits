---
name: mail-client
description: Help the owner use Roundcube webmail installed by StackKits for a mailbox they already have at an external provider - store the mail servers, verify the login, set up Thunderbird, Apple Mail, Outlook or FairEmail, understand what is backed up - without handling their mail password.
---

# Mail client

StackKits installs Roundcube Webmail at the workload's route
(`https://mail.<domain>`) as a client for a mailbox the owner already has at an
external provider. StackKits runs no mail server: it creates no SMTP listener,
no DNS or MX records and stores no mail. Mail stays with the provider.

## Verify the mailbox

Until the owner runs this owner-approved setup action, Roundcube refuses every
login and its login page says so. The login form never asks for a server.

```sh
stackkit setup mail --owner-approve --credentials-file .stackkit/setup/mail.json --json
```

The owner writes the private credentials file, readable only by the owner:

```json
{"imapHost":"imap.example.com","imapPort":993,"imapSecurity":"ssl","smtpHost":"smtp.example.com","smtpPort":587,"smtpSecurity":"starttls","username":"me@example.com","password":"<owner-provided>"}
```

- `imapSecurity` / `smtpSecurity` are `ssl` (implicit TLS) or `starttls`.
  They default only on the registered ports (IMAP 993 `ssl`, 143 `starttls`;
  SMTP 465 `ssl`, 587 `starttls`) and are required on any other port.
- The action stores the servers (never the password) for Roundcube and the
  device setup documents, signs in through Roundcube's own login form, and
  signs out. If the login fails, the previously stored servers are restored.
- Use the provider's app password when the account has two-factor sign-in.
- Running it again with other servers replaces them the same way.

Never ask the owner to paste a mailbox password into chat, and never repeat
one back. After three failed logins for a known account Roundcube pauses
logins for that account for a minute. Sessions end after 30 idle minutes.

## Set up devices

After setup, the mail route serves setup documents built from the stored
servers. They contain server names, ports and the address being set up, never
a password; the device asks for the password itself.

Mail apps look for settings under the domain of the mail address, which
belongs to the provider, so they do not find these documents on their own.
DNS-based discovery (`autoconfig.<domain>`, `autodiscover.<domain>`) is out of
scope. The owner opens the profile URL or enters the servers shown by the
autoconfig URL:

```text
https://mail.<domain>/mail/config-v1.1.xml?emailaddress=<address>
https://mail.<domain>/mail.mobileconfig?email=<address>
```

- **Apple Mail (iPhone, iPad, Mac):** open the `mail.mobileconfig` URL on the
  device, install the downloaded profile in Settings and enter the password.
  The profile is unsigned, so the device labels it "Unverified".
- **Thunderbird (desktop, Android):** add the account with name, address and
  password, choose "Configure manually" and enter the IMAP and SMTP servers,
  ports and security from the autoconfig URL. Thunderbird for iOS is planned
  for the end of 2026.
- **Outlook:** add the account, choose IMAP and enter the same servers. The
  route also answers Outlook autodiscover at `/autodiscover/autodiscover.xml`
  for owners who point their own domain's autodiscover at it.
- **FairEmail (Android):** use the manual setup with the same servers.

## Backups

StackKits backs up Roundcube's own database (settings, identities, address
book) and the stored mail servers. It does not back up mail; that is the
provider's responsibility.
