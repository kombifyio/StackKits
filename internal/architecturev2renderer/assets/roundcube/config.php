<?php
// Governed by StackKits (mail workload bundle); edits are replaced on apply.
// No secret lives here: the key is derived from the custody-backed
// environment, and the mailbox endpoints come from the owner file that
// `stackkit setup mail` writes. Until that file exists, logins go to an
// unresolvable placeholder and fail, so no free-form server is ever used.
$config['imap_host'] = 'ssl://setup-required.invalid:993';
$config['smtp_host'] = 'ssl://setup-required.invalid:465';
$config['product_name'] = 'Mail - not set up yet: run "stackkit setup mail"';
$stackkitMailbox = '/var/roundcube/mailbox/mailbox.php';
if (is_readable($stackkitMailbox)) {
    $stackkitEndpoints = include $stackkitMailbox;
    if (is_array($stackkitEndpoints) && isset($stackkitEndpoints['imap'], $stackkitEndpoints['smtp'])) {
        $config['imap_host'] = (string) $stackkitEndpoints['imap'];
        $config['smtp_host'] = (string) $stackkitEndpoints['smtp'];
        $config['product_name'] = 'Mail';
    }
}
unset($stackkitMailbox, $stackkitEndpoints);
$config['smtp_user'] = '%u';
$config['smtp_pass'] = '%p';
$config['des_key'] = substr((string) getenv('ROUNDCUBEMAIL_DES_KEY'), 0, 24);
$config['cipher_method'] = 'DES-EDE3-CBC';
$config['enable_installer'] = false;
$config['login_rate_limit'] = 3;
$config['login_autocomplete'] = 0;
$config['ip_check'] = true;
$config['session_lifetime'] = 30;
$config['session_samesite'] = 'Lax';
$config['display_product_info'] = 1;
$config['use_https'] = strtolower((string) ($_SERVER['HTTP_X_FORWARDED_PROTO'] ?? '')) === 'https';
