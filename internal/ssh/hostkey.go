package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Typed reasons for refusing an SSH target before any operation runs.
const (
	// ReasonHostKeyRequired means no expected host key was supplied and the
	// explicit first-contact mode was not requested.
	ReasonHostKeyRequired = "ssh_host_key_required"
	// ReasonHostKeyInvalid means the supplied host key is not one OpenSSH
	// public key.
	ReasonHostKeyInvalid = "ssh_host_key_invalid"
	// ReasonHostKeyMismatch means the target presented a different host key
	// than the one the caller pinned.
	ReasonHostKeyMismatch = "ssh_host_key_mismatch"
	// ReasonHostKeyUnreachable means first-contact pinning could not observe a
	// host key at all.
	ReasonHostKeyUnreachable = "ssh_host_key_unreachable"
)

// HostKeyError is the typed refusal for host key pinning. It never carries the
// private key material, only public fingerprints.
type HostKeyError struct {
	Reason   string
	Address  string
	Expected string
	Observed string
	Err      error
}

func (e *HostKeyError) Error() string {
	var b strings.Builder
	b.WriteString(e.Reason)
	if e.Address != "" {
		b.WriteString(" for " + e.Address)
	}
	if e.Expected != "" || e.Observed != "" {
		fmt.Fprintf(&b, " (expected %s, observed %s)", orNone(e.Expected), orNone(e.Observed))
	}
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

func (e *HostKeyError) Unwrap() error { return e.Err }

func orNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// HostKeyReason returns the typed reason of a host key refusal, or "".
func HostKeyReason(err error) string {
	var hostKeyErr *HostKeyError
	if errors.As(err, &hostKeyErr) {
		return hostKeyErr.Reason
	}
	return ""
}

// HostKeyPin is the host key every later SSH connection to the target must
// present.
type HostKeyPin struct {
	Key         ssh.PublicKey
	Fingerprint string
	Address     string
	// FirstContact is true when the key was observed rather than supplied by
	// the caller. Callers must record this in the operation receipt.
	FirstContact bool
}

// KnownHostsLine renders the pin as one known_hosts entry (unhashed).
func (p HostKeyPin) KnownHostsLine() string {
	return knownhosts.Line([]string{p.Address}, p.Key)
}

// WriteKnownHosts writes the pin as a private known_hosts file for ssh(1).
func (p HostKeyPin) WriteKnownHosts(path string) error {
	return os.WriteFile(path, []byte(p.KnownHostsLine()+"\n"), 0o600)
}

// ParseHostKey parses one OpenSSH public key line (authorized_keys format, the
// shape Techstack persists at enrolment).
func ParseHostKey(line string) (ssh.PublicKey, error) {
	line = strings.TrimSpace(line)
	if line == "" || len(line) > 16*1024 || strings.ContainsAny(line, "\r\n") {
		return nil, errors.New("host key must be a single OpenSSH public key line")
	}
	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(rest))) > 0 {
		return nil, errors.New("host key must hold exactly one key")
	}
	return key, nil
}

// HostKeyProbe observes the host key a target presents. It is a variable so
// tests can stand in for the network.
var HostKeyProbe = ProbeHostKey

// HostKeyAlgorithmsFor returns the SSH host key signature algorithms a probe
// must offer to be shown the given key. A client that offers its defaults is
// shown ECDSA or RSA first by a stock sshd, which holds several host keys, so
// a pinned ed25519 key would be reported as a mismatch.
func HostKeyAlgorithmsFor(key ssh.PublicKey) []string {
	if key.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{key.Type()}
}

// firstContactAttempts and firstContactBackoff bound how long first-contact
// pinning waits for a booting host.
var (
	firstContactAttempts = 6
	firstContactBackoff  = 5 * time.Second
)

var errHostKeyCaptured = errors.New("host key captured")

// ProbeHostKey completes the SSH key exchange with host:port, captures the
// presented host key and aborts before any authentication. algorithms limits
// the host key signature algorithms offered to the server; nil keeps the
// library defaults.
func ProbeHostKey(ctx context.Context, host string, port int, timeout time.Duration, algorithms []string) (ssh.PublicKey, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	var captured ssh.PublicKey
	config := &ssh.ClientConfig{
		User: "stackkits-hostkey-probe",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			captured = key
			return errHostKeyCaptured
		},
		HostKeyAlgorithms: algorithms,
		Timeout:           timeout,
	}
	_, _, _, handshakeErr := ssh.NewClientConn(conn, address, config)
	if captured == nil {
		if handshakeErr == nil {
			handshakeErr = errors.New("no host key presented")
		}
		return nil, handshakeErr
	}
	return captured, nil
}

// ResolveHostKey returns the pin every connection to host:port must satisfy.
//
//   - expected set: it is parsed and, when the target is reachable, compared
//     with the key the target presents. A different key is ReasonHostKeyMismatch.
//     An unreachable target is not an error here; the later connection still
//     enforces the pin through StrictHostKeyChecking.
//   - expected empty and firstContact false: ReasonHostKeyRequired. There is no
//     silent trust on first use.
//   - expected empty and firstContact true: the presented key is observed and
//     pinned for this operation; the caller must record the fingerprint.
func ResolveHostKey(ctx context.Context, host string, port int, expected string, firstContact bool) (*HostKeyPin, error) {
	if port <= 0 {
		port = 22
	}
	address := knownhosts.Normalize(net.JoinHostPort(host, strconv.Itoa(port)))
	expected = strings.TrimSpace(expected)
	if expected == "" {
		if !firstContact {
			return nil, &HostKeyError{Reason: ReasonHostKeyRequired, Address: address}
		}
		return observeFirstContact(ctx, host, port, address)
	}
	pinned, err := ParseHostKey(expected)
	if err != nil {
		return nil, &HostKeyError{Reason: ReasonHostKeyInvalid, Address: address, Err: err}
	}
	pin := &HostKeyPin{Key: pinned, Fingerprint: ssh.FingerprintSHA256(pinned), Address: address}
	observed, probeErr := HostKeyProbe(ctx, host, port, 15*time.Second, HostKeyAlgorithmsFor(pinned))
	if probeErr != nil {
		// Not reachable (yet), or the target holds no key of the pinned type:
		// nothing was trusted, and ssh(1) enforces the pin.
		return pin, nil
	}
	if !bytes.Equal(observed.Marshal(), pinned.Marshal()) {
		return nil, &HostKeyError{
			Reason:   ReasonHostKeyMismatch,
			Address:  address,
			Expected: pin.Fingerprint,
			Observed: ssh.FingerprintSHA256(observed),
		}
	}
	return pin, nil
}

func observeFirstContact(ctx context.Context, host string, port int, address string) (*HostKeyPin, error) {
	var lastErr error
	for attempt := 1; attempt <= firstContactAttempts; attempt++ {
		// Prefer ed25519, the key type Guard persists at enrolment; a host
		// without one is observed with the library defaults.
		key, err := HostKeyProbe(ctx, host, port, 15*time.Second, []string{ssh.KeyAlgoED25519})
		if err != nil {
			key, err = HostKeyProbe(ctx, host, port, 15*time.Second, nil)
		}
		if err == nil {
			return &HostKeyPin{Key: key, Fingerprint: ssh.FingerprintSHA256(key), Address: address, FirstContact: true}, nil
		}
		lastErr = err
		if attempt == firstContactAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, &HostKeyError{Reason: ReasonHostKeyUnreachable, Address: address, Err: ctx.Err()}
		case <-time.After(firstContactBackoff):
		}
	}
	return nil, &HostKeyError{Reason: ReasonHostKeyUnreachable, Address: address, Err: lastErr}
}

// SSHVerificationFailed reports whether ssh(1) output names a host key
// verification refusal, so retry loops stop instead of waiting for a host that
// will never match.
func SSHVerificationFailed(output string) bool {
	return strings.Contains(output, "Host key verification failed") ||
		strings.Contains(output, "REMOTE HOST IDENTIFICATION HAS CHANGED")
}
