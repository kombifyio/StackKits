package appsetup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/smtp"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	stalwartSetupTimeout      = 2 * time.Minute
	stalwartResponseBodyLimit = 4 << 20
	stalwartJMAPCapability    = "urn:stalwart:jmap"
	stalwartLetsEncrypt       = "https://acme-v02.api.letsencrypt.org/directory"
	stalwartIMAPSPort         = 993
	stalwartSubmissionPort    = 587
)

var (
	stalwartDomainPattern    = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	stalwartLocalPartPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._+-]{0,62}[a-z0-9])?$`)
)

// StalwartMailDomainRequest is the private owner input for the own mail
// server setup (ADR-0046). Password is sent to Stalwart once to create the
// mailbox and used for the IMAP and submission checks; it is never logged,
// returned or persisted. AdminPassword is the custody-held fallback
// administrator secret.
type StalwartMailDomainRequest struct {
	Domain             string
	LocalPart          string
	Password           string
	MailHost           string
	AdminPassword      []byte
	ACMEContact        string
	RequestCertificate bool
	// Relay optionally routes all outbound mail through the owner's
	// smarthost. Its password is sent to Stalwart once and never logged,
	// returned or persisted by StackKits; Stalwart masks it on read.
	Relay *StalwartRelay
	// Dial reaches the node's published mail ports; nil dials the loopback
	// address of the node the setup runs on.
	Dial func(ctx context.Context, port int) (net.Conn, error)
	// LookupIPv4 resolves the mail host's IPv4 addresses; nil uses the
	// system resolver.
	LookupIPv4 func(ctx context.Context, host string) ([]netip.Addr, error)
}

// StalwartRelay is the owner's outbound relay (smarthost). Security is "ssl"
// (implicit TLS) or "starttls"; TLS is always required and the relay
// certificate is always verified.
type StalwartRelay struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string
}

const (
	stalwartRelayRouteName = "stackkit-relay"
	stalwartRelayTLSName   = "stackkit-relay-tls"
)

// StalwartDNSRecord is one record the owner publishes. StackKits never
// creates DNS records.
type StalwartDNSRecord struct {
	Name     string
	Type     string
	Value    string
	Required bool
	Purpose  string
}

// StalwartMailDomainResult is secret-free. MailboxRef is a digest of the
// mailbox address so lifecycle evidence never records the address itself.
type StalwartMailDomainResult struct {
	MailboxRef string
	Address    string
	// PublicIPv4 are the mail host's public IPv4 addresses; the owner sets
	// reverse DNS for them.
	PublicIPv4 []string
	// Outbound says how the server sends mail: directly or through the relay.
	Outbound           string
	DomainCreated      bool
	MailboxCreated     bool
	IMAPLoginVerified  bool
	SubmissionVerified bool
	CertificateTrusted bool
	Certificate        string
	Records            []StalwartDNSRecord
}

// SetupStalwartMailDomain creates the owner's mail domain and first mailbox
// through Stalwart's JMAP management API with the custody administrator,
// optionally requests a certificate for the mail host through ACME
// TLS-ALPN-01, then verifies an IMAPS login and a submission handshake
// (EHLO, STARTTLS, AUTH) on the node. It sends no mail.
//
//nolint:gocyclo // One bounded owner action keeps its ordered checks together.
func SetupStalwartMailDomain(ctx context.Context, client *http.Client, baseURL string, request StalwartMailDomainRequest) (StalwartMailDomainResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, stalwartSetupTimeout)
	defer cancel()
	domain := strings.ToLower(strings.TrimSpace(request.Domain))
	if len(domain) > 253 || !stalwartDomainPattern.MatchString(domain) {
		return StalwartMailDomainResult{}, errors.New("domain must be the mail domain you own, for example example.com")
	}
	localPart := strings.ToLower(strings.TrimSpace(request.LocalPart))
	if !stalwartLocalPartPattern.MatchString(localPart) {
		return StalwartMailDomainResult{}, errors.New("localPart must be the mailbox name before the @, for example owner")
	}
	if len(request.Password) < 12 || len(request.Password) > 1024 || strings.IndexFunc(request.Password, unicode.IsControl) >= 0 {
		return StalwartMailDomainResult{}, errors.New("password must have 12 to 1024 printable characters")
	}
	mailHost := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(request.MailHost), "."))
	if !stalwartDomainPattern.MatchString(mailHost) {
		return StalwartMailDomainResult{}, errors.New("the mail server has no public host name; apply the mail-server workload with its public route first")
	}
	if len(request.AdminPassword) == 0 {
		return StalwartMailDomainResult{}, errors.New("the custody-held Stalwart administrator password is unavailable")
	}
	relay, err := normalizeStalwartRelay(request.Relay)
	if err != nil {
		return StalwartMailDomainResult{}, err
	}
	publicIPv4, err := stalwartPublicIPv4(ctx, request.LookupIPv4, mailHost)
	if err != nil {
		return StalwartMailDomainResult{}, err
	}
	api := stalwartAPI{client: client, endpoint: strings.TrimRight(baseURL, "/") + "/jmap/", auth: "admin:" + string(request.AdminPassword)}
	defer func() { api.auth = "" }()
	address := localPart + "@" + domain
	result := StalwartMailDomainResult{Address: address, MailboxRef: stalwartDigest(mailHost + "\x00" + address), PublicIPv4: publicIPv4}

	state, err := api.call(ctx,
		stalwartCall("x:Domain/get", map[string]any{"properties": []string{"name"}}, "domains"),
		stalwartCall("x:Account/get", map[string]any{"properties": []string{"name", "domainId"}}, "accounts"),
		stalwartCall("x:SystemSettings/get", map[string]any{"ids": []string{"singleton"}, "properties": []string{"defaultHostname", "defaultDomainId"}}, "settings"),
		stalwartCall("x:AcmeProvider/get", map[string]any{"properties": []string{"directory", "challengeType"}}, "acme"),
	)
	if err != nil {
		return StalwartMailDomainResult{}, err
	}
	domains, err := state.list("domains")
	if err != nil {
		return StalwartMailDomainResult{}, err
	}
	domainID := ""
	for _, item := range domains {
		if stringValue(item["name"]) == domain {
			domainID = stringValue(item["id"])
		}
	}
	if domainID == "" {
		created, err := api.create(ctx, "x:Domain/set", map[string]any{"name": domain})
		if err != nil {
			return StalwartMailDomainResult{}, fmt.Errorf("create the mail domain: %w", err)
		}
		domainID, result.DomainCreated = created, true
	}
	settings, err := state.list("settings")
	if err != nil || len(settings) != 1 {
		return StalwartMailDomainResult{}, errors.New("Stalwart did not return its system settings")
	}
	defaultDomainValid := false
	for _, item := range domains {
		if stringValue(item["id"]) == stringValue(settings[0]["defaultDomainId"]) {
			defaultDomainValid = true
		}
	}
	if !defaultDomainValid || stringValue(settings[0]["defaultHostname"]) != mailHost {
		update := map[string]any{"defaultHostname": mailHost, "defaultDomainId": stringValue(settings[0]["defaultDomainId"])}
		if !defaultDomainValid {
			update["defaultDomainId"] = domainID
		}
		if err := api.update(ctx, "x:SystemSettings/set", "singleton", update); err != nil {
			return StalwartMailDomainResult{}, fmt.Errorf("set the mail host name: %w", err)
		}
	}
	accounts, err := state.list("accounts")
	if err != nil {
		return StalwartMailDomainResult{}, err
	}
	accountExists := false
	for _, item := range accounts {
		if stringValue(item["name"]) == localPart && stringValue(item["domainId"]) == domainID {
			accountExists = true
		}
	}
	if !accountExists {
		if _, err := api.create(ctx, "x:Account/set", map[string]any{
			"@type": "User", "name": localPart, "domainId": domainID,
			"credentials": map[string]any{"0": map[string]any{"@type": "Password", "secret": request.Password}},
		}); err != nil {
			return StalwartMailDomainResult{}, fmt.Errorf("create the mailbox: %w", err)
		}
		result.MailboxCreated = true
	}

	result.Outbound = "direct delivery to each recipient's MX host on port 25"
	if relay != nil {
		if err := stalwartConfigureRelay(ctx, api, *relay); err != nil {
			return StalwartMailDomainResult{}, fmt.Errorf("configure the outbound relay: %w", err)
		}
		result.Outbound = fmt.Sprintf("through the relay %s:%d (%s, TLS required, certificate verified)", relay.Host, relay.Port, relay.Security)
	}

	result.Certificate = "not requested; IMAP and SMTP present Stalwart's self-signed certificate"
	if request.RequestCertificate {
		result.Certificate = stalwartRequestCertificate(ctx, api, state, domainID, mailHost, request.ACMEContact, domain)
	}

	// Stalwart generates the Ed25519 and RSA DKIM keys asynchronously after
	// the domain exists; wait briefly for both before printing the records.
	for attempt := 0; ; attempt++ {
		signatures, err := api.call(ctx, stalwartCall("x:DkimSignature/get", map[string]any{"properties": []string{"selector", "publicKey", "domainId", "stage", "@type"}}, "dkim"))
		if err != nil {
			return StalwartMailDomainResult{}, err
		}
		dkim, err := signatures.list("dkim")
		if err != nil {
			return StalwartMailDomainResult{}, err
		}
		result.Records = stalwartDNSRecords(domain, mailHost, domainID, dkim)
		keys := 0
		for _, record := range result.Records {
			if strings.Contains(record.Name, "._domainkey.") {
				keys++
			}
		}
		if keys >= 2 || attempt >= 15 {
			break
		}
		select {
		case <-ctx.Done():
			return StalwartMailDomainResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}

	dial := request.Dial
	if dial == nil {
		dial = func(ctx context.Context, port int) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		}
	}
	trusted, err := verifyStalwartIMAPLogin(ctx, dial, mailHost, address, request.Password)
	if err != nil {
		return StalwartMailDomainResult{}, err
	}
	result.IMAPLoginVerified, result.CertificateTrusted = true, trusted
	if err := verifyStalwartSubmission(ctx, dial, mailHost, address, request.Password); err != nil {
		return StalwartMailDomainResult{}, err
	}
	result.SubmissionVerified = true
	return result, nil
}

// stalwartPublicIPv4 requires a public IPv4 address record for the mail
// host. An IPv6-only mail server loses mail from IPv4-only senders, so the
// setup refuses it (ADR-0046 amendment 2026-09-25).
func stalwartPublicIPv4(ctx context.Context, lookup func(context.Context, string) ([]netip.Addr, error), mailHost string) ([]string, error) {
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		}
	}
	addresses, _ := lookup(ctx, mailHost)
	var public []string
	for _, address := range addresses {
		address = address.Unmap()
		if address.Is4() && address.IsGlobalUnicast() && !address.IsPrivate() {
			public = append(public, address.String())
		}
	}
	if len(public) == 0 {
		return nil, fmt.Errorf("%s has no public IPv4 address record; publish an A record pointing at this node's fixed public IPv4 (an IPv6-only mail server loses mail from IPv4-only senders) and rerun setup", mailHost)
	}
	sort.Strings(public)
	return public, nil
}

func normalizeStalwartRelay(relay *StalwartRelay) (*StalwartRelay, error) {
	if relay == nil {
		return nil, nil
	}
	normalized := *relay
	normalized.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(relay.Host), "."))
	if _, err := netip.ParseAddr(normalized.Host); err != nil && (len(normalized.Host) > 253 || !stalwartDomainPattern.MatchString(normalized.Host)) {
		return nil, errors.New("relay.host must be your relay's host name, for example smtp.example.net")
	}
	if normalized.Port < 1 || normalized.Port > 65535 {
		return nil, errors.New("relay.port must be the relay's submission port, usually 465 or 587")
	}
	switch normalized.Security {
	case "ssl", "starttls":
	default:
		return nil, errors.New(`relay.security must be "ssl" (implicit TLS, usually port 465) or "starttls" (usually port 587)`)
	}
	normalized.Username = strings.TrimSpace(relay.Username)
	if normalized.Username == "" || len(normalized.Username) > 512 || strings.IndexFunc(normalized.Username, unicode.IsControl) >= 0 {
		return nil, errors.New("relay.username must be the relay account's user name")
	}
	if relay.Password == "" || len(relay.Password) > 1024 || strings.IndexFunc(relay.Password, unicode.IsControl) >= 0 {
		return nil, errors.New("relay.password must be the relay account's password (printable characters)")
	}
	return &normalized, nil
}

// stalwartConfigureRelay creates or updates the StackKits relay route and a
// TLS strategy that requires TLS with a verified certificate, points every
// non-local delivery at that route and reloads Stalwart's settings. Local
// domains keep local delivery. The password is sent once; Stalwart masks it
// on read.
func stalwartConfigureRelay(ctx context.Context, api stalwartAPI, relay StalwartRelay) error {
	state, err := api.call(ctx,
		stalwartCall("x:MtaRoute/get", map[string]any{"properties": []string{"name"}}, "routes"),
		stalwartCall("x:MtaTlsStrategy/get", map[string]any{"properties": []string{"name"}}, "tls"),
	)
	if err != nil {
		return err
	}
	route := map[string]any{
		"@type": "Relay", "name": stalwartRelayRouteName, "description": "Owner relay configured by stackkit setup mail-server",
		"address": relay.Host, "port": relay.Port, "protocol": "smtp", "implicitTls": relay.Security == "ssl", "allowInvalidCerts": false,
		"authUsername": relay.Username, "authSecret": map[string]any{"@type": "Value", "secret": relay.Password},
	}
	tlsStrategy := map[string]any{
		"name": stalwartRelayTLSName, "description": "Owner relay: TLS required, certificate verified",
		"startTls": "require", "allowInvalidCerts": false, "dane": "disable", "mtaSts": "disable",
	}
	for _, item := range []struct {
		response, method string
		object           map[string]any
	}{
		{"routes", "x:MtaRoute/set", route},
		{"tls", "x:MtaTlsStrategy/set", tlsStrategy},
	} {
		existing, err := state.list(item.response)
		if err != nil {
			return err
		}
		id := ""
		for _, candidate := range existing {
			if stringValue(candidate["name"]) == stringValue(item.object["name"]) {
				id = stringValue(candidate["id"])
			}
		}
		if id == "" {
			if _, err := api.create(ctx, item.method, item.object); err != nil {
				return err
			}
			continue
		}
		patch := make(map[string]any, len(item.object))
		for key, value := range item.object {
			// The variant and the referenced name are read-only once created.
			if key != "@type" && key != "name" {
				patch[key] = value
			}
		}
		if err := api.update(ctx, item.method, id, patch); err != nil {
			return err
		}
	}
	if err := api.update(ctx, "x:MtaOutboundStrategy/set", "singleton", map[string]any{
		"route": map[string]any{"match": map[string]any{"0": map[string]any{"if": "is_local_domain(rcpt_domain)", "then": "'local'"}}, "else": "'" + stalwartRelayRouteName + "'"},
		"tls":   map[string]any{"match": map[string]any{}, "else": "'" + stalwartRelayTLSName + "'"},
	}); err != nil {
		return err
	}
	if _, err := api.create(ctx, "x:Action/set", map[string]any{"@type": "ReloadSettings"}); err != nil {
		return fmt.Errorf("reload settings: %w", err)
	}
	return nil
}

// stalwartRequestCertificate asks Stalwart for a publicly trusted certificate
// for the mail host only. The router passes TLS-ALPN-01 challenges for that
// host to Stalwart. A failure is reported, not fatal: mail keeps working with
// the self-signed certificate until the owner retries.
func stalwartRequestCertificate(ctx context.Context, api stalwartAPI, state stalwartResponses, domainID, mailHost, contact, domain string) string {
	providers, err := state.list("acme")
	if err != nil {
		return "pending: " + err.Error()
	}
	providerID := ""
	for _, item := range providers {
		if stringValue(item["directory"]) == stalwartLetsEncrypt && stringValue(item["challengeType"]) == "TlsAlpn01" {
			providerID = stringValue(item["id"])
		}
	}
	if providerID == "" {
		contact = strings.TrimSpace(contact)
		if contact == "" || strings.ContainsAny(contact, " \t\r\n") || !strings.Contains(contact, "@") {
			contact = "postmaster@" + domain
		}
		providerID, err = api.create(ctx, "x:AcmeProvider/set", map[string]any{
			"directory": stalwartLetsEncrypt, "challengeType": "TlsAlpn01", "contact": map[string]bool{contact: true},
		})
		if err != nil {
			return "pending: Let's Encrypt account registration failed (" + err.Error() + "); rerun setup to retry"
		}
	}
	if err := api.update(ctx, "x:Domain/set", domainID, map[string]any{"certificateManagement": map[string]any{
		"@type": "Automatic", "acmeProviderId": providerID, "subjectAlternativeNames": map[string]bool{mailHost: true},
	}}); err != nil {
		return "pending: certificate management was not accepted (" + err.Error() + "); rerun setup to retry"
	}
	return "requested from Let's Encrypt for " + mailHost + " through TLS-ALPN-01; Stalwart installs it when the order completes"
}

func stalwartDNSRecords(domain, mailHost, domainID string, dkim []map[string]any) []StalwartDNSRecord {
	records := []StalwartDNSRecord{
		{Name: domain + ".", Type: "MX", Value: "10 " + mailHost + ".", Required: true, Purpose: "deliver mail for the domain to this server"},
		{Name: domain + ".", Type: "TXT", Value: `"v=spf1 mx -all"`, Required: true, Purpose: "SPF: only the MX host sends for the domain"},
		{Name: mailHost + ".", Type: "TXT", Value: `"v=spf1 a -all"`, Required: true, Purpose: "SPF for the server's own HELO name"},
		{Name: "_dmarc." + domain + ".", Type: "TXT", Value: `"v=DMARC1; p=reject; rua=mailto:postmaster@` + domain + `"`, Required: true, Purpose: "DMARC policy and aggregate reports"},
	}
	sort.SliceStable(dkim, func(i, j int) bool { return stringValue(dkim[i]["selector"]) < stringValue(dkim[j]["selector"]) })
	for _, signature := range dkim {
		if stringValue(signature["domainId"]) != domainID || stringValue(signature["stage"]) != "active" {
			continue
		}
		selector, key := stringValue(signature["selector"]), stringValue(signature["publicKey"])
		algorithm := ""
		switch stringValue(signature["@type"]) {
		case "Dkim1Ed25519Sha256":
			algorithm = "ed25519"
		case "Dkim1RsaSha256":
			algorithm = "rsa"
		}
		if selector == "" || key == "" || algorithm == "" {
			continue
		}
		records = append(records, StalwartDNSRecord{
			Name: selector + "._domainkey." + domain + ".", Type: "TXT", Required: true, Purpose: "DKIM public key",
			Value: stalwartTXTValue("v=DKIM1; k=" + algorithm + "; h=sha256; p=" + key),
		})
	}
	records = append(records,
		StalwartDNSRecord{Name: "_imaps._tcp." + domain + ".", Type: "SRV", Value: "0 1 993 " + mailHost + ".", Purpose: "client autoconfiguration hint (RFC 6186): IMAPS"},
		StalwartDNSRecord{Name: "_submission._tcp." + domain + ".", Type: "SRV", Value: "0 1 587 " + mailHost + ".", Purpose: "client autoconfiguration hint (RFC 6186): submission with STARTTLS"},
		StalwartDNSRecord{Name: "_submissions._tcp." + domain + ".", Type: "SRV", Value: "0 1 465 " + mailHost + ".", Purpose: "client autoconfiguration hint (RFC 8314): submission with implicit TLS"},
	)
	return records
}

// stalwartTXTValue splits long TXT data into quoted 255-byte strings.
func stalwartTXTValue(value string) string {
	var parts []string
	for len(value) > 255 {
		parts = append(parts, `"`+value[:255]+`"`)
		value = value[255:]
	}
	return strings.Join(append(parts, `"`+value+`"`), " ")
}

func stalwartDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// stalwartTLSConfig accepts Stalwart's self-signed certificate on the node's
// own loopback address; trust is reported separately and never assumed.
func stalwartTLSConfig(mailHost string) *tls.Config {
	return &tls.Config{ServerName: mailHost, MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} //nolint:gosec // Loopback to the node itself; trust is verified and reported below.
}

func stalwartCertificateTrusted(state tls.ConnectionState, mailHost string) bool {
	if len(state.PeerCertificates) == 0 {
		return false
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range state.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	_, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: mailHost, Intermediates: intermediates})
	return err == nil
}

func verifyStalwartIMAPLogin(ctx context.Context, dial func(context.Context, int) (net.Conn, error), mailHost, address, password string) (bool, error) {
	raw, err := dial(ctx, stalwartIMAPSPort)
	if err != nil {
		return false, errors.New("IMAPS on port 993 is not reachable on the node")
	}
	defer raw.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	conn := tls.Client(raw, stalwartTLSConfig(mailHost))
	if err := conn.HandshakeContext(ctx); err != nil {
		return false, errors.New("IMAPS on port 993 did not complete a TLS handshake")
	}
	trusted := stalwartCertificateTrusted(conn.ConnectionState(), mailHost)
	reader := bufio.NewReader(conn)
	greeting, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(greeting, "* OK") {
		return false, errors.New("IMAPS on port 993 did not greet as an IMAP server")
	}
	command := func(tag, line string) error {
		if _, err := io.WriteString(conn, tag+" "+line+"\r\n"); err != nil {
			return err
		}
		for {
			response, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			if strings.HasPrefix(response, tag+" ") {
				if strings.HasPrefix(response, tag+" OK") {
					return nil
				}
				return errors.New("rejected")
			}
		}
	}
	if err := command("a1", "LOGIN "+imapQuoted(address)+" "+imapQuoted(password)); err != nil {
		return false, errors.New("the new mailbox did not sign in over IMAPS; check the password and rerun setup")
	}
	if err := command("a2", "SELECT INBOX"); err != nil {
		return false, errors.New("the new mailbox signed in but its inbox did not open")
	}
	_ = command("a3", "LOGOUT")
	return trusted, nil
}

func imapQuoted(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func verifyStalwartSubmission(ctx context.Context, dial func(context.Context, int) (net.Conn, error), mailHost, address, password string) error {
	raw, err := dial(ctx, stalwartSubmissionPort)
	if err != nil {
		return errors.New("submission on port 587 is not reachable on the node")
	}
	defer raw.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(raw, mailHost)
	if err != nil {
		return errors.New("submission on port 587 did not greet as an SMTP server")
	}
	defer client.Close()
	if err := client.Hello("stackkit-setup.invalid"); err != nil {
		return errors.New("submission on port 587 refused EHLO")
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return errors.New("submission on port 587 does not offer STARTTLS")
	}
	if err := client.StartTLS(stalwartTLSConfig(mailHost)); err != nil {
		return errors.New("submission on port 587 did not complete STARTTLS")
	}
	if err := client.Auth(smtp.PlainAuth("", address, password, mailHost)); err != nil {
		return errors.New("the new mailbox did not authenticate for submission on port 587")
	}
	_ = client.Quit()
	return nil
}

// stalwartAPI is a bounded JMAP management client for the custody
// administrator. Management objects use Stalwart's x: method namespace.
type stalwartAPI struct {
	client   *http.Client
	endpoint string
	auth     string
}

type stalwartResponses map[string]stalwartMethodResponse

type stalwartMethodResponse struct {
	name string
	args map[string]any
}

func stalwartCall(method string, args map[string]any, id string) []any {
	return []any{method, args, id}
}

func (api stalwartAPI) call(ctx context.Context, calls ...[]any) (stalwartResponses, error) {
	body, err := json.Marshal(map[string]any{"using": []string{"urn:ietf:params:jmap:core", stalwartJMAPCapability}, "methodCalls": calls})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, api.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	user, password, _ := strings.Cut(api.auth, ":")
	request.SetBasicAuth(user, password)
	request.Header.Set("Content-Type", "application/json")
	response, err := api.client.Do(request)
	if err != nil {
		return nil, errors.New("Stalwart's management API is not reachable on the node")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, stalwartResponseBodyLimit))
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, errors.New("Stalwart refused the custody administrator; the running server does not match its applied secret")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Stalwart's management API answered HTTP %d", response.StatusCode)
	}
	var decoded struct {
		MethodResponses [][]json.RawMessage `json:"methodResponses"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, errors.New("Stalwart's management API returned an unreadable response")
	}
	result := stalwartResponses{}
	for _, entry := range decoded.MethodResponses {
		if len(entry) != 3 {
			return nil, errors.New("Stalwart's management API returned a malformed method response")
		}
		var name, id string
		args := map[string]any{}
		if json.Unmarshal(entry[0], &name) != nil || json.Unmarshal(entry[1], &args) != nil || json.Unmarshal(entry[2], &id) != nil {
			return nil, errors.New("Stalwart's management API returned a malformed method response")
		}
		if name == "error" {
			return nil, fmt.Errorf("Stalwart rejected %s: %s", id, stringValue(args["type"]))
		}
		result[id] = stalwartMethodResponse{name: name, args: args}
	}
	return result, nil
}

func (responses stalwartResponses) list(id string) ([]map[string]any, error) {
	response, ok := responses[id]
	if !ok {
		return nil, fmt.Errorf("Stalwart omitted the %s response", id)
	}
	raw, _ := response.args["list"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		if item, ok := value.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items, nil
}

// create returns the server id of one created object or the set error.
func (api stalwartAPI) create(ctx context.Context, method string, object map[string]any) (string, error) {
	responses, err := api.call(ctx, stalwartCall(method, map[string]any{"create": map[string]any{"new": object}}, "set"))
	if err != nil {
		return "", err
	}
	args := responses["set"].args
	if created, ok := args["created"].(map[string]any); ok {
		if item, ok := created["new"].(map[string]any); ok && stringValue(item["id"]) != "" {
			return stringValue(item["id"]), nil
		}
	}
	return "", stalwartSetError(args, "notCreated", "new")
}

func (api stalwartAPI) update(ctx context.Context, method, id string, patch map[string]any) error {
	responses, err := api.call(ctx, stalwartCall(method, map[string]any{"update": map[string]any{id: patch}}, "set"))
	if err != nil {
		return err
	}
	args := responses["set"].args
	if updated, ok := args["updated"].(map[string]any); ok {
		if _, ok := updated[id]; ok {
			return nil
		}
	}
	return stalwartSetError(args, "notUpdated", id)
}

func stalwartSetError(args map[string]any, field, id string) error {
	failures, _ := args[field].(map[string]any)
	failure, _ := failures[id].(map[string]any)
	if failure == nil {
		return errors.New("Stalwart did not confirm the change")
	}
	description := stringValue(failure["description"])
	if description == "" {
		description = stringValue(failure["type"])
	}
	return errors.New(description)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
