<?php
// Governed by StackKits. Serves device setup documents generated from the
// owner's mailbox endpoints that `stackkit setup mail` stored. Responses carry
// server names, ports and the requested address only, never a password.
header('Cache-Control: no-store');
header('X-Content-Type-Options: nosniff');

function stackkit_fail(int $status, string $message): void
{
    http_response_code($status);
    header('Content-Type: text/plain; charset=utf-8');
    echo $message, "\n";
    exit;
}

function stackkit_endpoint(string $uri): array
{
    if (!preg_match('~^(ssl|tls)://([a-z0-9.-]+):([0-9]{1,5})$~', $uri, $m)) {
        stackkit_fail(503, 'The stored mailbox endpoints are invalid; run "stackkit setup mail" again.');
    }
    return ['host' => $m[2], 'port' => (int) $m[3], 'ssl' => $m[1] === 'ssl'];
}

function stackkit_email(?string $value): ?string
{
    $value = trim((string) $value);
    if ($value === '' || strlen($value) > 254 || !filter_var($value, FILTER_VALIDATE_EMAIL)) {
        return null;
    }
    return $value;
}

function stackkit_xml(string $value): string
{
    return htmlspecialchars($value, ENT_XML1 | ENT_QUOTES, 'UTF-8');
}

$stored = is_readable('/var/roundcube/mailbox/mailbox.php') ? include '/var/roundcube/mailbox/mailbox.php' : null;
if (!is_array($stored) || !isset($stored['imap'], $stored['smtp'])) {
    stackkit_fail(404, 'Mail is not set up yet; the owner runs "stackkit setup mail" first.');
}
$imap = stackkit_endpoint((string) $stored['imap']);
$smtp = stackkit_endpoint((string) $stored['smtp']);
$path = strtolower((string) parse_url((string) ($_SERVER['REQUEST_URI'] ?? '/'), PHP_URL_PATH));

if ($path === '/mail/config-v1.1.xml' || $path === '/.well-known/autoconfig/mail/config-v1.1.xml') {
    // Mozilla autoconfig (Thunderbird, FairEmail and other clients).
    $email = stackkit_email($_GET['emailaddress'] ?? null);
    $domain = $email !== null ? substr($email, strrpos($email, '@') + 1) : $imap['host'];
    $server = function (string $tag, string $type, array $endpoint): string {
        return "    <$tag type=\"$type\">\n"
            . '      <hostname>' . stackkit_xml($endpoint['host']) . "</hostname>\n"
            . '      <port>' . $endpoint['port'] . "</port>\n"
            . '      <socketType>' . ($endpoint['ssl'] ? 'SSL' : 'STARTTLS') . "</socketType>\n"
            . "      <authentication>password-cleartext</authentication>\n"
            . "      <username>%EMAILADDRESS%</username>\n"
            . "    </$tag>\n";
    };
    header('Content-Type: application/xml; charset=utf-8');
    echo "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<clientConfig version=\"1.1\">\n"
        . '  <emailProvider id="' . stackkit_xml($domain) . "\">\n"
        . '    <domain>' . stackkit_xml($domain) . "</domain>\n"
        . "    <displayName>Mail</displayName>\n"
        . $server('incomingServer', 'imap', $imap)
        . $server('outgoingServer', 'smtp', $smtp)
        . "  </emailProvider>\n</clientConfig>\n";
    exit;
}

if ($path === '/autodiscover/autodiscover.xml') {
    // Outlook autodiscover (POX). The client posts the address it sets up.
    $body = (string) file_get_contents('php://input', false, null, 0, 65536);
    $email = preg_match('~<EMailAddress>\s*([^<\s]+)\s*</EMailAddress>~i', $body, $m) ? stackkit_email(html_entity_decode($m[1], ENT_XML1)) : null;
    $email = $email ?? stackkit_email($_GET['email'] ?? null);
    if ($email === null) {
        stackkit_fail(400, 'Autodiscover needs the e-mail address being set up.');
    }
    $protocol = function (string $type, array $endpoint) use ($email): string {
        return "      <Protocol>\n"
            . "        <Type>$type</Type>\n"
            . '        <Server>' . stackkit_xml($endpoint['host']) . "</Server>\n"
            . '        <Port>' . $endpoint['port'] . "</Port>\n"
            . '        <LoginName>' . stackkit_xml($email) . "</LoginName>\n"
            . "        <DomainRequired>off</DomainRequired>\n"
            . "        <SPA>off</SPA>\n"
            . "        <SSL>on</SSL>\n"
            . '        <Encryption>' . ($endpoint['ssl'] ? 'SSL' : 'TLS') . "</Encryption>\n"
            . "        <AuthRequired>on</AuthRequired>\n"
            . "      </Protocol>\n";
    };
    header('Content-Type: application/xml; charset=utf-8');
    echo "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n"
        . "<Autodiscover xmlns=\"http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006\">\n"
        . "  <Response xmlns=\"http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a\">\n"
        . "    <Account>\n      <AccountType>email</AccountType>\n      <Action>settings</Action>\n"
        . $protocol('IMAP', $imap)
        . $protocol('SMTP', $smtp)
        . "    </Account>\n  </Response>\n</Autodiscover>\n";
    exit;
}

if ($path === '/mail.mobileconfig') {
    // Apple configuration profile (iOS, iPadOS, macOS Mail). Unsigned; the
    // device asks for the password during installation.
    $email = stackkit_email($_GET['email'] ?? null);
    if ($email === null) {
        stackkit_fail(400, 'Open this address with ?email=you@example.com to download your profile.');
    }
    $uuid = function (string $seed): string {
        $h = md5($seed);
        return strtoupper(substr($h, 0, 8) . '-' . substr($h, 8, 4) . '-' . substr($h, 12, 4) . '-' . substr($h, 16, 4) . '-' . substr($h, 20, 12));
    };
    $seed = $email . '|' . $imap['host'] . '|' . $smtp['host'];
    $bool = fn (bool $value): string => $value ? '<true/>' : '<false/>';
    $e = stackkit_xml($email);
    header('Content-Type: application/x-apple-aspen-config');
    header('Content-Disposition: attachment; filename="mail.mobileconfig"');
    echo "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"
        . "<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n"
        . "<plist version=\"1.0\">\n<dict>\n"
        . "  <key>PayloadContent</key>\n  <array>\n    <dict>\n"
        . "      <key>EmailAccountDescription</key><string>$e</string>\n"
        . "      <key>EmailAccountType</key><string>EmailTypeIMAP</string>\n"
        . "      <key>EmailAddress</key><string>$e</string>\n"
        . "      <key>IncomingMailServerAuthentication</key><string>EmailAuthPassword</string>\n"
        . '      <key>IncomingMailServerHostName</key><string>' . stackkit_xml($imap['host']) . "</string>\n"
        . '      <key>IncomingMailServerPortNumber</key><integer>' . $imap['port'] . "</integer>\n"
        . '      <key>IncomingMailServerUseSSL</key>' . $bool(true) . "\n"
        . "      <key>IncomingMailServerUsername</key><string>$e</string>\n"
        . "      <key>OutgoingMailServerAuthentication</key><string>EmailAuthPassword</string>\n"
        . '      <key>OutgoingMailServerHostName</key><string>' . stackkit_xml($smtp['host']) . "</string>\n"
        . '      <key>OutgoingMailServerPortNumber</key><integer>' . $smtp['port'] . "</integer>\n"
        . '      <key>OutgoingMailServerUseSSL</key>' . $bool(true) . "\n"
        . "      <key>OutgoingMailServerUsername</key><string>$e</string>\n"
        . '      <key>OutgoingPasswordSameAsIncomingPassword</key>' . $bool(true) . "\n"
        . "      <key>PayloadDisplayName</key><string>Mail</string>\n"
        . '      <key>PayloadIdentifier</key><string>io.kombify.stackkit.mail.account.' . $uuid('account|' . $seed) . "</string>\n"
        . "      <key>PayloadType</key><string>com.apple.mail.managed</string>\n"
        . '      <key>PayloadUUID</key><string>' . $uuid('account|' . $seed) . "</string>\n"
        . "      <key>PayloadVersion</key><integer>1</integer>\n"
        . "    </dict>\n  </array>\n"
        . "  <key>PayloadDescription</key><string>Mail account for $e. The password is requested on this device and is not part of the profile.</string>\n"
        . "  <key>PayloadDisplayName</key><string>Mail ($e)</string>\n"
        . '  <key>PayloadIdentifier</key><string>io.kombify.stackkit.mail.' . $uuid('profile|' . $seed) . "</string>\n"
        . "  <key>PayloadRemovalDisallowed</key><false/>\n"
        . "  <key>PayloadType</key><string>Configuration</string>\n"
        . '  <key>PayloadUUID</key><string>' . $uuid('profile|' . $seed) . "</string>\n"
        . "  <key>PayloadVersion</key><integer>1</integer>\n"
        . "</dict>\n</plist>\n";
    exit;
}

stackkit_fail(404, 'Not found.');
