package hostsecurity

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// benignListeners are sockets every managed host has and that are not a
// service exposure: the DHCP client ports.
var benignListeners = map[Listener]bool{
	{Transport: "udp", Port: 68}:  true,
	{Transport: "udp", Port: 546}: true,
}

// observedListener is one socket listening on a non-loopback address.
type observedListener struct {
	Listener
	Address string
	Source  string // "socket" or "container"
}

func (l observedListener) String() string {
	return fmt.Sprintf("%s/%d on %s (%s)", l.Transport, l.Port, l.Address, l.Source)
}

// parseSocketListeners reads `ss -H -l -n -t -u` and keeps non-loopback
// listeners.
func parseSocketListeners(stdout string) []observedListener {
	var listeners []observedListener
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		transport := fields[0]
		if transport != "tcp" && transport != "udp" {
			continue
		}
		address, port, ok := splitHostPort(fields[4])
		if !ok || loopbackAddress(address) {
			continue
		}
		listeners = append(listeners, observedListener{Listener: Listener{Transport: transport, Port: port}, Address: address, Source: "socket"})
	}
	return listeners
}

// splitHostPort splits the local address column of ss: 0.0.0.0:22, [::]:22,
// *:22, 127.0.0.53%lo:53.
func splitHostPort(value string) (string, int, bool) {
	index := strings.LastIndex(value, ":")
	if index < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(value[index+1:])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	address := strings.Trim(value[:index], "[]")
	if zone := strings.Index(address, "%"); zone >= 0 {
		address = address[:zone]
	}
	if address == "*" {
		address = "0.0.0.0"
	}
	return address, port, true
}

func loopbackAddress(address string) bool {
	parsed, err := netip.ParseAddr(address)
	return err == nil && parsed.IsLoopback()
}

// parseContainerPorts reads the Ports column of `docker ps`, for example
// "0.0.0.0:8080->80/tcp, :::8080->80/tcp, 127.0.0.1:9000->9000/tcp". It keeps
// published ports bound beyond loopback. With the userland proxy disabled such
// a port has no socket, so a socket listing alone would miss it.
func parseContainerPorts(stdout string) []observedListener {
	var listeners []observedListener
	seen := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		for _, entry := range strings.Split(line, ",") {
			entry = strings.TrimSpace(entry)
			arrow := strings.Index(entry, "->")
			if arrow < 0 {
				continue
			}
			hostPart := entry[:arrow]
			transport := "tcp"
			if slash := strings.LastIndex(entry, "/"); slash > arrow {
				transport = entry[slash+1:]
			}
			if transport != "tcp" && transport != "udp" {
				continue
			}
			colon := strings.LastIndex(hostPart, ":")
			if colon < 0 {
				continue
			}
			address := strings.Trim(hostPart[:colon], "[]")
			if address == "" {
				address = "0.0.0.0"
			}
			if loopbackAddress(address) {
				continue
			}
			first, last := hostPart[colon+1:], hostPart[colon+1:]
			if dash := strings.Index(first, "-"); dash >= 0 {
				first, last = first[:dash], first[dash+1:]
			}
			low, lowErr := strconv.Atoi(first)
			high, highErr := strconv.Atoi(last)
			if lowErr != nil || highErr != nil || low < 1 || high > 65535 || high < low || high-low > 1024 {
				continue
			}
			for port := low; port <= high; port++ {
				key := fmt.Sprintf("%s/%d", transport, port)
				if seen[key] {
					continue
				}
				seen[key] = true
				listeners = append(listeners, observedListener{Listener: Listener{Transport: transport, Port: port}, Address: address, Source: "container"})
			}
		}
	}
	return listeners
}

// exposure observes the two ways a service is reachable beyond loopback: a
// socket listening on this host, and a container-published port. They are
// separate controls because the input firewall filters the first and cannot
// filter the second (Docker forwards published ports after DNAT, so they never
// reach the input hook), so a published port is its own finding.
func (o *observation) exposure(ctx context.Context) (listeners, published Control) {
	listeners = o.control(ControlListeners, "every socket listener beyond loopback is a declared service or the ssh port",
		Remediation{Capability: RemediationManual, Action: "stop the undeclared service, declare it in the StackSpec, or record an owner exception"})
	published = o.control(ControlPublishedPorts, "every container-published port beyond loopback is a declared service",
		Remediation{Capability: RemediationManual, Action: "bind the published port to 127.0.0.1 or a private address in the workload, declare it in the StackSpec, or record an owner exception; the host input firewall does not filter Docker-published ports"})

	publishedPorts, publishedNote, publishedOK := o.publishedListeners(ctx)
	published = o.judgeExposure(ctx, published, publishedPorts, publishedNote, publishedOK, ReasonExposedPublishedPort, "container-published")

	output, err := o.engine.run(ctx, "ss", "-H", "-l", "-n", "-t", "-u")
	switch {
	case err != nil:
		return o.unknown(listeners, "ss could not list sockets: "+err.Error()), published
	case output.ExitCode != 0:
		return o.unknown(listeners, "ss failed: "+boundedText(output.Stderr)), published
	}
	// A socket that is a container's published port (docker-proxy) belongs to
	// the published-ports control, not here.
	inPublished := map[Listener]bool{}
	for _, listener := range publishedPorts {
		inPublished[listener.Listener] = true
	}
	var sockets []observedListener
	for _, listener := range parseSocketListeners(output.Stdout) {
		if !inPublished[listener.Listener] {
			sockets = append(sockets, listener)
		}
	}
	return o.judgeExposure(ctx, listeners, sockets, "", true, "", "socket"), published
}

// allowedListeners are the ports that are not an exposure: the DHCP client, the
// ssh ports, and the declared services.
func (o *observation) allowedListeners(ctx context.Context) map[Listener]bool {
	allowed := map[Listener]bool{}
	for listener := range benignListeners {
		allowed[listener] = true
	}
	if settings, _ := o.sshdEffective(ctx); settings != nil {
		for _, port := range settings.ports() {
			allowed[Listener{Transport: "tcp", Port: port}] = true
		}
	} else {
		allowed[Listener{Transport: "tcp", Port: 22}] = true
	}
	for _, listener := range o.options.DeclaredListeners {
		allowed[listener] = true
	}
	return allowed
}

// judgeExposure turns a set of observed non-loopback listeners into the control
// state. Anything not declared is drifted (or unknown when there is no
// declared-service authority); reasonCode marks the distinct finding.
func (o *observation) judgeExposure(ctx context.Context, c Control, found []observedListener, note string, observable bool, reasonCode, kind string) Control {
	if !observable {
		return o.unknown(c, note)
	}
	allowed := o.allowedListeners(ctx)
	var undeclared []string
	seen := map[string]bool{}
	for _, listener := range found {
		if allowed[listener.Listener] {
			continue
		}
		if text := listener.String(); !seen[text] {
			seen[text] = true
			undeclared = append(undeclared, text)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) == 0 {
		observed := fmt.Sprintf("%d non-loopback %s listeners, all declared or ssh", len(found), kind)
		if reasonCode != "" && len(found) > 0 {
			observed += "; published ports are not filtered by the host input firewall"
		}
		if note != "" {
			observed += "; " + note
		}
		return o.finish(c, StateCompliant, observed, "")
	}
	c.ReasonCode = reasonCode
	summary := fmt.Sprintf("%d undeclared non-loopback %s listeners: %s", len(undeclared), kind, strings.Join(limitList(undeclared, 8), ", "))
	if o.options.DeclaredListeners == nil {
		c.ReasonCode = ""
		return o.finish(c, StateUnknown, summary, "no declared-service authority is available; pass --resolved-plan with --local-node or --declared-port to judge them")
	}
	reason := "services listen beyond loopback without being declared (" + valueOr(o.options.DeclaredAuthority, "declared ports") + ")"
	if reasonCode != "" {
		reason = reasonCode + ": " + reason + "; the host input firewall does not filter container-published ports"
	}
	return o.finish(c, StateDrifted, summary, reason)
}

// publishedListeners reads container-published ports. No docker means no
// published ports; a docker that cannot be queried is not observable.
func (o *observation) publishedListeners(ctx context.Context) (found []observedListener, note string, observable bool) {
	if _, installed := o.engine.toolPath("docker"); !installed {
		return nil, "no container runtime is installed", true
	}
	output, err := o.engine.run(ctx, "docker", "ps", "--format", "{{.Ports}}")
	if err != nil {
		return nil, "docker could not list containers: " + err.Error(), false
	}
	if output.ExitCode != 0 {
		return nil, "docker ps failed: " + boundedText(output.Stderr), false
	}
	return parseContainerPorts(output.Stdout), "", true
}

func limitList(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return append(append([]string(nil), values[:limit]...), fmt.Sprintf("and %d more", len(values)-limit))
}

const (
	certificateMinRemaining = 30 * 24 * time.Hour
	maxCertificateFileBytes = 1 << 20
)

// certificates judges the certificates that are discoverable on this host: the
// step-ca root and intermediate in workspace custody, and the leaf certificates
// Traefik holds in an ACME volume. It reports nothing when none is
// discoverable, so a host without a private CA is not penalized.
func (o *observation) certificates(ctx context.Context) (Control, bool) {
	c := o.control(ControlCertificateExpiry, fmt.Sprintf("CA certificates valid for at least %d days and no expired leaf certificate", int(certificateMinRemaining/(24*time.Hour))),
		Remediation{Capability: RemediationManual, Action: "renew the certificate authority material or let the ACME resolver renew the leaf"})
	now := o.now()
	var lines []string
	var drifted []string
	inspect := func(label string, cert *x509.Certificate, leaf bool) {
		remaining := cert.NotAfter.Sub(now)
		lines = append(lines, fmt.Sprintf("%s expires %s", label, cert.NotAfter.UTC().Format(time.RFC3339)))
		switch {
		case remaining <= 0:
			drifted = append(drifted, label+" expired")
		case !leaf && remaining < certificateMinRemaining:
			drifted = append(drifted, fmt.Sprintf("%s expires in %d days", label, int(remaining/(24*time.Hour))))
		}
	}
	if o.options.WorkspaceRoot != "" {
		base := o.options.WorkspaceRoot + "/.stackkit/custody/basement-runtime/step-ca/certs/"
		for _, name := range []string{"root_ca.crt", "intermediate_ca.crt"} {
			raw, err := o.engine.Host.ReadFile(base + name)
			if err != nil || len(raw) > maxCertificateFileBytes {
				continue
			}
			for _, cert := range parseCertificates(raw) {
				inspect("step-ca "+name, cert, false)
			}
		}
	}
	for _, cert := range o.acmeLeafCertificates(ctx) {
		inspect("traefik leaf "+valueOr(cert.Subject.CommonName, "certificate"), cert, true)
	}
	if len(lines) == 0 {
		o.note("certificate expiry: no step-ca custody or Traefik ACME certificate is discoverable on this host, so the control is not reported")
		return Control{}, false
	}
	if len(drifted) > 0 {
		return o.finish(c, StateDrifted, strings.Join(limitList(lines, 6), "; "), strings.Join(drifted, "; ")), true
	}
	return o.finish(c, StateCompliant, strings.Join(limitList(lines, 6), "; "), ""), true
}

func parseCertificates(raw []byte) []*x509.Certificate {
	var certificates []*x509.Certificate
	for {
		block, rest := pem.Decode(raw)
		if block == nil {
			return certificates
		}
		raw = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			certificates = append(certificates, cert)
		}
	}
}

// acmeLeafCertificates reads the leaf certificates of Traefik ACME storage in
// Docker volumes whose name mentions acme. Anything unreadable is skipped: this
// is a best-effort discovery, not a requirement.
func (o *observation) acmeLeafCertificates(ctx context.Context) []*x509.Certificate {
	volumes, err := o.engine.run(ctx, "docker", "volume", "ls", "--format", "{{.Name}}")
	if err != nil || volumes.ExitCode != 0 {
		return nil
	}
	var leaves []*x509.Certificate
	for _, name := range strings.Fields(volumes.Stdout) {
		if !strings.Contains(name, "acme") {
			continue
		}
		inspected, inspectErr := o.engine.run(ctx, "docker", "volume", "inspect", "--format", "{{.Mountpoint}}", name)
		if inspectErr != nil || inspected.ExitCode != 0 {
			continue
		}
		mountpoint := strings.TrimSpace(inspected.Stdout)
		if mountpoint == "" {
			continue
		}
		raw, readErr := o.engine.Host.ReadFile(mountpoint + "/acme.json")
		if readErr != nil || len(raw) > 8<<20 {
			continue
		}
		var store map[string]struct {
			Certificates []struct {
				Certificate string `json:"certificate"`
			} `json:"Certificates"`
		}
		if json.Unmarshal(raw, &store) != nil {
			continue
		}
		for _, resolver := range store {
			for _, entry := range resolver.Certificates {
				decoded, decodeErr := base64.StdEncoding.DecodeString(entry.Certificate)
				if decodeErr != nil {
					continue
				}
				if certs := parseCertificates(decoded); len(certs) > 0 {
					leaves = append(leaves, certs[0])
				}
			}
		}
	}
	return leaves
}
